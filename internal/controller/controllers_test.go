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

package controller

import (
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	harborv1alpha1 "github.com/oreondev/harbor-resource-operator/api/v1alpha1"
	"github.com/oreondev/harbor-resource-operator/pkg/harbor"
)

const (
	timeout  = 20 * time.Second
	interval = 200 * time.Millisecond
)

var nsCounter int

// newNamespace creates a fresh namespace per test so objects never collide.
func newNamespace() string {
	nsCounter++
	name := fmt.Sprintf("t%d-%d", nsCounter, time.Now().UnixNano()%100000)
	Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}})).To(Succeed())
	return name
}

// eventSummaries lists "type/reason/action" of the events.k8s.io/v1 Events
// the operator recorded for the named object.
func eventSummaries(ns, name string) []string {
	var list eventsv1.EventList
	if err := k8sClient.List(ctx, &list, client.InNamespace(ns)); err != nil {
		return nil
	}
	var out []string
	for _, e := range list.Items {
		if e.Regarding.Name == name && e.ReportingController == "harbor-resource-operator" {
			out = append(out, e.Type+"/"+e.Reason+"/"+e.Action)
		}
	}
	return out
}

// newConnection creates a credentials Secret and a HarborConnection that
// allows every namespace, and waits for it to be Ready.
func newConnection(ns, name string, syncPeriod time.Duration) *harborv1alpha1.HarborConnection {
	sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name + "-creds", Namespace: ns},
		StringData: map[string]string{"username": fakeUser, "password": fakePass}}
	Expect(k8sClient.Create(ctx, sec)).To(Succeed())
	conn := &harborv1alpha1.HarborConnection{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: harborv1alpha1.HarborConnectionSpec{
			URL:                  harborSrv.URL(),
			CredentialsSecretRef: harborv1alpha1.LocalObjectRef{Name: name + "-creds"},
			AllowedNamespaces:    []string{"*"},
			SyncPeriod:           &metav1.Duration{Duration: syncPeriod},
		},
	}
	Expect(k8sClient.Create(ctx, conn)).To(Succeed())
	Eventually(func() bool {
		_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(conn), conn)
		return isReady(conn.Status.Conditions)
	}, timeout, interval).Should(BeTrue())
	return conn
}

func readyReason(conds []metav1.Condition) string {
	c := meta.FindStatusCondition(conds, harborv1alpha1.ConditionReady)
	if c == nil {
		return ""
	}
	return string(c.Status) + "/" + c.Reason
}

var _ = Describe("HarborConnection", func() {
	It("becomes Ready and reports the Harbor version", func() {
		ns := newNamespace()
		conn := newConnection(ns, "conn", time.Minute)
		Expect(conn.Status.HarborVersion).To(Equal(harborSrv.Version))
		Expect(conn.Status.Username).To(Equal(fakeUser))
	})

	It("reports AuthenticationFailed with wrong credentials", func() {
		ns := newNamespace()
		sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "bad", Namespace: ns},
			StringData: map[string]string{"username": "admin", "password": "nope"}}
		Expect(k8sClient.Create(ctx, sec)).To(Succeed())
		conn := &harborv1alpha1.HarborConnection{ObjectMeta: metav1.ObjectMeta{Name: "bad", Namespace: ns},
			Spec: harborv1alpha1.HarborConnectionSpec{URL: harborSrv.URL(), CredentialsSecretRef: harborv1alpha1.LocalObjectRef{Name: "bad"}}}
		Expect(k8sClient.Create(ctx, conn)).To(Succeed())
		Eventually(func() string {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(conn), conn)
			return readyReason(conn.Status.Conditions)
		}, timeout, interval).Should(Equal("False/" + harborv1alpha1.ReasonAuthenticationFailed))

		By("recovering as soon as the Secret is fixed, without waiting for the sync period")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sec), sec)).To(Succeed())
		sec.Data = map[string][]byte{"username": []byte(fakeUser), "password": []byte(fakePass)}
		Expect(k8sClient.Update(ctx, sec)).To(Succeed())
		Eventually(func() string {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(conn), conn)
			return readyReason(conn.Status.Conditions)
		}, timeout, interval).Should(Equal("True/" + harborv1alpha1.ReasonConnected))
	})
})

