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
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	harborv1alpha1 "github.com/oreondev/harbor-resource-operator/api/v1alpha1"
	"github.com/oreondev/harbor-resource-operator/pkg/harbor"
)

const (
	depthNS     = "e2e-depth"
	tlsService  = "harbor-tls"
	scaleCount  = 100
	scaleRPS    = 10
	scaleBurst  = 20 // the limiter's burst, see REQUIREMENTS X7
	scalePrefix = "e2e-scale-"
)

// These specs go beyond one object per kind: HTTPS with a CA bundle (through
// a TLS proxy in front of Harbor, since the e2e Harbor serves plain HTTP), a
// few hundred objects against one rate-limited connection, and the content
// of the operator's own metrics.
var _ = Describe("Harbor depth", Ordered, func() {
	ctx := context.Background()
	var hc *harbor.Client
	var caPEM, otherCAPEM []byte
	tlsURL := "https://" + tlsService + "." + depthNS + ".svc"

	BeforeAll(func() {
		hc = harborAdmin()
		createNamespace(depthNS)
		Expect(k8s.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "harbor-admin", Namespace: depthNS},
			StringData: map[string]string{"username": "admin", "password": harborAdminPassword},
		})).To(Succeed())
	})

	AfterEach(func() {
		if CurrentSpecReport().Failed() {
			dumpDebug(depthNS)
		}
	})

	AfterAll(func() {
		if os.Getenv("E2E_SKIP_CLEANUP") == "true" {
			return
		}
		_ = k8s.DeleteAllOf(ctx, &harborv1alpha1.HarborLabel{}, client.InNamespace(depthNS))
		_ = k8s.DeleteAllOf(ctx, &harborv1alpha1.HarborRobotAccount{}, client.InNamespace(depthNS))
		_ = k8s.DeleteAllOf(ctx, &harborv1alpha1.HarborProject{}, client.InNamespace(depthNS))
		Eventually(func() int {
			var list harborv1alpha1.HarborProjectList
			_ = k8s.List(ctx, &list, client.InNamespace(depthNS))
			return len(list.Items)
		}, 10*time.Minute, 5*time.Second).Should(BeZero())
		_ = k8s.DeleteAllOf(ctx, &harborv1alpha1.HarborConnection{}, client.InNamespace(depthNS))
		for i := range scaleCount {
			if p, err := hc.GetProjectByName(ctx, scaleName(i)); err == nil {
				_ = hc.DeleteProject(ctx, p.ProjectID)
			}
		}
		_ = k8s.Delete(ctx, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: depthNS + "-metrics"}})
		_ = k8s.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: depthNS}})
	})

	It("serves Harbor over HTTPS through a TLS proxy", func() {
		var certPEM, keyPEM []byte
		caPEM, certPEM, keyPEM = issueServerCert(tlsService+"."+depthNS+".svc", tlsService+"."+depthNS+".svc.cluster.local")
		otherCAPEM, _, _ = issueServerCert("unrelated.invalid")
		deployTLSProxy(certPEM, keyPEM)
	})

	It("rejects the proxy's certificate without a CA bundle", func() {
		c := depthConnection("tls-no-ca", tlsURL, nil, false)
		expectNotReadyWithMessage(c, harborv1alpha1.ReasonHarborError, "certificate", 2*time.Minute)
	})

	It("connects with insecureSkipTLSVerify", func() {
		c := depthConnection("tls-insecure", tlsURL, nil, true)
		expectReady(c, 2*time.Minute)
	})

	It("connects once the CA bundle Secret is corrected, and manages a project over HTTPS", func() {
		Expect(k8s.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "harbor-ca", Namespace: depthNS},
			Data:       map[string][]byte{"ca.crt": otherCAPEM},
		})).To(Succeed())
		c := depthConnection("tls-ca", tlsURL, &harborv1alpha1.LocalObjectRef{Name: "harbor-ca"}, false)
		expectNotReadyWithMessage(c, harborv1alpha1.ReasonHarborError, "certificate", 2*time.Minute)

		// The connection has a 10 minute sync period, so recovering within a
		// minute proves the Secret watch and the per-CA transport cache.
		s := &corev1.Secret{}
		Expect(k8s.Get(ctx, client.ObjectKey{Namespace: depthNS, Name: "harbor-ca"}, s)).To(Succeed())
		s.Data = map[string][]byte{"ca.crt": caPEM}
		Expect(k8s.Update(ctx, s)).To(Succeed())
		expectReady(c, time.Minute)
		Expect(c.Status.HarborVersion).NotTo(BeEmpty())

		p := &harborv1alpha1.HarborProject{
			ObjectMeta: metav1.ObjectMeta{Name: "e2e-tls", Namespace: depthNS},
			Spec: harborv1alpha1.HarborProjectSpec{
				ConnectionRef:  harborv1alpha1.ConnectionRef{Name: "tls-ca", Namespace: depthNS},
				DeletionPolicy: harborv1alpha1.DeletionPolicyDelete,
			},
		}
		Expect(k8s.Create(ctx, p)).To(Succeed())
		expectReady(p, 2*time.Minute)
		_, err := hc.GetProjectByName(ctx, "e2e-tls")
		Expect(err).NotTo(HaveOccurred())
		Expect(k8s.Delete(ctx, p)).To(Succeed())
		expectGone(p, time.Minute)
		_, err = hc.GetProjectByName(ctx, "e2e-tls")
		Expect(harbor.IsNotFound(err)).To(BeTrue(), "project should be deleted, got %v", err)
	})

	It("reconciles a few hundred objects on one connection within its rate limit", func() {
		m := newMetricsReader()
		connKey := depthNS + "/scale"
		before := m.sum("harbor_api_requests_total", map[string]string{"connection": connKey})
		start := time.Now()

		c := depthConnection("scale", harborInClusterURL, nil, false)
		base := c.DeepCopy()
		c.Spec.RequestsPerSecond = scaleRPS
		Expect(k8s.Patch(ctx, c, client.MergeFrom(base))).To(Succeed())
		expectReady(c, 2*time.Minute)

		conn := harborv1alpha1.ConnectionRef{Name: "scale", Namespace: depthNS}
		long := &metav1.Duration{Duration: time.Hour} // keep periodic resyncs out of the measurement
		createAll(scaleCount, func(i int) client.Object {
			return &harborv1alpha1.HarborProject{
				ObjectMeta: metav1.ObjectMeta{Name: scaleName(i), Namespace: depthNS},
				Spec: harborv1alpha1.HarborProjectSpec{
					ConnectionRef: conn, DeletionPolicy: harborv1alpha1.DeletionPolicyDelete,
					Metadata:   &harborv1alpha1.ProjectMetadata{AutoScan: new(i%2 == 0)},
					SyncPeriod: long,
				},
			}
		})
		createAll(scaleCount, func(i int) client.Object {
			return &harborv1alpha1.HarborRobotAccount{
				ObjectMeta: metav1.ObjectMeta{Name: scaleName(i), Namespace: depthNS},
				Spec: harborv1alpha1.HarborRobotAccountSpec{
					ConnectionRef: conn, ProjectRef: &harborv1alpha1.LocalObjectRef{Name: scaleName(i)},
					Duration: 30,
					Permissions: []harborv1alpha1.RobotPermission{{Access: []harborv1alpha1.RobotAccess{
						{Resource: "repository", Action: "pull"},
					}}},
					Secret:     harborv1alpha1.RobotSecretSpec{Name: scaleName(i) + "-pull"},
					SyncPeriod: long,
				},
			}
		})
		createAll(scaleCount, func(i int) client.Object {
			return &harborv1alpha1.HarborLabel{
				ObjectMeta: metav1.ObjectMeta{Name: scaleName(i), Namespace: depthNS},
				Spec: harborv1alpha1.HarborLabelSpec{
					ConnectionRef: conn, Name: "scale", Scope: harborv1alpha1.LabelScope("project"),
					ProjectReference: harborv1alpha1.ProjectReference{ProjectRef: &harborv1alpha1.LocalObjectRef{Name: scaleName(i)}},
					Color:            "#1565C0", SyncPeriod: long,
				},
			}
		})

		Eventually(func(g Gomega) {
			g.Expect(countReady(g, &harborv1alpha1.HarborProjectList{})).To(Equal(scaleCount))
			g.Expect(countReady(g, &harborv1alpha1.HarborRobotAccountList{})).To(Equal(scaleCount))
			g.Expect(countReady(g, &harborv1alpha1.HarborLabelList{})).To(Equal(scaleCount))
		}, 15*time.Minute, 10*time.Second).Should(Succeed())
		elapsed := time.Since(start)
		m = newMetricsReader() // a reader holds one scrape
		after := m.sum("harbor_api_requests_total", map[string]string{"connection": connKey})
		requests := after - before
		GinkgoWriter.Printf("%d objects Ready in %s with %.0f Harbor API calls (%.1f/s, limit %d/s)\n",
			3*scaleCount, elapsed.Round(time.Second), requests, requests/elapsed.Seconds(), scaleRPS)
		Expect(requests).To(BeNumerically(">", 3*scaleCount), "every object needs at least one call")
		Expect(requests).To(BeNumerically("<=", scaleRPS*elapsed.Seconds()+scaleBurst+1),
			"the operator exceeded the connection's requestsPerSecond")

		for i := range scaleCount {
			p, err := hc.GetProjectByName(ctx, scaleName(i))
			Expect(err).NotTo(HaveOccurred())
			Expect(p.Metadata.AutoScan).To(Equal(strconv.FormatBool(i%2 == 0)))
		}
		Expect(m.sum("harbor_api_requests_total", map[string]string{"connection": connKey, "code": "500"})).To(BeZero())
		Expect(managerRestarts()).To(BeZero(), "the operator restarted under load")
	})

	It("exports resource, request and robot expiry metrics", func() {
		m := newMetricsReader()
		Expect(m.sum("harbor_resources", map[string]string{
			"kind": "HarborProject", "connection": depthNS + "/scale", "ready": "true",
		})).To(BeNumerically("==", scaleCount))
		Expect(m.sum("harbor_resources", map[string]string{
			"kind": "HarborRobotAccount", "connection": depthNS + "/scale", "ready": "true",
		})).To(BeNumerically("==", scaleCount))
		inNS := map[string]string{"namespace": depthNS}
		Expect(m.count("harbor_robot_expiration_timestamp_seconds", inNS)).To(Equal(scaleCount))
		in29Days := float64(time.Now().Add(29 * 24 * time.Hour).Unix())
		Expect(m.min("harbor_robot_expiration_timestamp_seconds", inNS)).
			To(BeNumerically(">", in29Days), "robots were created with a 30 day duration")
		Expect(m.count("harbor_api_request_duration_seconds_count", map[string]string{"connection": depthNS + "/scale"})).
			To(BeNumerically(">", 0))
	})

	It("deletes a few hundred objects and their Harbor counterparts", func() {
		start := time.Now()
		Expect(k8s.DeleteAllOf(ctx, &harborv1alpha1.HarborLabel{}, client.InNamespace(depthNS))).To(Succeed())
		Expect(k8s.DeleteAllOf(ctx, &harborv1alpha1.HarborRobotAccount{}, client.InNamespace(depthNS))).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(countAll(g, &harborv1alpha1.HarborLabelList{})).To(BeZero())
			g.Expect(countAll(g, &harborv1alpha1.HarborRobotAccountList{})).To(BeZero())
		}, 10*time.Minute, 5*time.Second).Should(Succeed())
		Expect(k8s.DeleteAllOf(ctx, &harborv1alpha1.HarborProject{}, client.InNamespace(depthNS))).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(countAll(g, &harborv1alpha1.HarborProjectList{})).To(BeZero())
		}, 10*time.Minute, 5*time.Second).Should(Succeed())
		GinkgoWriter.Printf("%d objects deleted in %s\n", 3*scaleCount, time.Since(start).Round(time.Second))
		for i := range scaleCount {
			_, err := hc.GetProjectByName(ctx, scaleName(i))
			Expect(harbor.IsNotFound(err)).To(BeTrue(), "project %s should be deleted, got %v", scaleName(i), err)
		}
	})
})

