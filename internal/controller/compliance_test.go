/*
Copyright 2026 Fahri Cap.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package controller

import (
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	harborv1alpha1 "github.com/oreondev/harbor-resource-operator/api/v1alpha1"
	"github.com/oreondev/harbor-resource-operator/internal/harbor"
)

// readyOf returns "Status/Reason" of the Ready condition after a fresh read.
func readyOf(obj harborResource) func() string {
	return func() string {
		_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj)
		return readyReason(obj.GetConditions())
	}
}

// goneFrom reports whether obj no longer exists.
func goneFrom(obj client.Object) func() bool {
	return func() bool {
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj)
		return err != nil && client.IgnoreNotFound(err) == nil
	}
}

// readyProject creates a HarborProject and waits for it.
func readyProject(ns, name string) *harborv1alpha1.HarborProject {
	p := &harborv1alpha1.HarborProject{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       harborv1alpha1.HarborProjectSpec{ConnectionRef: harborv1alpha1.ConnectionRef{Name: "conn"}},
	}
	Expect(k8sClient.Create(ctx, p)).To(Succeed())
	Eventually(func() string {
		_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(p), p)
		return readyReason(p.Status.Conditions)
	}, timeout, interval).Should(Equal("True/" + harborv1alpha1.ReasonSynced))
	return p
}

var synced = "True/" + harborv1alpha1.ReasonSynced
var testConn = harborv1alpha1.ConnectionRef{Name: "conn"}

// readyCond returns the Ready condition (never nil).
func readyCond(conds []metav1.Condition) metav1.Condition {
	for _, c := range conds {
		if c.Type == harborv1alpha1.ConditionReady {
			return c
		}
	}
	return metav1.Condition{}
}

func inProject(name string) harborv1alpha1.ProjectReference {
	return harborv1alpha1.ProjectReference{ProjectRef: &harborv1alpha1.LocalObjectRef{Name: name}}
}

var _ = Describe("HarborProjectMember", func() {
	It("adds a user, follows role changes, corrects drift and removes the member", func() {
		ns := newNamespace()
		newConnection(ns, "conn", 2*time.Second)
		proj := readyProject(ns, "members-a")
		harborSrv.AddUser("alice")

		m := &harborv1alpha1.HarborProjectMember{
			ObjectMeta: metav1.ObjectMeta{Name: "alice", Namespace: ns},
			Spec: harborv1alpha1.HarborProjectMemberSpec{
				ConnectionRef: testConn, ProjectReference: inProject("members-a"),
				User: new("alice"), Role: harborv1alpha1.RoleDeveloper,
			},
		}
		Expect(k8sClient.Create(ctx, m)).To(Succeed())
		Eventually(readyOf(m), timeout, interval).Should(Equal(synced))
		roleOf := func() int64 {
			for _, x := range harborSrv.MembersOf(*proj.Status.ProjectID) {
				if x.EntityName == "alice" {
					return x.RoleID
				}
			}
			return 0
		}
		Expect(roleOf()).To(Equal(int64(harbor.RoleDeveloper)))

		By("changing the role in spec")
		base := m.DeepCopy()
		m.Spec.Role = harborv1alpha1.RoleMaintainer
		Expect(k8sClient.Patch(ctx, m, client.MergeFrom(base))).To(Succeed())
		Eventually(roleOf, timeout, interval).Should(Equal(int64(harbor.RoleMaintainer)))

		By("correcting a role changed in Harbor")
		harborSrv.Locked(func() { harborSrv.Members[*m.Status.MemberID].RoleID = harbor.RoleGuest })
		Eventually(roleOf, timeout, interval).Should(Equal(int64(harbor.RoleMaintainer)))
		Eventually(func() []string { return eventSummaries(ns, "alice") }, timeout, interval).
			Should(ContainElement("Normal/DriftCorrected/Update"))

		By("re-adding a member removed in Harbor")
		old := *m.Status.MemberID
		harborSrv.Locked(func() { delete(harborSrv.Members, old) })
		Eventually(func() int64 {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(m), m)
			return deref(m.Status.MemberID)
		}, timeout, interval).ShouldNot(Or(Equal(old), BeZero()))

		By("removing the member on delete")
		Expect(k8sClient.Delete(ctx, m)).To(Succeed())
		Eventually(goneFrom(m), timeout, interval).Should(BeTrue())
		Expect(roleOf()).To(BeZero())
	})

	It("adopts the creator's membership only when asked, adds groups and reports unknown users", func() {
		ns := newNamespace()
		newConnection(ns, "conn", 2*time.Second)
		proj := readyProject(ns, "members-b")

		admin := &harborv1alpha1.HarborProjectMember{
			ObjectMeta: metav1.ObjectMeta{Name: "admin", Namespace: ns},
			Spec: harborv1alpha1.HarborProjectMemberSpec{
				ConnectionRef: testConn, ProjectReference: inProject("members-b"),
				User: new(fakeUser), Role: harborv1alpha1.RoleProjectAdmin, DeletionPolicy: harborv1alpha1.DeletionPolicyRetain,
			},
		}
		Expect(k8sClient.Create(ctx, admin)).To(Succeed())
		Eventually(readyOf(admin), timeout, interval).Should(Equal("False/" + harborv1alpha1.ReasonAlreadyExists))
		base := admin.DeepCopy()
		admin.Spec.AdoptExisting = true
		Expect(k8sClient.Patch(ctx, admin, client.MergeFrom(base))).To(Succeed())
		Eventually(readyOf(admin), timeout, interval).Should(Equal(synced))

		group := &harborv1alpha1.HarborProjectMember{
			ObjectMeta: metav1.ObjectMeta{Name: "devs", Namespace: ns},
			Spec: harborv1alpha1.HarborProjectMemberSpec{
				ConnectionRef: testConn, ProjectReference: harborv1alpha1.ProjectReference{ProjectName: "members-b"},
				Group: &harborv1alpha1.MemberGroup{Name: "devs", Type: harborv1alpha1.GroupTypeOIDC},
				Role:  harborv1alpha1.RoleGuest,
			},
		}
		Expect(k8sClient.Create(ctx, group)).To(Succeed())
		Eventually(readyOf(group), timeout, interval).Should(Equal(synced))
		Expect(harborSrv.MembersOf(*proj.Status.ProjectID)).To(ContainElement(And(
			HaveField("EntityType", "g"), HaveField("EntityName", "devs"), HaveField("RoleID", int64(harbor.RoleGuest)))))

		ghost := &harborv1alpha1.HarborProjectMember{
			ObjectMeta: metav1.ObjectMeta{Name: "ghost", Namespace: ns},
			Spec: harborv1alpha1.HarborProjectMemberSpec{
				ConnectionRef: testConn, ProjectReference: inProject("members-b"),
				User: new("ghost"), Role: harborv1alpha1.RoleGuest,
			},
		}
		Expect(k8sClient.Create(ctx, ghost)).To(Succeed())
		Eventually(readyOf(ghost), timeout, interval).Should(Equal("False/" + harborv1alpha1.ReasonHarborError))
		Expect(readyCond(ghost.Status.Conditions).Message).To(ContainSubstring("not found"))

		By("deleting the adopted admin membership with Retain keeps it in Harbor")
		Expect(k8sClient.Delete(ctx, admin)).To(Succeed())
		Eventually(goneFrom(admin), timeout, interval).Should(BeTrue())
		Expect(harborSrv.MembersOf(*proj.Status.ProjectID)).To(ContainElement(HaveField("EntityName", fakeUser)))
	})

	It("rejects specs with both user and group, and member changes", func() {
		ns := newNamespace()
		bad := &harborv1alpha1.HarborProjectMember{
			ObjectMeta: metav1.ObjectMeta{Name: "bad", Namespace: ns},
			Spec: harborv1alpha1.HarborProjectMemberSpec{
				ConnectionRef: testConn, ProjectReference: inProject("p"), User: new("a"),
				Group: &harborv1alpha1.MemberGroup{Name: "g", Type: harborv1alpha1.GroupTypeHTTP}, Role: harborv1alpha1.RoleGuest,
			},
		}
		Expect(k8sClient.Create(ctx, bad)).To(MatchError(ContainSubstring("set exactly one of user or group")))

		ok := &harborv1alpha1.HarborProjectMember{
			ObjectMeta: metav1.ObjectMeta{Name: "ok", Namespace: ns},
			Spec: harborv1alpha1.HarborProjectMemberSpec{
				ConnectionRef: testConn, ProjectReference: inProject("p"), User: new("a"), Role: harborv1alpha1.RoleGuest,
			},
		}
		Expect(k8sClient.Create(ctx, ok)).To(Succeed())
		base := ok.DeepCopy()
		ok.Spec.User = new("b")
		Expect(k8sClient.Patch(ctx, ok, client.MergeFrom(base))).To(MatchError(ContainSubstring("cannot change")))
	})
})

var _ = Describe("HarborLabel", func() {
	It("creates a project label, corrects drift, refuses conflicts and deletes", func() {
		ns := newNamespace()
		newConnection(ns, "conn", 2*time.Second)
		proj := readyProject(ns, "labels-a")

		l := &harborv1alpha1.HarborLabel{
			ObjectMeta: metav1.ObjectMeta{Name: "approved", Namespace: ns},
			Spec: harborv1alpha1.HarborLabelSpec{
				ConnectionRef: testConn, ProjectReference: inProject("labels-a"),
				Color: "#2E7D32", Description: new("passed QA"),
			},
		}
		Expect(k8sClient.Create(ctx, l)).To(Succeed())
		Eventually(readyOf(l), timeout, interval).Should(Equal(synced))
		var got harbor.Label
		harborSrv.Locked(func() { got = *harborSrv.Labels[*l.Status.LabelID] })
		Expect(got).To(And(HaveField("Name", "approved"), HaveField("Scope", harbor.LabelScopeProject),
			HaveField("ProjectID", *proj.Status.ProjectID), HaveField("Color", "#2E7D32"), HaveField("Description", "passed QA")))

		harborSrv.Locked(func() { harborSrv.Labels[*l.Status.LabelID].Color = "#000000" })
		Eventually(func() string {
			var c string
			harborSrv.Locked(func() { c = harborSrv.Labels[*l.Status.LabelID].Color })
			return c
		}, timeout, interval).Should(Equal("#2E7D32"))

		dup := &harborv1alpha1.HarborLabel{
			ObjectMeta: metav1.ObjectMeta{Name: "dup", Namespace: ns},
			Spec:       harborv1alpha1.HarborLabelSpec{ConnectionRef: testConn, Name: "approved", ProjectReference: inProject("labels-a")},
		}
		Expect(k8sClient.Create(ctx, dup)).To(Succeed())
		Eventually(readyOf(dup), timeout, interval).Should(Equal("False/" + harborv1alpha1.ReasonAlreadyExists))
		Expect(k8sClient.Delete(ctx, dup)).To(Succeed())
		Eventually(goneFrom(dup), timeout, interval).Should(BeTrue())

		id := *l.Status.LabelID
		Expect(k8sClient.Delete(ctx, l)).To(Succeed())
		Eventually(goneFrom(l), timeout, interval).Should(BeTrue())
		harborSrv.Locked(func() { Expect(harborSrv.Labels).NotTo(HaveKey(id)) })
	})

	It("creates global labels and validates scope", func() {
		ns := newNamespace()
		newConnection(ns, "conn", 2*time.Second)
		g := &harborv1alpha1.HarborLabel{
			ObjectMeta: metav1.ObjectMeta{Name: "global-prod", Namespace: ns},
			Spec:       harborv1alpha1.HarborLabelSpec{ConnectionRef: testConn, Scope: harborv1alpha1.LabelScopeGlobal, Color: "#C62828"},
		}
		Expect(k8sClient.Create(ctx, g)).To(Succeed())
		Eventually(readyOf(g), timeout, interval).Should(Equal(synced))
		harborSrv.Locked(func() {
			Expect(harborSrv.Labels[*g.Status.LabelID].Scope).To(Equal(harbor.LabelScopeGlobal))
		})

		bad := &harborv1alpha1.HarborLabel{
			ObjectMeta: metav1.ObjectMeta{Name: "bad", Namespace: ns},
			Spec: harborv1alpha1.HarborLabelSpec{ConnectionRef: testConn, Scope: harborv1alpha1.LabelScopeGlobal,
				ProjectReference: inProject("x")},
		}
		Expect(k8sClient.Create(ctx, bad)).To(MatchError(ContainSubstring("global labels take no project")))
		noProject := &harborv1alpha1.HarborLabel{
			ObjectMeta: metav1.ObjectMeta{Name: "noproj", Namespace: ns},
			Spec:       harborv1alpha1.HarborLabelSpec{ConnectionRef: testConn},
		}
		Expect(k8sClient.Create(ctx, noProject)).To(MatchError(ContainSubstring("project labels need exactly one")))
	})
})

var _ = Describe("HarborImmutableRule", func() {
	It("creates a rule, updates it, recreates it when lost, refuses duplicates and deletes", func() {
		ns := newNamespace()
		newConnection(ns, "conn", 2*time.Second)
		proj := readyProject(ns, "immutable-a")

		rule := &harborv1alpha1.HarborImmutableRule{
			ObjectMeta: metav1.ObjectMeta{Name: "releases", Namespace: ns},
			Spec: harborv1alpha1.HarborImmutableRuleSpec{
				ConnectionRef: testConn, ProjectReference: inProject("immutable-a"),
				Tags: harborv1alpha1.PatternSelector{Pattern: "v*"},
			},
		}
		Expect(k8sClient.Create(ctx, rule)).To(Succeed())
		Eventually(readyOf(rule), timeout, interval).Should(Equal(synced))
		stored := func() harbor.TagRule {
			var r harbor.TagRule
			harborSrv.Locked(func() {
				if ir := harborSrv.ImmutableRules[deref(rule.Status.RuleID)]; ir != nil {
					r = ir.Rule
				}
			})
			return r
		}
		got := stored()
		Expect(got.TagSelectors).To(ConsistOf(harbor.TagSelector{Kind: "doublestar", Decoration: "matches", Pattern: "v*"}))
		Expect(got.ScopeSelectors["repository"]).To(ConsistOf(harbor.TagSelector{Kind: "doublestar", Decoration: "repoMatches", Pattern: "**"}))
		Expect(got.Action).To(Equal("immutable"))

		By("disabling it through spec")
		base := rule.DeepCopy()
		rule.Spec.Disabled = true
		Expect(k8sClient.Patch(ctx, rule, client.MergeFrom(base))).To(Succeed())
		Eventually(func() bool { return stored().Disabled }, timeout, interval).Should(BeTrue())

		By("recreating it after deletion in Harbor")
		old := *rule.Status.RuleID
		harborSrv.Locked(func() { delete(harborSrv.ImmutableRules, old) })
		Eventually(func() int64 {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(rule), rule)
			return deref(rule.Status.RuleID)
		}, timeout, interval).ShouldNot(Or(Equal(old), BeZero()))

		dup := &harborv1alpha1.HarborImmutableRule{
			ObjectMeta: metav1.ObjectMeta{Name: "dup", Namespace: ns},
			Spec: harborv1alpha1.HarborImmutableRuleSpec{
				ConnectionRef: testConn, ProjectReference: inProject("immutable-a"),
				Tags: harborv1alpha1.PatternSelector{Pattern: "v*"},
			},
		}
		Expect(k8sClient.Create(ctx, dup)).To(Succeed())
		Eventually(readyOf(dup), timeout, interval).Should(Equal("False/" + harborv1alpha1.ReasonAlreadyExists))
		Expect(k8sClient.Delete(ctx, dup)).To(Succeed())
		Eventually(goneFrom(dup), timeout, interval).Should(BeTrue())

		Expect(k8sClient.Delete(ctx, rule)).To(Succeed())
		Eventually(goneFrom(rule), timeout, interval).Should(BeTrue())
		harborSrv.Locked(func() {
			for _, ir := range harborSrv.ImmutableRules {
				Expect(ir.ProjectID).NotTo(Equal(*proj.Status.ProjectID))
			}
		})
	})
})

var _ = Describe("HarborWebhook", func() {
	It("creates a webhook with an auth header from a Secret, follows Secret changes, corrects drift and deletes", func() {
		ns := newNamespace()
		newConnection(ns, "conn", 2*time.Second)
		readyProject(ns, "hooks-a")
		sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "siem", Namespace: ns},
			StringData: map[string]string{"header": "Bearer one"}}
		Expect(k8sClient.Create(ctx, sec)).To(Succeed())

		wh := &harborv1alpha1.HarborWebhook{
			ObjectMeta: metav1.ObjectMeta{Name: "siem", Namespace: ns},
			Spec: harborv1alpha1.HarborWebhookSpec{
				ConnectionRef: testConn, ProjectReference: inProject("hooks-a"),
				EventTypes: []string{"PUSH_ARTIFACT", "DELETE_ARTIFACT"},
				Targets: []harborv1alpha1.WebhookTarget{{
					Address:             "https://siem.example.com/harbor",
					AuthHeaderSecretRef: &harborv1alpha1.SecretKeyRef{Name: "siem", Key: "header"},
					PayloadFormat:       "CloudEvents",
				}},
			},
		}
		Expect(k8sClient.Create(ctx, wh)).To(Succeed())
		Eventually(readyOf(wh), timeout, interval).Should(Equal(synced))
		stored := func() harbor.WebhookPolicy {
			var p harbor.WebhookPolicy
			harborSrv.Locked(func() {
				if x := harborSrv.Webhooks[deref(wh.Status.WebhookID)]; x != nil {
					p = *x
				}
			})
			return p
		}
		got := stored()
		Expect(got.Name).To(Equal("siem"))
		Expect(got.Enabled).To(BeTrue())
		Expect(got.EventTypes).To(Equal([]string{"DELETE_ARTIFACT", "PUSH_ARTIFACT"}))
		Expect(got.Targets).To(ConsistOf(harbor.WebhookTarget{Type: "http", Address: "https://siem.example.com/harbor",
			AuthHeader: "Bearer one", PayloadFormat: "CloudEvents"}))
		Expect(readyCond(wh.Status.Conditions).Message).NotTo(ContainSubstring("Bearer"))

		By("rotating the auth header Secret")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sec), sec)).To(Succeed())
		sec.Data["header"] = []byte("Bearer two")
		Expect(k8sClient.Update(ctx, sec)).To(Succeed())
		Eventually(func() string { return stored().Targets[0].AuthHeader }, timeout, interval).Should(Equal("Bearer two"))

		By("correcting a webhook disabled in Harbor")
		harborSrv.Locked(func() { harborSrv.Webhooks[*wh.Status.WebhookID].Enabled = false })
		Eventually(func() bool { return stored().Enabled }, timeout, interval).Should(BeTrue())

		id := *wh.Status.WebhookID
		Expect(k8sClient.Delete(ctx, wh)).To(Succeed())
		Eventually(goneFrom(wh), timeout, interval).Should(BeTrue())
		harborSrv.Locked(func() { Expect(harborSrv.Webhooks).NotTo(HaveKey(id)) })
	})

	It("waits for a missing auth header Secret", func() {
		ns := newNamespace()
		newConnection(ns, "conn", 2*time.Second)
		readyProject(ns, "hooks-b")
		wh := &harborv1alpha1.HarborWebhook{
			ObjectMeta: metav1.ObjectMeta{Name: "nosecret", Namespace: ns},
			Spec: harborv1alpha1.HarborWebhookSpec{
				ConnectionRef: testConn, ProjectReference: inProject("hooks-b"), EventTypes: []string{"PUSH_ARTIFACT"},
				Targets: []harborv1alpha1.WebhookTarget{{Type: harborv1alpha1.WebhookTargetSlack,
					Address: "https://hooks.slack.com/x", AuthHeaderSecretRef: &harborv1alpha1.SecretKeyRef{Name: "missing", Key: "k"}}},
			},
		}
		Expect(k8sClient.Create(ctx, wh)).To(Succeed())
		Eventually(readyOf(wh), timeout, interval).Should(Equal("False/" + harborv1alpha1.ReasonSecretMissing))
		Expect(k8sClient.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "missing", Namespace: ns},
			StringData: map[string]string{"k": "token"}})).To(Succeed())
		Eventually(readyOf(wh), timeout, interval).Should(Equal(synced))
	})
})

var _ = Describe("HarborRetentionPolicy", func() {
	It("creates the project policy, corrects drift, refuses a second policy, and recreates after delete", func() {
		ns := newNamespace()
		newConnection(ns, "conn", 2*time.Second)
		proj := readyProject(ns, "retention-a")

		pol := &harborv1alpha1.HarborRetentionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "keep", Namespace: ns},
			Spec: harborv1alpha1.HarborRetentionPolicySpec{
				ConnectionRef: testConn, ProjectReference: inProject("retention-a"),
				Schedule: "0 0 2 * * *",
				Rules: []harborv1alpha1.RetentionRule{
					{Template: harborv1alpha1.RetainLatestPushedK, Count: new(int64(10))},
					{Template: harborv1alpha1.RetainNDaysSinceLastPull, Count: new(int64(30)),
						Repositories: harborv1alpha1.PatternSelector{Pattern: "release/**"},
						Tags: harborv1alpha1.RetentionTagSelector{PatternSelector: harborv1alpha1.PatternSelector{
							Decoration: harborv1alpha1.SelectorExcludes, Pattern: "tmp-*"}, Untagged: new(false)}},
				},
			},
		}
		Expect(k8sClient.Create(ctx, pol)).To(Succeed())
		Eventually(readyOf(pol), timeout, interval).Should(Equal(synced))
		stored := func() harbor.RetentionPolicy {
			var p harbor.RetentionPolicy
			harborSrv.Locked(func() {
				if x := harborSrv.Retentions[deref(pol.Status.RetentionID)]; x != nil {
					p = *x
				}
			})
			return p
		}
		got := stored()
		Expect(got.Scope.Ref).To(Equal(*proj.Status.ProjectID))
		Expect(got.Trigger.Settings["cron"]).To(Equal("0 0 2 * * *"))
		Expect(got.Rules).To(HaveLen(2))
		Expect(got.Rules[0].Params).To(HaveKeyWithValue("latestPushedK", BeNumerically("==", 10)))
		Expect(got.Rules[0].TagSelectors[0].Extras).To(Equal(`{"untagged":true}`))
		Expect(got.Rules[1].TagSelectors[0]).To(Equal(harbor.TagSelector{Kind: "doublestar", Decoration: "excludes",
			Pattern: "tmp-*", Extras: `{"untagged":false}`}))
		Expect(got.Rules[1].ScopeSelectors["repository"][0].Pattern).To(Equal("release/**"))
		Expect(harborSrv.ProjectByName("retention-a").Metadata.RetentionID).To(Equal(strconv.FormatInt(*pol.Status.RetentionID, 10)))

		By("correcting a schedule changed in Harbor")
		harborSrv.Locked(func() { harborSrv.Retentions[*pol.Status.RetentionID].Trigger.Settings["cron"] = "" })
		Eventually(func() string { return stored().Trigger.Settings["cron"] }, timeout, interval).Should(Equal("0 0 2 * * *"))

		By("refusing a second policy for the same project")
		second := &harborv1alpha1.HarborRetentionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "second", Namespace: ns},
			Spec: harborv1alpha1.HarborRetentionPolicySpec{ConnectionRef: testConn, ProjectReference: inProject("retention-a"),
				Rules: []harborv1alpha1.RetentionRule{{Template: harborv1alpha1.RetainAlways}}},
		}
		Expect(k8sClient.Create(ctx, second)).To(Succeed())
		Eventually(readyOf(second), timeout, interval).Should(Equal("False/" + harborv1alpha1.ReasonAlreadyExists))

		By("deleting the first policy, after which the second one takes the project over")
		id := *pol.Status.RetentionID
		Expect(k8sClient.Delete(ctx, pol)).To(Succeed())
		Eventually(goneFrom(pol), timeout, interval).Should(BeTrue())
		harborSrv.Locked(func() { Expect(harborSrv.Retentions).NotTo(HaveKey(id)) })
		Eventually(readyOf(second), timeout, interval).Should(Equal(synced))
		Expect(*second.Status.RetentionID).NotTo(Equal(id))

		By("recreating a policy deleted in Harbor, which then answers 500 for its ID")
		id = *second.Status.RetentionID
		harborSrv.Locked(func() { harborSrv.DropRetention(id) })
		Eventually(func() int64 {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(second), second)
			return deref(second.Status.RetentionID)
		}, timeout, interval).ShouldNot(Or(BeZero(), Equal(id)))
		Eventually(readyOf(second), timeout, interval).Should(Equal(synced))

		By("on Harbor 2.12 and 2.13, clearing the pointer a deleted policy leaves behind")
		harborSrv.Locked(func() { harborSrv.KeepRetentionPointer = true })
		defer harborSrv.Locked(func() { harborSrv.KeepRetentionPointer = false })
		id = *second.Status.RetentionID
		Expect(k8sClient.Delete(ctx, second)).To(Succeed())
		Eventually(goneFrom(second), timeout, interval).Should(BeTrue())
		Expect(harborSrv.ProjectByName("retention-a").Metadata.RetentionID).To(BeEmpty())

		third := &harborv1alpha1.HarborRetentionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "third", Namespace: ns},
			Spec: harborv1alpha1.HarborRetentionPolicySpec{ConnectionRef: testConn, ProjectReference: inProject("retention-a"),
				Rules: []harborv1alpha1.RetentionRule{{Template: harborv1alpha1.RetainAlways}}},
		}
		Expect(k8sClient.Create(ctx, third)).To(Succeed())
		Eventually(readyOf(third), timeout, interval).Should(Equal(synced))

		By("recreating a policy deleted out of band that left a stale pointer")
		id = *third.Status.RetentionID
		harborSrv.Locked(func() { harborSrv.DropRetention(id) })
		Eventually(func() int64 {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(third), third)
			return deref(third.Status.RetentionID)
		}, timeout, interval).ShouldNot(Or(BeZero(), Equal(id)))
		Eventually(readyOf(third), timeout, interval).Should(Equal(synced))
	})

	It("rejects rules without a count", func() {
		ns := newNamespace()
		bad := &harborv1alpha1.HarborRetentionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "bad", Namespace: ns},
			Spec: harborv1alpha1.HarborRetentionPolicySpec{ConnectionRef: testConn, ProjectReference: inProject("x"),
				Rules: []harborv1alpha1.RetentionRule{{Template: harborv1alpha1.RetainLatestPushedK}}},
		}
		Expect(k8sClient.Create(ctx, bad)).To(MatchError(ContainSubstring("count is required")))
	})
})

var _ = Describe("HarborGarbageCollection", func() {
	It("sets the schedule, reports duplicates, follows changes and hands over on delete", func() {
		ns := newNamespace()
		newConnection(ns, "conn", 2*time.Second)
		harborSrv.AddGCRun("Success")

		gc := &harborv1alpha1.HarborGarbageCollection{
			ObjectMeta: metav1.ObjectMeta{Name: "nightly", Namespace: ns},
			Spec: harborv1alpha1.HarborGarbageCollectionSpec{ConnectionRef: testConn, Schedule: "Daily", DeleteUntagged: true, Workers: 2,
				DeletionPolicy: harborv1alpha1.DeletionPolicyDelete},
		}
		Expect(k8sClient.Create(ctx, gc)).To(Succeed())
		Eventually(readyOf(gc), timeout, interval).Should(Equal(synced))
		schedule := func() harbor.GCSchedule {
			var g harbor.GCSchedule
			harborSrv.Locked(func() {
				if harborSrv.GC != nil {
					g = *harborSrv.GC
				}
			})
			return g
		}
		got := schedule()
		Expect(got.Schedule.Type).To(Equal("Daily"))
		Expect(got.Parameters).To(And(HaveKeyWithValue("delete_untagged", true), HaveKeyWithValue("workers", BeNumerically("==", 2))))
		Expect(gc.Status.NextScheduledTime).NotTo(BeEmpty())
		Expect(gc.Status.LastRun).NotTo(BeNil())
		Expect(gc.Status.LastRun.Status).To(Equal("Success"))

		time.Sleep(1100 * time.Millisecond) // creationTimestamp has second resolution
		dup := &harborv1alpha1.HarborGarbageCollection{
			ObjectMeta: metav1.ObjectMeta{Name: "weekly", Namespace: ns},
			Spec:       harborv1alpha1.HarborGarbageCollectionSpec{ConnectionRef: testConn, Schedule: "Weekly"},
		}
		Expect(k8sClient.Create(ctx, dup)).To(Succeed())
		Eventually(readyOf(dup), timeout, interval).Should(Equal("False/" + harborv1alpha1.ReasonDuplicate))
		Expect(schedule().Schedule.Type).To(Equal("Daily"))

		By("switching to a custom cron")
		base := gc.DeepCopy()
		gc.Spec.Schedule = "0 30 3 * * 6"
		Expect(k8sClient.Patch(ctx, gc, client.MergeFrom(base))).To(Succeed())
		Eventually(func() string { return schedule().Schedule.Cron }, timeout, interval).Should(Equal("0 30 3 * * 6"))
		Expect(schedule().Schedule.Type).To(Equal("Custom"))

		By("correcting workers changed in Harbor")
		harborSrv.Locked(func() { harborSrv.GC.Parameters["workers"] = 5 })
		Eventually(func() any { return normalizeNumber(schedule().Parameters["workers"]) }, timeout, interval).Should(Equal(int64(2)))

		By("deleting the winner hands the schedule to the remaining object")
		Expect(k8sClient.Delete(ctx, gc)).To(Succeed())
		Eventually(goneFrom(gc), timeout, interval).Should(BeTrue())
		Eventually(readyOf(dup), timeout, interval).Should(Equal(synced))
		Expect(schedule().Schedule.Type).To(Equal("Weekly"))

		By("deleting the last object with Retain leaves the schedule")
		Expect(k8sClient.Delete(ctx, dup)).To(Succeed())
		Eventually(goneFrom(dup), timeout, interval).Should(BeTrue())
		Expect(schedule().Schedule.Type).To(Equal("Weekly"))
	})
})