var _ = Describe("HarborProject", func() {
	It("creates, updates, corrects drift and retains on delete", func() {
		ns := newNamespace()
		newConnection(ns, "conn", 2*time.Second)

		proj := &harborv1alpha1.HarborProject{
			ObjectMeta: metav1.ObjectMeta{Name: "team-a", Namespace: ns},
			Spec: harborv1alpha1.HarborProjectSpec{
				ConnectionRef:     harborv1alpha1.ConnectionRef{Name: "conn"},
				Public:            new(false),
				StorageQuotaBytes: new(int64(1 << 30)),
				Metadata:          &harborv1alpha1.ProjectMetadata{AutoScan: new(true), Severity: new("high")},
			},
		}
		Expect(k8sClient.Create(ctx, proj)).To(Succeed())
		Eventually(func() string {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)
			return readyReason(proj.Status.Conditions)
		}, timeout, interval).Should(Equal("True/" + harborv1alpha1.ReasonSynced))
		Expect(proj.Status.ProjectID).NotTo(BeNil())
		hp := harborSrv.ProjectByName("team-a")
		Expect(hp).NotTo(BeNil())
		Expect(hp.Metadata.AutoScan).To(Equal("true"))
		Expect(hp.Metadata.Severity).To(Equal("high"))
		q, ok := harborSrv.QuotaOfProject(hp.ProjectID)
		Expect(ok).To(BeTrue())
		Expect(q).To(Equal(int64(1 << 30)))

		By("applying a spec change")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())
		proj.Spec.Public = new(true)
		proj.Spec.StorageQuotaBytes = new(int64(2 << 30))
		Expect(k8sClient.Update(ctx, proj)).To(Succeed())
		Eventually(func() string {
			p := harborSrv.ProjectByName("team-a")
			if p == nil {
				return ""
			}
			return p.Metadata.Public
		}, timeout, interval).Should(Equal("true"))
		Eventually(func() int64 {
			q, _ := harborSrv.QuotaOfProject(hp.ProjectID)
			return q
		}, timeout, interval).Should(Equal(int64(2 << 30)))

		By("correcting drift made in Harbor")
		harborSrv.Locked(func() {
			harborSrv.Projects[hp.ProjectID].Metadata.AutoScan = "false"
		})
		Eventually(func() string {
			return harborSrv.ProjectByName("team-a").Metadata.AutoScan
		}, timeout, interval).Should(Equal("true"))
		Eventually(func() []string {
			return eventSummaries(ns, "team-a")
		}, timeout, interval).Should(ContainElement("Normal/DriftCorrected/Update"))

		By("deleting with the default Retain policy")
		Expect(k8sClient.Delete(ctx, proj)).To(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)
			return client.IgnoreNotFound(err) == nil && err != nil
		}, timeout, interval).Should(BeTrue())
		Expect(harborSrv.ProjectByName("team-a")).NotTo(BeNil())
	})

	It("deletes from Harbor with deletionPolicy Delete and refuses when not empty", func() {
		ns := newNamespace()
		newConnection(ns, "conn", time.Minute)
		proj := &harborv1alpha1.HarborProject{
			ObjectMeta: metav1.ObjectMeta{Name: "ephemeral", Namespace: ns},
			Spec: harborv1alpha1.HarborProjectSpec{ConnectionRef: harborv1alpha1.ConnectionRef{Name: "conn"},
				DeletionPolicy: harborv1alpha1.DeletionPolicyDelete},
		}
		Expect(k8sClient.Create(ctx, proj)).To(Succeed())
		Eventually(func() bool {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)
			return isReady(proj.Status.Conditions)
		}, timeout, interval).Should(BeTrue())
		id := *proj.Status.ProjectID

		By("blocking deletion while the project holds repositories")
		harborSrv.Locked(func() { harborSrv.Projects[id].RepoCount = 3 })
		Expect(k8sClient.Delete(ctx, proj)).To(Succeed())
		Eventually(func() string {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)
			return readyReason(proj.Status.Conditions)
		}, timeout, interval).Should(Equal("False/" + harborv1alpha1.ReasonProjectNotEmpty))
		Expect(harborSrv.ProjectByName("ephemeral")).NotTo(BeNil())

		By("deleting once the project is empty")
		harborSrv.Locked(func() { harborSrv.Projects[id].RepoCount = 0 })
		// Nudge a reconcile by touching the object.
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())
		proj.Annotations = map[string]string{"nudge": time.Now().String()}
		Expect(k8sClient.Update(ctx, proj)).To(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)
			return err != nil
		}, timeout, interval).Should(BeTrue())
		Expect(harborSrv.ProjectByName("ephemeral")).To(BeNil())
	})

	It("reports AlreadyExists unless adoptExisting is set", func() {
		ns := newNamespace()
		newConnection(ns, "conn", time.Minute)
		harborSrv.AddProject("legacy", true)
		proj := &harborv1alpha1.HarborProject{
			ObjectMeta: metav1.ObjectMeta{Name: "legacy", Namespace: ns},
			Spec:       harborv1alpha1.HarborProjectSpec{ConnectionRef: harborv1alpha1.ConnectionRef{Name: "conn"}, Public: new(false)},
		}
		Expect(k8sClient.Create(ctx, proj)).To(Succeed())
		Eventually(func() string {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)
			return readyReason(proj.Status.Conditions)
		}, timeout, interval).Should(Equal("False/" + harborv1alpha1.ReasonAlreadyExists))
		Expect(harborSrv.ProjectByName("legacy").Metadata.Public).To(Equal("true"), "must not touch the project")

		proj.Spec.AdoptExisting = true
		Expect(k8sClient.Update(ctx, proj)).To(Succeed())
		Eventually(func() bool {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)
			return isReady(proj.Status.Conditions)
		}, timeout, interval).Should(BeTrue())
		Expect(harborSrv.ProjectByName("legacy").Metadata.Public).To(Equal("false"), "adopted project is brought to spec")
	})

	It("refuses a connection that does not allow the namespace", func() {
		connNS := newNamespace()
		otherNS := newNamespace()
		sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: connNS},
			StringData: map[string]string{"username": fakeUser, "password": fakePass}}
		Expect(k8sClient.Create(ctx, sec)).To(Succeed())
		conn := &harborv1alpha1.HarborConnection{ObjectMeta: metav1.ObjectMeta{Name: "private", Namespace: connNS},
			Spec: harborv1alpha1.HarborConnectionSpec{URL: harborSrv.URL(), CredentialsSecretRef: harborv1alpha1.LocalObjectRef{Name: "creds"}}}
		Expect(k8sClient.Create(ctx, conn)).To(Succeed())
		proj := &harborv1alpha1.HarborProject{
			ObjectMeta: metav1.ObjectMeta{Name: "nope", Namespace: otherNS},
			Spec:       harborv1alpha1.HarborProjectSpec{ConnectionRef: harborv1alpha1.ConnectionRef{Name: "private", Namespace: connNS}},
		}
		Expect(k8sClient.Create(ctx, proj)).To(Succeed())
		Eventually(func() string {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)
			return readyReason(proj.Status.Conditions)
		}, timeout, interval).Should(Equal("False/" + harborv1alpha1.ReasonConnectionNotAllowed))
		Expect(harborSrv.ProjectByName("nope")).To(BeNil())
	})
})