func scaleName(i int) string { return fmt.Sprintf("%s%03d", scalePrefix, i) }

// depthConnection creates a HarborConnection in the depth namespace with a
// sync period long enough that only watches can explain quick recoveries.
func depthConnection(name, url string, ca *harborv1alpha1.LocalObjectRef,
	insecure bool) *harborv1alpha1.HarborConnection {
	GinkgoHelper()
	c := &harborv1alpha1.HarborConnection{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: depthNS},
		Spec: harborv1alpha1.HarborConnectionSpec{
			URL:                   url,
			CredentialsSecretRef:  harborv1alpha1.LocalObjectRef{Name: "harbor-admin"},
			CABundleSecretRef:     ca,
			InsecureSkipTLSVerify: insecure,
			SyncPeriod:            &metav1.Duration{Duration: 10 * time.Minute},
		},
	}
	Expect(k8s.Create(context.Background(), c)).To(Succeed())
	return c
}

// expectNotReadyWithMessage waits for Ready=False with reason and a message
// containing substr.
func expectNotReadyWithMessage(obj client.Object, reason, substr string, timeout time.Duration) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		c := readyCondition(g, obj)
		g.Expect(c).NotTo(BeNil(), "no Ready condition yet")
		g.Expect(c.Status).To(Equal("False"))
		g.Expect(c.Reason).To(Equal(reason), "message: %s", c.Message)
		g.Expect(c.Message).To(ContainSubstring(substr))
	}, timeout, 2*time.Second).Should(Succeed())
}

