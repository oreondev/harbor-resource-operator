//go:build e2e

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
package e2e

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	harborv1alpha1 "github.com/oreondev/harbor-resource-operator/api/v1alpha1"
	"github.com/oreondev/harbor-resource-operator/pkg/harbor"
)

const (
	e2eNS       = "e2e-harbor"
	deniedNS    = "e2e-denied"
	connName    = "harbor"
	srcProject  = "e2e-src"
	replProject = "e2e-replica"
	adoptName   = "e2e-adopt"
)

// These specs drive every M1 kind against a real Harbor and assert the
// result through Harbor's own API, not through the operator's status.
var _ = Describe("Harbor resources", Ordered, func() {
	ctx := context.Background()
	var hc *harbor.Client
	conn := connRef()
	short := &metav1.Duration{Duration: 15 * time.Second}

	BeforeAll(func() {
		hc = harborAdmin()
		for _, ns := range []string{e2eNS, deniedNS} {
			createNamespace(ns)
		}
		Expect(k8s.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "harbor-admin", Namespace: e2eNS},
			StringData: map[string]string{"username": "admin", "password": harborAdminPassword},
		})).To(Succeed())
		Expect(k8s.Create(ctx, &harborv1alpha1.HarborConnection{
			ObjectMeta: metav1.ObjectMeta{Name: connName, Namespace: e2eNS},
			Spec: harborv1alpha1.HarborConnectionSpec{
				URL:                  harborInClusterURL,
				CredentialsSecretRef: harborv1alpha1.LocalObjectRef{Name: "harbor-admin"},
				SyncPeriod:           short,
			},
		})).To(Succeed())
	})

	AfterEach(func() {
		if CurrentSpecReport().Failed() {
			dumpDebug(e2eNS, deniedNS)
		}
	})

	AfterAll(func() {
		if os.Getenv("E2E_SKIP_CLEANUP") == "true" {
			return
		}
		// Remove what the specs may have left behind, in Harbor and in Kubernetes.
		for _, p := range []string{srcProject, replProject} {
			if repos, err := listRepositories(p); err == nil {
				for _, r := range repos {
					_ = deleteRepository(p, strings.TrimPrefix(r, p+"/"))
				}
			}
		}
		for _, ns := range []string{e2eNS, deniedNS} {
			_ = k8s.DeleteAllOf(ctx, &harborv1alpha1.HarborReplicationPolicy{}, client.InNamespace(ns))
			_ = k8s.DeleteAllOf(ctx, &harborv1alpha1.HarborRobotAccount{}, client.InNamespace(ns))
			_ = k8s.DeleteAllOf(ctx, &harborv1alpha1.HarborRegistry{}, client.InNamespace(ns))
			_ = k8s.DeleteAllOf(ctx, &harborv1alpha1.HarborProject{}, client.InNamespace(ns))
		}
		Eventually(func() int {
			var list harborv1alpha1.HarborProjectList
			_ = k8s.List(ctx, &list)
			return len(list.Items)
		}, 2*time.Minute, 2*time.Second).Should(BeZero())
		_ = k8s.DeleteAllOf(ctx, &harborv1alpha1.HarborConnection{}, client.InNamespace(e2eNS))
		for _, p := range []string{srcProject, replProject, adoptName} {
			if got, err := hc.GetProjectByName(ctx, p); err == nil {
				_ = hc.DeleteProject(ctx, got.ProjectID)
			}
		}
		for _, ns := range []string{e2eNS, deniedNS} {
			_ = k8s.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
		}
	})

	It("connects to Harbor and reports its version", func() {
		c := &harborv1alpha1.HarborConnection{ObjectMeta: metav1.ObjectMeta{Name: connName, Namespace: e2eNS}}
		expectReady(c, 2*time.Minute)
		Expect(c.Status.Username).To(Equal("admin"))
		Expect(c.Status.HarborVersion).NotTo(BeEmpty())
		if want := os.Getenv("HARBOR_VERSION"); want != "" {
			Expect(c.Status.HarborVersion).To(HavePrefix("v" + want))
		}
		GinkgoWriter.Printf("Harbor version under test: %s\n", c.Status.HarborVersion)
	})

	It("creates a project with metadata and quota", func() {
		p := &harborv1alpha1.HarborProject{
			ObjectMeta: metav1.ObjectMeta{Name: srcProject, Namespace: e2eNS},
			Spec: harborv1alpha1.HarborProjectSpec{
				ConnectionRef:     conn,
				Public:            new(false),
				StorageQuotaBytes: new(int64(1 << 30)),
				Metadata: &harborv1alpha1.ProjectMetadata{
					AutoScan:          new(true),
					PreventVulnerable: new(true),
					Severity:          new("high"),
				},
				DeletionPolicy: harborv1alpha1.DeletionPolicyDelete,
				SyncPeriod:     short,
			},
		}
		Expect(k8s.Create(ctx, p)).To(Succeed())
		expectReady(p, 2*time.Minute)

		got, err := hc.GetProjectByName(ctx, srcProject)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ProjectID).To(Equal(*p.Status.ProjectID))
		Expect(got.Metadata.AutoScan).To(Equal("true"))
		Expect(got.Metadata.PreventVul).To(Equal("true"))
		Expect(got.Metadata.Severity).To(Equal("high"))
		Expect(got.Metadata.Public).To(Equal("false"))
		q, err := hc.GetProjectQuota(ctx, got.ProjectID)
		Expect(err).NotTo(HaveOccurred())
		Expect(q.Hard["storage"]).To(Equal(int64(1 << 30)))
	})

	It("corrects drift made in Harbor", func() {
		got, err := hc.GetProjectByName(ctx, srcProject)
		Expect(err).NotTo(HaveOccurred())
		Expect(hc.UpdateProject(ctx, got.ProjectID, harbor.ProjectReq{
			Metadata: &harbor.ProjectMetadata{AutoScan: "false", Severity: "low"},
		})).To(Succeed())

		Eventually(func(g Gomega) {
			p, err := hc.GetProjectByName(ctx, srcProject)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(p.Metadata.AutoScan).To(Equal("true"))
			g.Expect(p.Metadata.Severity).To(Equal("high"))
		}, 2*time.Minute, 2*time.Second).Should(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(eventReasons(g, e2eNS, srcProject)).To(ContainElement("DriftCorrected"))
		}, time.Minute, 2*time.Second).Should(Succeed())
	})

	It("creates a robot whose pull secret can push to the project", func() {
		rb := &harborv1alpha1.HarborRobotAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "pusher", Namespace: e2eNS},
			Spec: harborv1alpha1.HarborRobotAccountSpec{
				ConnectionRef: conn,
				ProjectRef:    &harborv1alpha1.LocalObjectRef{Name: srcProject},
				Description:   "e2e push robot",
				Permissions: []harborv1alpha1.RobotPermission{{Access: []harborv1alpha1.RobotAccess{
					{Resource: "repository", Action: "push"},
					{Resource: "repository", Action: "pull"},
					{Resource: "artifact", Action: "read"},
				}}},
				Secret: harborv1alpha1.RobotSecretSpec{Name: "pusher-regcred", Type: string(corev1.SecretTypeDockerConfigJson)},
			},
		}
		Expect(k8s.Create(ctx, rb)).To(Succeed())
		expectReady(rb, 2*time.Minute)
		Expect(rb.Status.RobotName).To(Equal("robot$" + srcProject + "+pusher"))

		user, pass := dockerCreds("pusher-regcred")
		Expect(user).To(Equal(rb.Status.RobotName))
		Expect(pushTestImage(user, pass, srcProject+"/app", "v1")).To(Succeed())
		Expect(listRepositories(srcProject)).To(ContainElement(srcProject + "/app"))
	})

	It("recreates a robot deleted in Harbor and rewrites the secret", func() {
		rb := &harborv1alpha1.HarborRobotAccount{ObjectMeta: metav1.ObjectMeta{Name: "pusher", Namespace: e2eNS}}
		Expect(k8s.Get(ctx, client.ObjectKeyFromObject(rb), rb)).To(Succeed())
		oldID := *rb.Status.RobotID
		_, oldPass := dockerCreds("pusher-regcred")
		Expect(hc.DeleteRobot(ctx, oldID)).To(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(k8s.Get(ctx, client.ObjectKeyFromObject(rb), rb)).To(Succeed())
			g.Expect(rb.Status.RobotID).NotTo(BeNil())
			g.Expect(*rb.Status.RobotID).NotTo(Equal(oldID))
		}, 2*time.Minute, 2*time.Second).Should(Succeed())
		expectReady(rb, time.Minute)

		user, pass := dockerCreds("pusher-regcred")
		Expect(pass).NotTo(Equal(oldPass))
		Expect(pushTestImage(user, pass, srcProject+"/app", "v2")).To(Succeed())
	})

	It("registers a replication endpoint and replicates an image through it", func() {
		Expect(k8s.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "self-creds", Namespace: e2eNS},
			StringData: map[string]string{"accessKey": "admin", "accessSecret": harborAdminPassword},
		})).To(Succeed())
		reg := &harborv1alpha1.HarborRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "self", Namespace: e2eNS},
			Spec: harborv1alpha1.HarborRegistrySpec{
				ConnectionRef:        conn,
				Type:                 "harbor",
				URL:                  harborInClusterURL,
				Insecure:             true,
				CredentialsSecretRef: &harborv1alpha1.LocalObjectRef{Name: "self-creds"},
				Description:          "the Harbor under test, as its own replication target",
			},
		}
		Expect(k8s.Create(ctx, reg)).To(Succeed())
		expectReady(reg, 2*time.Minute)
		Expect(reg.Status.Health).To(Equal("healthy"))

		dest := &harborv1alpha1.HarborProject{
			ObjectMeta: metav1.ObjectMeta{Name: replProject, Namespace: e2eNS},
			Spec: harborv1alpha1.HarborProjectSpec{
				ConnectionRef:  conn,
				DeletionPolicy: harborv1alpha1.DeletionPolicyDelete,
				SyncPeriod:     short,
			},
		}
		Expect(k8s.Create(ctx, dest)).To(Succeed())
		expectReady(dest, 2*time.Minute)

		pol := &harborv1alpha1.HarborReplicationPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "src-to-replica", Namespace: e2eNS},
			Spec: harborv1alpha1.HarborReplicationPolicySpec{
				ConnectionRef:   conn,
				Direction:       "push",
				DestRegistryRef: &harborv1alpha1.LocalObjectRef{Name: "self"},
				DestNamespace:   replProject,
				Filters:         []harborv1alpha1.ReplicationFilter{{Type: "name", Value: srcProject + "/**"}},
				Trigger:         harborv1alpha1.ReplicationTrigger{Type: "manual"},
				ExecuteOnCreate: true,
			},
		}
		Expect(k8s.Create(ctx, pol)).To(Succeed())
		expectReady(pol, 2*time.Minute)

		got, err := hc.ListReplicationPolicies(ctx, "src-to-replica")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(1))
		Expect(got[0].DestNamespace).To(Equal(replProject))

		Eventually(func(g Gomega) {
			g.Expect(k8s.Get(ctx, client.ObjectKeyFromObject(pol), pol)).To(Succeed())
			g.Expect(pol.Status.LastExecution).NotTo(BeNil())
			g.Expect(pol.Status.LastExecution.Status).To(Equal("Succeed"))
		}, 5*time.Minute, 5*time.Second).Should(Succeed())
		Expect(listRepositories(replProject)).To(ContainElement(HaveSuffix("/app")))
	})

	It("deletes the policy and the endpoint from Harbor with their objects", func() {
		pol := &harborv1alpha1.HarborReplicationPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "src-to-replica", Namespace: e2eNS},
		}
		Expect(k8s.Delete(ctx, pol)).To(Succeed())
		expectGone(pol, time.Minute)
		Expect(hc.ListReplicationPolicies(ctx, "src-to-replica")).To(BeEmpty())

		reg := &harborv1alpha1.HarborRegistry{ObjectMeta: metav1.ObjectMeta{Name: "self", Namespace: e2eNS}}
		Expect(k8s.Delete(ctx, reg)).To(Succeed())
		expectGone(reg, time.Minute)
		Expect(hc.ListRegistries(ctx, "self")).To(BeEmpty())
	})

	It("refuses to delete a project that holds repositories, then deletes it once empty", func() {
		dest := &harborv1alpha1.HarborProject{ObjectMeta: metav1.ObjectMeta{Name: replProject, Namespace: e2eNS}}
		Expect(k8s.Delete(ctx, dest)).To(Succeed())
		expectNotReady(dest, harborv1alpha1.ReasonProjectNotEmpty, time.Minute)
		_, err := hc.GetProjectByName(ctx, replProject)
		Expect(err).NotTo(HaveOccurred(), "project must stay in Harbor while it holds repositories")

		repos, err := listRepositories(replProject)
		Expect(err).NotTo(HaveOccurred())
		for _, r := range repos {
			Expect(deleteRepository(replProject, strings.TrimPrefix(r, replProject+"/"))).To(Succeed())
		}
		expectGone(dest, 2*time.Minute)
		_, err = hc.GetProjectByName(ctx, replProject)
		Expect(harbor.IsNotFound(err)).To(BeTrue(), "project should be deleted from Harbor, got %v", err)
	})

	It("adopts an existing project only when asked, and retains it on delete", func() {
		_, err := hc.CreateProject(ctx, harbor.ProjectReq{ProjectName: adoptName})
		Expect(err).NotTo(HaveOccurred())
		existing, err := hc.GetProjectByName(ctx, adoptName)
		Expect(err).NotTo(HaveOccurred())

		p := &harborv1alpha1.HarborProject{
			ObjectMeta: metav1.ObjectMeta{Name: adoptName, Namespace: e2eNS},
			Spec:       harborv1alpha1.HarborProjectSpec{ConnectionRef: conn, Public: new(true)},
		}
		Expect(k8s.Create(ctx, p)).To(Succeed())
		expectNotReady(p, harborv1alpha1.ReasonAlreadyExists, time.Minute)
		got, err := hc.GetProjectByName(ctx, adoptName)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Metadata.Public).To(Equal("false"), "a conflicting project must not be touched")

		base := p.DeepCopy()
		p.Spec.AdoptExisting = true
		Expect(k8s.Patch(ctx, p, client.MergeFrom(base))).To(Succeed())
		expectReady(p, time.Minute)
		Expect(*p.Status.ProjectID).To(Equal(existing.ProjectID))
		Eventually(func(g Gomega) {
			got, err := hc.GetProjectByName(ctx, adoptName)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(got.Metadata.Public).To(Equal("true"))
		}, time.Minute, 2*time.Second).Should(Succeed())

		Expect(k8s.Delete(ctx, p)).To(Succeed())
		expectGone(p, time.Minute)
		_, err = hc.GetProjectByName(ctx, adoptName)
		Expect(err).NotTo(HaveOccurred(), "deletionPolicy Retain (the default) must leave the project in Harbor")
	})

	It("rejects references from namespaces the connection does not allow", func() {
		p := &harborv1alpha1.HarborProject{
			ObjectMeta: metav1.ObjectMeta{Name: "denied", Namespace: deniedNS},
			Spec:       harborv1alpha1.HarborProjectSpec{ConnectionRef: conn, Name: "e2e-denied"},
		}
		Expect(k8s.Create(ctx, p)).To(Succeed())
		expectNotReady(p, harborv1alpha1.ReasonConnectionNotAllowed, time.Minute)
		_, err := hc.GetProjectByName(ctx, "e2e-denied")
		Expect(harbor.IsNotFound(err)).To(BeTrue(), "nothing may be created in Harbor, got %v", err)
		Expect(k8s.Delete(ctx, p)).To(Succeed())
		expectGone(p, time.Minute)
	})

	It("deletes the robot and the project from Harbor with their objects", func() {
		rb := &harborv1alpha1.HarborRobotAccount{ObjectMeta: metav1.ObjectMeta{Name: "pusher", Namespace: e2eNS}}
		Expect(k8s.Get(ctx, client.ObjectKeyFromObject(rb), rb)).To(Succeed())
		id := *rb.Status.RobotID
		Expect(k8s.Delete(ctx, rb)).To(Succeed())
		expectGone(rb, time.Minute)
		_, err := hc.GetRobot(ctx, id)
		Expect(harbor.IsNotFound(err)).To(BeTrue(), "robot should be deleted from Harbor, got %v", err)
		expectGone(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "pusher-regcred", Namespace: e2eNS}}, time.Minute)

		Expect(deleteRepository(srcProject, "app")).To(Succeed())
		p := &harborv1alpha1.HarborProject{ObjectMeta: metav1.ObjectMeta{Name: srcProject, Namespace: e2eNS}}
		Expect(k8s.Delete(ctx, p)).To(Succeed())
		expectGone(p, 2*time.Minute)
		_, err = hc.GetProjectByName(ctx, srcProject)
		Expect(harbor.IsNotFound(err)).To(BeTrue(), "project should be deleted from Harbor, got %v", err)
	})
})

func connRef() harborv1alpha1.ConnectionRef {
	return harborv1alpha1.ConnectionRef{Name: connName, Namespace: e2eNS}
}

// dockerCreds reads username and password from a dockerconfigjson Secret
// written by the operator for the in-cluster Harbor host.
func dockerCreds(secretName string) (string, string) {
	GinkgoHelper()
	s := &corev1.Secret{}
	Expect(k8s.Get(context.Background(), client.ObjectKey{Namespace: e2eNS, Name: secretName}, s)).To(Succeed())
	Expect(s.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
	var cfg struct {
		Auths map[string]struct {
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"auths"`
	}
	Expect(json.Unmarshal(s.Data[corev1.DockerConfigJsonKey], &cfg)).To(Succeed())
	host := strings.TrimPrefix(harborInClusterURL, "http://")
	Expect(cfg.Auths).To(HaveKey(host))
	return cfg.Auths[host].Username, cfg.Auths[host].Password
}