var _ = Describe("HarborRobotAccount", func() {
	It("creates a project robot, writes the Secret, recreates it when lost, and deletes", func() {
		ns := newNamespace()
		newConnection(ns, "conn", 2*time.Second)
		proj := &harborv1alpha1.HarborProject{
			ObjectMeta: metav1.ObjectMeta{Name: "payments", Namespace: ns},
			Spec:       harborv1alpha1.HarborProjectSpec{ConnectionRef: harborv1alpha1.ConnectionRef{Name: "conn"}},
		}
		Expect(k8sClient.Create(ctx, proj)).To(Succeed())

		robot := &harborv1alpha1.HarborRobotAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "payments-ci", Namespace: ns},
			Spec: harborv1alpha1.HarborRobotAccountSpec{
				ConnectionRef: harborv1alpha1.ConnectionRef{Name: "conn"},
				Name:          "ci",
				Level:         harborv1alpha1.RobotLevelProject,
				ProjectRef:    &harborv1alpha1.LocalObjectRef{Name: "payments"},
				Duration:      30,
				Permissions: []harborv1alpha1.RobotPermission{{Access: []harborv1alpha1.RobotAccess{
					{Resource: "repository", Action: "pull"}, {Resource: "repository", Action: "push"}}}},
			},
		}
		Expect(k8sClient.Create(ctx, robot)).To(Succeed())
		Eventually(func() string {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(robot), robot)
			return readyReason(robot.Status.Conditions)
		}, timeout, interval).Should(Equal("True/" + harborv1alpha1.ReasonSynced))
		Expect(robot.Status.RobotName).To(Equal("robot$payments+ci"))
		Expect(robot.Status.ExpiresAt).NotTo(BeNil())
		Expect(robot.Status.SecretRef.Name).To(Equal("payments-ci-robot"))

		sec := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "payments-ci-robot", Namespace: ns}, sec)).To(Succeed())
		Expect(string(sec.Data["username"])).To(Equal("robot$payments+ci"))
		Expect(string(sec.Data["password"])).To(Equal(harborSrv.SecretOf(*robot.Status.RobotID)))
		Expect(metav1.IsControlledBy(sec, robot)).To(BeTrue())
		hr := harborSrv.RobotByName("robot$payments+ci")
		Expect(hr).NotTo(BeNil())
		Expect(hr.Permissions).To(HaveLen(1))
		Expect(hr.Permissions[0].Namespace).To(Equal("payments"))
		Expect(hr.Permissions[0].Access).To(HaveLen(2))
		oldPassword := string(sec.Data["password"])

		By("recreating the Secret with a refreshed secret when it is deleted")
		Expect(k8sClient.Delete(ctx, sec)).To(Succeed())
		Eventually(func() string {
			s := &corev1.Secret{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "payments-ci-robot", Namespace: ns}, s); err != nil {
				return ""
			}
			return string(s.Data["password"])
		}, timeout, interval).Should(And(Not(BeEmpty()), Not(Equal(oldPassword))))

		By("correcting permission drift")
		harborSrv.Locked(func() {
			harborSrv.Robots[*robot.Status.RobotID].Permissions[0].Access = []harbor.Access{{Resource: "repository", Action: "pull", Effect: "allow"}}
		})
		Eventually(func() int {
			return len(harborSrv.RobotByName("robot$payments+ci").Permissions[0].Access)
		}, timeout, interval).Should(Equal(2))

		By("deleting the robot from Harbor")
		Expect(k8sClient.Delete(ctx, robot)).To(Succeed())
		Eventually(func() bool {
			return harborSrv.RobotByName("robot$payments+ci") == nil
		}, timeout, interval).Should(BeTrue())
	})

	It("adopts an existing project robot only with adoptExisting", func() {
		ns := newNamespace()
		newConnection(ns, "conn", 2*time.Second)
		proj := &harborv1alpha1.HarborProject{
			ObjectMeta: metav1.ObjectMeta{Name: "billing", Namespace: ns},
			Spec:       harborv1alpha1.HarborProjectSpec{ConnectionRef: harborv1alpha1.ConnectionRef{Name: "conn"}},
		}
		Expect(k8sClient.Create(ctx, proj)).To(Succeed())
		Eventually(func() bool { return harborSrv.ProjectByName("billing") != nil }, timeout, interval).Should(BeTrue())
		existing := harborSrv.AddRobot(harbor.Robot{Name: "robot$billing+deploy", Level: "project", Duration: new(int64(-1)), ExpiresAt: -1,
			Permissions: []harbor.RobotPermission{{Kind: "project", Namespace: "billing",
				Access: []harbor.Access{{Resource: "repository", Action: "pull", Effect: "allow"}}}}}, "old-secret")

		robot := &harborv1alpha1.HarborRobotAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "billing-deploy", Namespace: ns},
			Spec: harborv1alpha1.HarborRobotAccountSpec{
				ConnectionRef: harborv1alpha1.ConnectionRef{Name: "conn"},
				Name:          "deploy",
				ProjectRef:    &harborv1alpha1.LocalObjectRef{Name: "billing"},
				Permissions: []harborv1alpha1.RobotPermission{{Access: []harborv1alpha1.RobotAccess{
					{Resource: "repository", Action: "pull"}}}},
			},
		}
		Expect(k8sClient.Create(ctx, robot)).To(Succeed())
		Eventually(func() string {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(robot), robot)
			return readyReason(robot.Status.Conditions)
		}, timeout, interval).Should(Equal("False/" + harborv1alpha1.ReasonAlreadyExists))
		Expect(harborSrv.SecretOf(existing)).To(Equal("old-secret"), "must not touch the robot")

		robot.Spec.AdoptExisting = true
		Expect(k8sClient.Update(ctx, robot)).To(Succeed())
		Eventually(func() bool {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(robot), robot)
			return isReady(robot.Status.Conditions)
		}, timeout, interval).Should(BeTrue())
		Expect(*robot.Status.RobotID).To(Equal(existing))
		sec := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: robot.Status.SecretRef.Name, Namespace: ns}, sec)).To(Succeed())
		Expect(string(sec.Data["password"])).To(And(Equal(harborSrv.SecretOf(existing)), Not(Equal("old-secret"))))
	})

	It("writes a dockerconfigjson Secret for a system robot", func() {
		ns := newNamespace()
		newConnection(ns, "conn", time.Minute)
		robot := &harborv1alpha1.HarborRobotAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "puller", Namespace: ns},
			Spec: harborv1alpha1.HarborRobotAccountSpec{
				ConnectionRef: harborv1alpha1.ConnectionRef{Name: "conn"},
				Level:         harborv1alpha1.RobotLevelSystem,
				Permissions:   []harborv1alpha1.RobotPermission{{Namespace: "*", Access: []harborv1alpha1.RobotAccess{{Resource: "repository", Action: "pull"}}}},
				Secret:        harborv1alpha1.RobotSecretSpec{Name: "regcred", Type: string(corev1.SecretTypeDockerConfigJson)},
			},
		}
		Expect(k8sClient.Create(ctx, robot)).To(Succeed())
		sec := &corev1.Secret{}
		Eventually(func() error {
			return k8sClient.Get(ctx, types.NamespacedName{Name: "regcred", Namespace: ns}, sec)
		}, timeout, interval).Should(Succeed())
		Expect(sec.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
		var cfg struct {
			Auths map[string]struct {
				Username string `json:"username"`
				Password string `json:"password"`
				Auth     string `json:"auth"`
			} `json:"auths"`
		}
		Expect(json.Unmarshal(sec.Data[corev1.DockerConfigJsonKey], &cfg)).To(Succeed())
		Expect(cfg.Auths).To(HaveLen(1))
		for host, a := range cfg.Auths {
			Expect(host).NotTo(BeEmpty())
			Expect(a.Username).To(Equal("robot$puller"))
			Expect(a.Password).NotTo(BeEmpty())
			Expect(a.Auth).NotTo(BeEmpty())
		}
	})
})