// createAll creates n objects concurrently, as a GitOps apply would.
func createAll(n int, build func(i int) client.Object) {
	GinkgoHelper()
	var wg sync.WaitGroup
	errs := make(chan error, n)
	sem := make(chan struct{}, 20)
	for i := range n {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			errs <- k8s.Create(context.Background(), build(i))
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		Expect(err).NotTo(HaveOccurred())
	}
}

// countReady lists objects in the depth namespace and counts Ready=True ones.
func countReady(g Gomega, list client.ObjectList) int {
	g.Expect(k8s.List(context.Background(), list, client.InNamespace(depthNS))).To(Succeed())
	n := 0
	for _, o := range listObjects(g, list) {
		if c := conditionsOf(o); c != nil && c.Status == "True" {
			n++
		}
	}
	return n
}

// countAll counts objects of a kind in the depth namespace.
func countAll(g Gomega, list client.ObjectList) int {
	g.Expect(k8s.List(context.Background(), list, client.InNamespace(depthNS))).To(Succeed())
	return len(listObjects(g, list))
}

// managerRestarts sums container restarts of the operator pods.
func managerRestarts() int32 {
	GinkgoHelper()
	var pods corev1.PodList
	Expect(k8s.List(context.Background(), &pods, client.InNamespace(namespace),
		client.MatchingLabels{"control-plane": "controller-manager"})).To(Succeed())
	var n int32
	for _, p := range pods.Items {
		for _, cs := range p.Status.ContainerStatuses {
			n += cs.RestartCount
		}
	}
	return n
}

