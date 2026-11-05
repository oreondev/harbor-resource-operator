//go:build e2e

/*
Copyright 2026 Oreon IT.

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
package e2e

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	harborv1alpha1 "github.com/oreondev/harbor-resource-operator/api/v1alpha1"
	"github.com/oreondev/harbor-resource-operator/pkg/harbor"
)

const (
	compNS      = "e2e-compliance"
	compProject = "e2e-compliance"
	compUser    = "e2e-dev"
)

// These specs drive the milestone 2 kinds against a real Harbor. Several of
// them check behaviour the fake server only assumes: how Harbor reports an
// unknown user, whether DELETE /retentions clears the project's retention_id,
// and the POST-then-PUT semantics of the GC schedule.
var _ = Describe("Harbor compliance resources", Ordered, func() {
	ctx := context.Background()
	var hc *harbor.Client
	var projectID int64
	conn := harborv1alpha1.ConnectionRef{Name: connName, Namespace: compNS}
	short := &metav1.Duration{Duration: 15 * time.Second}
	inProject := harborv1alpha1.ProjectReference{ProjectRef: &harborv1alpha1.LocalObjectRef{Name: compProject}}

	BeforeAll(func() {
		hc = harborAdmin()
		createNamespace(compNS)
		Expect(k8s.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "harbor-admin", Namespace: compNS},
			StringData: map[string]string{"username": "admin", "password": harborAdminPassword},
		})).To(Succeed())
		c := &harborv1alpha1.HarborConnection{
			ObjectMeta: metav1.ObjectMeta{Name: connName, Namespace: compNS},
			Spec: harborv1alpha1.HarborConnectionSpec{
				URL:                  harborInClusterURL,
				CredentialsSecretRef: harborv1alpha1.LocalObjectRef{Name: "harbor-admin"},
				SyncPeriod:           short,
			},
		}
		Expect(k8s.Create(ctx, c)).To(Succeed())
		expectReady(c, 2*time.Minute)

		p := &harborv1alpha1.HarborProject{
			ObjectMeta: metav1.ObjectMeta{Name: compProject, Namespace: compNS},
			Spec: harborv1alpha1.HarborProjectSpec{
				ConnectionRef: conn, DeletionPolicy: harborv1alpha1.DeletionPolicyDelete, SyncPeriod: short,
			},
		}
		Expect(k8s.Create(ctx, p)).To(Succeed())
		expectReady(p, 2*time.Minute)
		projectID = *p.Status.ProjectID

		// A database user for the membership specs (the chart uses db_auth).
		code, body, err := harborRaw(http.MethodPost, "/users", map[string]string{
			"username": compUser, "email": compUser + "@example.com", "realname": "e2e developer",
			"password": "E2eDev12345", "comment": "created by the e2e suite",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(code).To(SatisfyAny(Equal(http.StatusCreated), Equal(http.StatusConflict)), string(body))
	})

	AfterEach(func() {
		if CurrentSpecReport().Failed() {
			dumpDebug(compNS)
		}
	})

	AfterAll(func() {
		if os.Getenv("E2E_SKIP_CLEANUP") == "true" {
			return
		}
		for _, l := range []client.ObjectList{
			&harborv1alpha1.HarborProjectMemberList{}, &harborv1alpha1.HarborLabelList{},
			&harborv1alpha1.HarborImmutableRuleList{}, &harborv1alpha1.HarborWebhookList{},
			&harborv1alpha1.HarborRetentionPolicyList{}, &harborv1alpha1.HarborGarbageCollectionList{},
		} {
			_ = k8s.List(ctx, l, client.InNamespace(compNS))
			items, _ := meta.ExtractList(l)
			for _, o := range items {
				if obj, ok := o.(client.Object); ok {
					_ = k8s.Delete(ctx, obj)
				}
			}
		}
		_ = k8s.Delete(ctx, &harborv1alpha1.HarborProject{
			ObjectMeta: metav1.ObjectMeta{Name: compProject, Namespace: compNS},
		})
		Eventually(func() int {
			var list harborv1alpha1.HarborProjectList
			_ = k8s.List(ctx, &list, client.InNamespace(compNS))
			return len(list.Items)
		}, 2*time.Minute, 2*time.Second).Should(BeZero())
		_ = k8s.DeleteAllOf(ctx, &harborv1alpha1.HarborConnection{}, client.InNamespace(compNS))
		if got, err := hc.GetProjectByName(ctx, compProject); err == nil {
			_ = hc.DeleteProject(ctx, got.ProjectID)
		}
		_ = hc.UpdateGCSchedule(ctx, harbor.GCSchedule{Schedule: &harbor.ScheduleObj{Type: harbor.ScheduleNone}})
		_ = k8s.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: compNS}})
	})

	It("adds a user to the project, corrects a role changed in Harbor, and removes the member", func() {
		m := &harborv1alpha1.HarborProjectMember{
			ObjectMeta: metav1.ObjectMeta{Name: "dev", Namespace: compNS},
			Spec: harborv1alpha1.HarborProjectMemberSpec{
				ConnectionRef: conn, ProjectReference: inProject,
				User: new(compUser), Role: harborv1alpha1.RoleDeveloper, SyncPeriod: short,
			},
		}
		Expect(k8s.Create(ctx, m)).To(Succeed())
		expectReady(m, 2*time.Minute)
		member, err := hc.GetProjectMember(ctx, projectID, *m.Status.MemberID)
		Expect(err).NotTo(HaveOccurred())
		Expect(member.EntityName).To(Equal(compUser))
		Expect(member.RoleID).To(Equal(int64(harbor.RoleDeveloper)))

		Expect(hc.UpdateProjectMemberRole(ctx, projectID, member.ID, harbor.RoleGuest)).To(Succeed())
		Eventually(func(g Gomega) {
			got, err := hc.GetProjectMember(ctx, projectID, member.ID)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(got.RoleID).To(Equal(int64(harbor.RoleDeveloper)))
		}, 2*time.Minute, 2*time.Second).Should(Succeed())

		Expect(k8s.Delete(ctx, m)).To(Succeed())
		expectGone(m, time.Minute)
		_, err = hc.GetProjectMember(ctx, projectID, member.ID)
		Expect(harbor.IsNotFound(err)).To(BeTrue(), "member should be removed from Harbor, got %v", err)
	})

	It("reports a user that does not exist in Harbor without creating anything", func() {
		m := &harborv1alpha1.HarborProjectMember{
			ObjectMeta: metav1.ObjectMeta{Name: "ghost", Namespace: compNS},
			Spec: harborv1alpha1.HarborProjectMemberSpec{
				ConnectionRef: conn, ProjectReference: inProject,
				User: new("e2e-no-such-user"), Role: harborv1alpha1.RoleGuest,
			},
		}
		Expect(k8s.Create(ctx, m)).To(Succeed())
		Eventually(func(g Gomega) {
			c := readyCondition(g, m)
			g.Expect(c).NotTo(BeNil())
			g.Expect(c.Status).To(Equal("False"))
			g.Expect(c.Reason).NotTo(Equal(harborv1alpha1.ReasonReconcileInProgress))
			GinkgoWriter.Printf("unknown user reported as %s: %s\n", c.Reason, c.Message)
		}, time.Minute, 2*time.Second).Should(Succeed())
		Expect(hc.ListProjectMembers(ctx, projectID, "e2e-no-such-user")).To(BeEmpty())
		Expect(k8s.Delete(ctx, m)).To(Succeed())
		expectGone(m, time.Minute)
	})

	It("manages project and global labels", func() {
		pl := &harborv1alpha1.HarborLabel{
			ObjectMeta: metav1.ObjectMeta{Name: "rc", Namespace: compNS},
			Spec: harborv1alpha1.HarborLabelSpec{
				ConnectionRef: conn, ProjectReference: inProject, Name: "release-candidate",
				Scope: harborv1alpha1.LabelScope("project"), Color: "#2E7D32", Description: new("promoted"),
				SyncPeriod: short,
			},
		}
		gl := &harborv1alpha1.HarborLabel{
			ObjectMeta: metav1.ObjectMeta{Name: "e2e-global", Namespace: compNS},
			Spec: harborv1alpha1.HarborLabelSpec{
				ConnectionRef: conn, Scope: harborv1alpha1.LabelScope("global"), Color: "#C62828",
			},
		}
		Expect(k8s.Create(ctx, pl)).To(Succeed())
		Expect(k8s.Create(ctx, gl)).To(Succeed())
		expectReady(pl, 2*time.Minute)
		expectReady(gl, 2*time.Minute)

		got, err := hc.GetLabel(ctx, *pl.Status.LabelID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Name).To(Equal("release-candidate"))
		Expect(got.Scope).To(Equal(harbor.LabelScopeProject))
		Expect(got.ProjectID).To(Equal(projectID))
		Expect(got.Color).To(MatchRegexp("(?i)^#2E7D32$"))
		Expect(hc.ListLabels(ctx, harbor.LabelScopeGlobal, 0, "e2e-global")).To(HaveLen(1))

		got.Color = "#000000"
		Expect(hc.UpdateLabel(ctx, got.ID, *got)).To(Succeed())
		Eventually(func(g Gomega) {
			l, err := hc.GetLabel(ctx, got.ID)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(l.Color).To(MatchRegexp("(?i)^#2E7D32$"))
		}, 2*time.Minute, 2*time.Second).Should(Succeed())

		for _, l := range []*harborv1alpha1.HarborLabel{pl, gl} {
			id := *l.Status.LabelID
			Expect(k8s.Delete(ctx, l)).To(Succeed())
			expectGone(l, time.Minute)
			_, err := hc.GetLabel(ctx, id)
			Expect(harbor.IsNotFound(err)).To(BeTrue(), "label %d should be deleted, got %v", id, err)
		}
	})

	It("creates an immutable tag rule, re-enables it after drift, and deletes it", func() {
		r := &harborv1alpha1.HarborImmutableRule{
			ObjectMeta: metav1.ObjectMeta{Name: "releases", Namespace: compNS},
			Spec: harborv1alpha1.HarborImmutableRuleSpec{
				ConnectionRef: conn, ProjectReference: inProject,
				Repositories: harborv1alpha1.PatternSelector{Pattern: "**"},
				Tags:         harborv1alpha1.PatternSelector{Decoration: harborv1alpha1.SelectorMatches, Pattern: "v*"},
				SyncPeriod:   short,
			},
		}
		Expect(k8s.Create(ctx, r)).To(Succeed())
		expectReady(r, 2*time.Minute)
		rule := immutableRule(hc, projectID, *r.Status.RuleID)
		Expect(rule).NotTo(BeNil())
		Expect(rule.Disabled).To(BeFalse())
		Expect(rule.TagSelectors).To(ContainElement(HaveField("Pattern", "v*")))

		rule.Disabled = true
		Expect(hc.UpdateImmutableRule(ctx, projectID, rule.ID, *rule)).To(Succeed())
		Eventually(func(g Gomega) {
			got := immutableRule(hc, projectID, rule.ID)
			g.Expect(got).NotTo(BeNil())
			g.Expect(got.Disabled).To(BeFalse())
		}, 2*time.Minute, 2*time.Second).Should(Succeed())

		Expect(k8s.Delete(ctx, r)).To(Succeed())
		expectGone(r, time.Minute)
		Expect(immutableRule(hc, projectID, rule.ID)).To(BeNil())
	})

	It("creates a webhook with an auth header from a Secret and follows Secret changes", func() {
		Expect(k8s.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "hook-auth", Namespace: compNS},
			StringData: map[string]string{"authorization": "Bearer one"},
		})).To(Succeed())
		w := &harborv1alpha1.HarborWebhook{
			ObjectMeta: metav1.ObjectMeta{Name: "ci", Namespace: compNS},
			Spec: harborv1alpha1.HarborWebhookSpec{
				ConnectionRef: conn, ProjectReference: inProject, Name: "e2e-ci",
				EventTypes: []string{"PUSH_ARTIFACT", "DELETE_ARTIFACT"},
				Targets: []harborv1alpha1.WebhookTarget{{
					Type: harborv1alpha1.WebhookTargetType("http"), Address: "http://ci.e2e.invalid/hook",
					AuthHeaderSecretRef: &harborv1alpha1.SecretKeyRef{Name: "hook-auth", Key: "authorization"},
				}},
				SyncPeriod: short,
			},
		}
		Expect(k8s.Create(ctx, w)).To(Succeed())
		expectReady(w, 2*time.Minute)
		pol, err := hc.GetWebhookPolicy(ctx, projectID, *w.Status.WebhookID)
		Expect(err).NotTo(HaveOccurred())
		Expect(pol.Name).To(Equal("e2e-ci"))
		Expect(pol.Enabled).To(BeTrue())
		Expect(pol.EventTypes).To(ConsistOf("PUSH_ARTIFACT", "DELETE_ARTIFACT"))
		Expect(pol.Targets).To(HaveLen(1))
		Expect(pol.Targets[0].Address).To(Equal("http://ci.e2e.invalid/hook"))
		GinkgoWriter.Printf("GET webhook policy returns auth_header: %t\n", pol.Targets[0].AuthHeader != "")
		version := w.Status.AuthSecretsVersion

		s := &corev1.Secret{}
		Expect(k8s.Get(ctx, client.ObjectKey{Namespace: compNS, Name: "hook-auth"}, s)).To(Succeed())
		s.StringData = map[string]string{"authorization": "Bearer two"}
		Expect(k8s.Update(ctx, s)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8s.Get(ctx, client.ObjectKeyFromObject(w), w)).To(Succeed())
			g.Expect(w.Status.AuthSecretsVersion).NotTo(Equal(version))
		}, time.Minute, 2*time.Second).Should(Succeed())
		expectReady(w, time.Minute)
		pol, err = hc.GetWebhookPolicy(ctx, projectID, *w.Status.WebhookID)
		Expect(err).NotTo(HaveOccurred())
		if pol.Targets[0].AuthHeader != "" {
			Expect(pol.Targets[0].AuthHeader).To(Equal("Bearer two"))
		}

		id := *w.Status.WebhookID
		Expect(k8s.Delete(ctx, w)).To(Succeed())
		expectGone(w, time.Minute)
		_, err = hc.GetWebhookPolicy(ctx, projectID, id)
		Expect(harbor.IsNotFound(err)).To(BeTrue(), "webhook should be deleted, got %v", err)
	})

	It("links a retention policy to the project and can recreate it after deletion", func() {
		newPolicy := func() *harborv1alpha1.HarborRetentionPolicy {
			return &harborv1alpha1.HarborRetentionPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "keep-recent", Namespace: compNS},
				Spec: harborv1alpha1.HarborRetentionPolicySpec{
					ConnectionRef: conn, ProjectReference: inProject,
					Rules: []harborv1alpha1.RetentionRule{
						{Template: harborv1alpha1.RetentionTemplate("latestPushedK"), Count: new(int64(3))},
						{Template: harborv1alpha1.RetentionTemplate("always"),
							Tags: harborv1alpha1.RetentionTagSelector{PatternSelector: harborv1alpha1.PatternSelector{Pattern: "v*"}}},
					},
					Schedule:   "0 0 3 * * *",
					SyncPeriod: short,
				},
			}
		}
		rp := newPolicy()
		Expect(k8s.Create(ctx, rp)).To(Succeed())
		expectReady(rp, 2*time.Minute)
		first := *rp.Status.RetentionID
		Expect(projectRetentionID(hc)).To(Equal(strconv.FormatInt(first, 10)))
		got, err := hc.GetRetention(ctx, first)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Rules).To(HaveLen(2))
		Expect(got.Trigger).NotTo(BeNil())
		Expect(got.Trigger.Settings["cron"]).To(Equal("0 0 3 * * *"))

		got.Rules[0].Params = map[string]any{"latestPushedK": 99}
		Expect(hc.UpdateRetention(ctx, first, *got)).To(Succeed())
		Eventually(func(g Gomega) {
			p, err := hc.GetRetention(ctx, first)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(p.Rules[0].Params["latestPushedK"]).To(BeNumerically("==", 3))
		}, 2*time.Minute, 2*time.Second).Should(Succeed())

		Expect(k8s.Delete(ctx, rp)).To(Succeed())
		expectGone(rp, time.Minute)
		_, err = hc.GetRetention(ctx, first)
		GinkgoWriter.Printf("after DELETE /retentions: GET err=%v, project retention_id=%q\n", err, projectRetentionID(hc))

		// Whatever Harbor leaves behind, a new object must get a working policy.
		rp = newPolicy()
		Expect(k8s.Create(ctx, rp)).To(Succeed())
		expectReady(rp, 2*time.Minute)
		Expect(projectRetentionID(hc)).To(Equal(strconv.FormatInt(*rp.Status.RetentionID, 10)))
		Expect(k8s.Delete(ctx, rp)).To(Succeed())
		expectGone(rp, time.Minute)
	})

	It("sets the GC schedule, flags a duplicate, and clears the schedule on delete", func() {
		gc := &harborv1alpha1.HarborGarbageCollection{
			ObjectMeta: metav1.ObjectMeta{Name: "gc", Namespace: compNS},
			Spec: harborv1alpha1.HarborGarbageCollectionSpec{
				ConnectionRef: conn, Schedule: "0 0 4 * * 0", DeleteUntagged: true, Workers: 2,
				DeletionPolicy: harborv1alpha1.DeletionPolicyDelete, SyncPeriod: short,
			},
		}
		Expect(k8s.Create(ctx, gc)).To(Succeed())
		expectReady(gc, 2*time.Minute)
		s, err := hc.GetGCSchedule(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Schedule).NotTo(BeNil())
		Expect(s.Schedule.Type).To(Equal(harbor.ScheduleCustom))
		Expect(s.Schedule.Cron).To(Equal("0 0 4 * * 0"))
		Expect(s.Parameters["delete_untagged"]).To(BeTrue())
		Expect(s.Parameters["workers"]).To(BeNumerically("==", 2))

		// The first write is a POST; changing the spec exercises the PUT path.
		base := gc.DeepCopy()
		gc.Spec.Schedule = "Daily"
		Expect(k8s.Patch(ctx, gc, client.MergeFrom(base))).To(Succeed())
		Eventually(func(g Gomega) {
			s, err := hc.GetGCSchedule(ctx)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(s.Schedule.Type).To(Equal(harbor.ScheduleDaily))
		}, 2*time.Minute, 2*time.Second).Should(Succeed())
		expectReady(gc, time.Minute)

		dup := &harborv1alpha1.HarborGarbageCollection{
			ObjectMeta: metav1.ObjectMeta{Name: "gc-dup", Namespace: compNS},
			Spec:       harborv1alpha1.HarborGarbageCollectionSpec{ConnectionRef: conn, Schedule: "Hourly"},
		}
		Expect(k8s.Create(ctx, dup)).To(Succeed())
		expectNotReady(dup, harborv1alpha1.ReasonDuplicate, time.Minute)
		Expect(k8s.Delete(ctx, dup)).To(Succeed())
		expectGone(dup, time.Minute)
		s, err = hc.GetGCSchedule(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Schedule.Type).To(Equal(harbor.ScheduleDaily), "a duplicate must never touch Harbor")

		Expect(k8s.Delete(ctx, gc)).To(Succeed())
		expectGone(gc, time.Minute)
		s, err = hc.GetGCSchedule(ctx)
		Expect(err).NotTo(HaveOccurred())
		if s.Schedule != nil {
			Expect(s.Schedule.Type).To(Equal(harbor.ScheduleNone))
		}
	})
})

// immutableRule returns the project's immutable rule with id, or nil.
func immutableRule(hc *harbor.Client, projectID, id int64) *harbor.TagRule {
	GinkgoHelper()
	rules, err := hc.ListImmutableRules(context.Background(), projectID)
	Expect(err).NotTo(HaveOccurred())
	for i := range rules {
		if rules[i].ID == id {
			return &rules[i]
		}
	}
	return nil
}

// projectRetentionID returns the retention_id metadata of the compliance project.
func projectRetentionID(hc *harbor.Client) string {
	GinkgoHelper()
	p, err := hc.GetProjectByName(context.Background(), compProject)
	Expect(err).NotTo(HaveOccurred())
	return p.Metadata.RetentionID
}