var _ = Describe("HarborRegistry and HarborReplicationPolicy", func() {
	It("creates an endpoint, then a push policy that waits for it and runs once", func() {
		ns := newNamespace()
		newConnection(ns, "conn", 2*time.Second)

		pol := &harborv1alpha1.HarborReplicationPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "to-edge", Namespace: ns},
			Spec: harborv1alpha1.HarborReplicationPolicySpec{
				ConnectionRef:   harborv1alpha1.ConnectionRef{Name: "conn"},
				Direction:       harborv1alpha1.ReplicationPush,
				DestRegistryRef: &harborv1alpha1.LocalObjectRef{Name: "edge"},
				DestNamespace:   "mirror",
				Filters:         []harborv1alpha1.ReplicationFilter{{Type: "name", Value: "payments/**"}, {Type: "tag", Value: "v*", Decoration: "matches"}},
				Trigger:         harborv1alpha1.ReplicationTrigger{Type: "scheduled", Cron: "0 0 2 * * *"},
				ExecuteOnCreate: true,
			},
		}
		Expect(k8sClient.Create(ctx, pol)).To(Succeed())
		Eventually(func() string {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(pol), pol)
			return readyReason(pol.Status.Conditions)
		}, timeout, interval).Should(Equal("False/" + harborv1alpha1.ReasonRegistryNotReady))

		creds := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "edge-creds", Namespace: ns},
			StringData: map[string]string{"accessKey": "robot$sync", "accessSecret": "s3cret"}}
		Expect(k8sClient.Create(ctx, creds)).To(Succeed())
		reg := &harborv1alpha1.HarborRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: ns},
			Spec: harborv1alpha1.HarborRegistrySpec{
				ConnectionRef:        harborv1alpha1.ConnectionRef{Name: "conn"},
				Type:                 "harbor",
				URL:                  "https://edge.example.com",
				CredentialsSecretRef: &harborv1alpha1.LocalObjectRef{Name: "edge-creds"},
				Description:          "edge site",
			},
		}
		Expect(k8sClient.Create(ctx, reg)).To(Succeed())
		Eventually(func() string {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(reg), reg)
			return readyReason(reg.Status.Conditions)
		}, timeout, interval).Should(Equal("True/" + harborv1alpha1.ReasonSynced))
		Expect(reg.Status.Health).To(Equal("healthy"))
		var stored harbor.Registry
		harborSrv.Locked(func() { stored = *harborSrv.Registries[*reg.Status.RegistryID] })
		Expect(stored.Credential.AccessKey).To(Equal("robot$sync"))
		Expect(stored.Credential.AccessSecret).To(Equal("s3cret"))

		By("pushing changed credentials")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(creds), creds)).To(Succeed())
		creds.StringData = map[string]string{"accessKey": "robot$sync", "accessSecret": "rotated"}
		Expect(k8sClient.Update(ctx, creds)).To(Succeed())
		Eventually(func() string {
			var s string
			harborSrv.Locked(func() { s = harborSrv.Registries[*reg.Status.RegistryID].Credential.AccessSecret })
			return s
		}, timeout, interval).Should(Equal("rotated"))

		By("the policy becoming Ready once the registry is")
		Eventually(func() string {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(pol), pol)
			return readyReason(pol.Status.Conditions)
		}, timeout, interval).Should(Equal("True/" + harborv1alpha1.ReasonSynced))
		var hp harbor.ReplicationPolicy
		harborSrv.Locked(func() { hp = *harborSrv.Policies[*pol.Status.PolicyID] })
		Expect(hp.DestRegistry.ID).To(Equal(*reg.Status.RegistryID))
		Expect(hp.SrcRegistry).To(BeNil())
		Expect(hp.DestNamespace).To(Equal("mirror"))
		Expect(hp.Trigger.Type).To(Equal("scheduled"))
		Expect(hp.Trigger.TriggerSettings.Cron).To(Equal("0 0 2 * * *"))
		Expect(hp.Filters).To(HaveLen(2))
		Expect(hp.Enabled).To(BeTrue())
		Expect(hp.Override).To(BeTrue())
		Eventually(func() string {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(pol), pol)
			if pol.Status.LastExecution == nil {
				return ""
			}
			return pol.Status.LastExecution.Status
		}, timeout, interval).Should(Equal("Succeed"))
		Expect(pol.Status.ExecutedOnCreate).To(BeTrue())

		By("correcting drift on the policy")
		harborSrv.Locked(func() { harborSrv.Policies[*pol.Status.PolicyID].Enabled = false })
		Eventually(func() bool {
			var e bool
			harborSrv.Locked(func() { e = harborSrv.Policies[*pol.Status.PolicyID].Enabled })
			return e
		}, timeout, interval).Should(BeTrue())

		By("deleting policy then registry")
		Expect(k8sClient.Delete(ctx, pol)).To(Succeed())
		Eventually(func() int {
			var n int
			harborSrv.Locked(func() { n = len(harborSrv.Policies) })
			return n
		}, timeout, interval).Should(Equal(0))
		Expect(k8sClient.Delete(ctx, reg)).To(Succeed())
		Eventually(func() bool {
			var gone bool
			harborSrv.Locked(func() { _, ok := harborSrv.Registries[*reg.Status.RegistryID]; gone = !ok })
			return gone
		}, timeout, interval).Should(BeTrue())
	})

	It("reports PingFailed for an unreachable registry", func() {
		ns := newNamespace()
		newConnection(ns, "conn", time.Minute)
		reg := &harborv1alpha1.HarborRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "dead", Namespace: ns},
			Spec: harborv1alpha1.HarborRegistrySpec{ConnectionRef: harborv1alpha1.ConnectionRef{Name: "conn"},
				Type: "docker-registry", URL: "https://unreachable.example.com"},
		}
		Expect(k8sClient.Create(ctx, reg)).To(Succeed())
		Eventually(func() string {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(reg), reg)
			return readyReason(reg.Status.Conditions)
		}, timeout, interval).Should(Equal("False/" + harborv1alpha1.ReasonPingFailed))
		Expect(reg.Status.RegistryID).To(BeNil())
	})
})