// issueServerCert returns a fresh CA and a server certificate it signed for hosts.
func issueServerCert(hosts ...string) (caPEM, certPEM, keyPEM []byte) {
	GinkgoHelper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).NotTo(HaveOccurred())
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "e2e CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	Expect(err).NotTo(HaveOccurred())
	caCert, err := x509.ParseCertificate(caDER)
	Expect(err).NotTo(HaveOccurred())

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).NotTo(HaveOccurred())
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: hosts[0]}, DNSNames: hosts,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	Expect(err).NotTo(HaveOccurred())
	keyDER, err := x509.MarshalECPrivateKey(key)
	Expect(err).NotTo(HaveOccurred())
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

// deployTLSProxy runs an unprivileged nginx that terminates TLS and forwards
// to the in-cluster Harbor, and waits until it serves.
func deployTLSProxy(certPEM, keyPEM []byte) {
	GinkgoHelper()
	ctx := context.Background()
	harborHost := strings.TrimPrefix(harborInClusterURL, "http://")
	conf := fmt.Sprintf(`server {
  listen 8443 ssl;
  ssl_certificate     /tls/tls.crt;
  ssl_certificate_key /tls/tls.key;
  client_max_body_size 0;
  location / {
    proxy_pass http://%[1]s;
    proxy_set_header Host %[1]s;
    proxy_set_header X-Forwarded-Proto https;
  }
}
`, harborHost)
	Expect(k8s.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: tlsService + "-tls", Namespace: depthNS},
		Type:       corev1.SecretTypeTLS,
		Data:       map[string][]byte{corev1.TLSCertKey: certPEM, corev1.TLSPrivateKeyKey: keyPEM},
	})).To(Succeed())
	Expect(k8s.Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: tlsService + "-conf", Namespace: depthNS},
		Data:       map[string]string{"default.conf": conf},
	})).To(Succeed())
	labels := map[string]string{"app": tlsService}
	Expect(k8s.Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: tlsService, Namespace: depthNS, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "nginx",
						Image: "nginxinc/nginx-unprivileged:1.27-alpine",
						Ports: []corev1.ContainerPort{{ContainerPort: 8443}},
						ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
							TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(8443)},
						}},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "tls", MountPath: "/tls", ReadOnly: true},
							{Name: "conf", MountPath: "/etc/nginx/conf.d", ReadOnly: true},
						},
					}},
					Volumes: []corev1.Volume{
						{Name: "tls", VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: tlsService + "-tls"},
						}},
						{Name: "conf", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: tlsService + "-conf"},
						}}},
					},
				},
			},
		},
	})).To(Succeed())
	Expect(k8s.Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: tlsService, Namespace: depthNS},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports:    []corev1.ServicePort{{Name: "https", Port: 443, TargetPort: intstr.FromInt32(8443)}},
		},
	})).To(Succeed())
	Eventually(func(g Gomega) {
		d := &appsv1.Deployment{}
		g.Expect(k8s.Get(ctx, client.ObjectKey{Namespace: depthNS, Name: tlsService}, d)).To(Succeed())
		g.Expect(d.Status.AvailableReplicas).To(BeNumerically(">=", 1))
	}, 3*time.Minute, 2*time.Second).Should(Succeed())
}

// metricsReader scrapes the operator's metrics endpoint through a
// port-forward, authenticating as a service account bound to the
// metrics-reader ClusterRole.
type metricsReader struct{ samples []metricSample }

type metricSample struct {
	name   string
	labels map[string]string
	value  float64
}

var metricsToken string

func newMetricsReader() *metricsReader {
	GinkgoHelper()
	ctx := context.Background()
	if metricsToken == "" {
		crb := &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: depthNS + "-metrics"},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "harbor-resource-operator-metrics-reader",
			},
			Subjects: []rbacv1.Subject{{Kind: rbacv1.ServiceAccountKind, Name: "default", Namespace: depthNS}},
		}
		Expect(client.IgnoreAlreadyExists(k8s.Create(ctx, crb))).To(Succeed())
		sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: depthNS}}
		tr := &authenticationv1.TokenRequest{Spec: authenticationv1.TokenRequestSpec{ExpirationSeconds: new(int64(3600))}}
		Eventually(func() error {
			return k8s.SubResource("token").Create(ctx, sa, tr)
		}, time.Minute, time.Second).Should(Succeed())
		metricsToken = tr.Status.Token
	}
	pf, err := startPortForward(namespace, "deployment/harbor-resource-operator-controller-manager", 8443)
	Expect(err).NotTo(HaveOccurred())
	defer pf.stop()

	httpc := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // the operator's self-signed metrics cert
	}}
	var body []byte
	Eventually(func(g Gomega) {
		req, err := http.NewRequest(http.MethodGet, "https://127.0.0.1:"+pf.port+"/metrics", nil)
		g.Expect(err).NotTo(HaveOccurred())
		req.Header.Set("Authorization", "Bearer "+metricsToken)
		resp, err := httpc.Do(req)
		g.Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
		body, err = io.ReadAll(resp.Body)
		g.Expect(err).NotTo(HaveOccurred())
	}, time.Minute, 2*time.Second).Should(Succeed())
	return &metricsReader{samples: parseMetrics(string(body))}
}

// parseMetrics reads the Prometheus text format, enough for these assertions.
func parseMetrics(text string) []metricSample {
	var out []metricSample
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		s := metricSample{labels: map[string]string{}}
		var rest string
		if i := strings.IndexByte(line, '{'); i >= 0 {
			j := strings.LastIndexByte(line, '}')
			s.name = line[:i]
			for _, kv := range splitLabels(line[i+1 : j]) {
				if k, v, ok := strings.Cut(kv, "="); ok {
					s.labels[k] = strings.Trim(v, `"`)
				}
			}
			rest = strings.TrimSpace(line[j+1:])
		} else {
			s.name, rest, _ = strings.Cut(line, " ")
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		v, err := strconv.ParseFloat(f[0], 64)
		if err != nil {
			continue
		}
		s.value = v
		out = append(out, s)
	}
	return out
}

// splitLabels splits `a="x",b="y,z"` on commas outside quotes.
func splitLabels(s string) []string {
	var out []string
	var cur strings.Builder
	quoted := false
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
			cur.WriteRune(r)
		case r == ',' && !quoted:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func (m *metricsReader) matching(name string, want map[string]string) []metricSample {
	var out []metricSample
	for _, s := range m.samples {
		if s.name != name {
			continue
		}
		ok := true
		for k, v := range want {
			if s.labels[k] != v {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, s)
		}
	}
	return out
}

func (m *metricsReader) sum(name string, want map[string]string) float64 {
	var t float64
	for _, s := range m.matching(name, want) {
		t += s.value
	}
	return t
}

func (m *metricsReader) count(name string, want map[string]string) int {
	return len(m.matching(name, want))
}

func (m *metricsReader) min(name string, want map[string]string) float64 {
	ms := m.matching(name, want)
	if len(ms) == 0 {
		return 0
	}
	v := ms[0].value
	for _, s := range ms[1:] {
		v = min(v, s.value)
	}
	return v
}

// listObjects returns the items of a typed list.
func listObjects(g Gomega, list client.ObjectList) []client.Object {
	items, err := meta.ExtractList(list)
	g.Expect(err).NotTo(HaveOccurred())
	out := make([]client.Object, 0, len(items))
	for _, it := range items {
		if o, ok := it.(client.Object); ok {
			out = append(out, o)
		}
	}
	return out
}

// conditionsOf returns obj's Ready condition without fetching it again.
func conditionsOf(obj client.Object) *readyStatus {
	raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil
	}
	status, _ := raw["status"].(map[string]any)
	conds, _ := status["conditions"].([]any)
	for _, c := range conds {
		m, _ := c.(map[string]any)
		if m["type"] == harborv1alpha1.ConditionReady {
			return &readyStatus{
				Status: fmt.Sprint(m["status"]), Reason: fmt.Sprint(m["reason"]), Message: fmt.Sprint(m["message"]),
			}
		}
	}
	return nil
}
