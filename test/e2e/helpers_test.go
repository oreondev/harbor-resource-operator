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
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	harborv1alpha1 "github.com/oreondev/harbor-resource-operator/api/v1alpha1"
	"github.com/oreondev/harbor-resource-operator/pkg/harbor"
)

// k8s is a typed client for the e2e cluster.
var k8s client.Client

func setupKubeClient() {
	scheme := runtime.NewScheme()
	ExpectWithOffset(1, clientgoscheme.AddToScheme(scheme)).To(Succeed())
	ExpectWithOffset(1, harborv1alpha1.AddToScheme(scheme)).To(Succeed())
	cfg, err := ctrl.GetConfig()
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	k8s, err = client.New(cfg, client.Options{Scheme: scheme})
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
}

// portForward is a running "kubectl port-forward".
type portForward struct {
	cmd  *exec.Cmd
	port string
}

var forwardingRE = regexp.MustCompile(`Forwarding from 127\.0\.0\.1:(\d+)`)

// startPortForward forwards a free local port to target in ns and returns
// once kubectl reports the port.
func startPortForward(ns, target string, remotePort int) (*portForward, error) {
	cmd := exec.Command("kubectl", "port-forward", "-n", ns, target, fmt.Sprintf(":%d", remotePort))
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = GinkgoWriter
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	portCh := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			if m := forwardingRE.FindStringSubmatch(sc.Text()); m != nil {
				portCh <- m[1]
			}
		}
	}()
	select {
	case p := <-portCh:
		return &portForward{cmd: cmd, port: p}, nil
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		return nil, errors.New("kubectl port-forward did not report a port within 30s")
	}
}

func (p *portForward) stop() {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
		_ = p.cmd.Wait()
	}
}

// harborURL is the local base URL of Harbor through the port-forward.
func harborURL() string { return "http://127.0.0.1:" + harborPF.port }

// harborAdmin is a Harbor API client authenticated as admin.
func harborAdmin() *harbor.Client {
	hc, err := harbor.New(harbor.Options{URL: harborURL(), Username: "admin", Password: harborAdminPassword})
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return hc
}

func harborPing() error {
	_, err := harborAdmin().SystemInfo(context.Background())
	return err
}

// harborRaw calls a Harbor API path not covered by the operator's client.
func harborRaw(method, path string, body any) (int, []byte, error) {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, harborURL()+"/api/v2.0"+path, rdr)
	if err != nil {
		return 0, nil, err
	}
	req.SetBasicAuth("admin", harborAdminPassword)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	return resp.StatusCode, data, err
}

// listRepositories returns the repository names in a Harbor project.
func listRepositories(project string) ([]string, error) {
	code, data, err := harborRaw(http.MethodGet, "/projects/"+project+"/repositories", nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("list repositories of %s: HTTP %d: %s", project, code, data)
	}
	var repos []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &repos); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(repos))
	for _, r := range repos {
		names = append(names, r.Name)
	}
	return names, nil
}

// deleteRepository removes repo (without the project prefix) from project.
func deleteRepository(project, repo string) error {
	code, data, err := harborRaw(http.MethodDelete, "/projects/"+project+"/repositories/"+url.PathEscape(repo), nil)
	if err != nil {
		return err
	}
	if code != http.StatusOK && code != http.StatusNotFound {
		return fmt.Errorf("delete repository %s/%s: HTTP %d: %s", project, repo, code, data)
	}
	return nil
}

// registryToken asks Harbor's token service for a bearer token with scope.
func registryToken(user, pass, scope string) (string, error) {
	q := url.Values{"service": {"harbor-registry"}, "scope": {scope}}
	req, err := http.NewRequest(http.MethodGet, harborURL()+"/service/token?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(user, pass)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token: HTTP %d: %s", resp.StatusCode, data)
	}
	var tok struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", err
	}
	return tok.Token, nil
}

// pushTestImage pushes a tiny single-layer OCI image to repo:tag using the
// given registry credentials, without docker or extra dependencies.
func pushTestImage(user, pass, repo, tag string) error {
	tok, err := registryToken(user, pass, "repository:"+repo+":pull,push")
	if err != nil {
		return err
	}
	// Unique content per push: Harbor refuses to re-push a manifest identical
	// to one deleted earlier from the same Harbor (MANIFEST_UNKNOWN on 2.15),
	// which would break reruns against a long-lived Harbor.
	layer, diffID, err := tinyLayer(fmt.Sprintf("%s:%s %d", repo, tag, time.Now().UnixNano()))
	if err != nil {
		return err
	}
	cfg, _ := json.Marshal(map[string]any{
		"architecture": "amd64", "os": "linux",
		"rootfs": map[string]any{"type": "layers", "diff_ids": []string{diffID}},
	})
	for _, blob := range [][]byte{layer, cfg} {
		if err := pushBlob(tok, repo, blob); err != nil {
			return err
		}
	}
	manifest, _ := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config":        descriptor("application/vnd.oci.image.config.v1+json", cfg),
		"layers":        []any{descriptor("application/vnd.oci.image.layer.v1.tar+gzip", layer)},
	})
	req, _ := http.NewRequest(http.MethodPut, harborURL()+"/v2/"+repo+"/manifests/"+tag, bytes.NewReader(manifest))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	return expectStatus(http.DefaultClient.Do(req))(http.StatusCreated)
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func descriptor(mediaType string, b []byte) map[string]any {
	return map[string]any{"mediaType": mediaType, "digest": digestOf(b), "size": len(b)}
}

// tinyLayer returns a gzipped tar with one file and the digest of the plain tar.
func tinyLayer(nonce string) ([]byte, string, error) {
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	content := []byte("harbor-resource-operator e2e " + nonce + "\n")
	if err := tw.WriteHeader(&tar.Header{Name: "e2e.txt", Mode: 0o644, Size: int64(len(content))}); err != nil {
		return nil, "", err
	}
	if _, err := tw.Write(content); err != nil {
		return nil, "", err
	}
	if err := tw.Close(); err != nil {
		return nil, "", err
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(tarBuf.Bytes()); err != nil {
		return nil, "", err
	}
	if err := zw.Close(); err != nil {
		return nil, "", err
	}
	return gz.Bytes(), digestOf(tarBuf.Bytes()), nil
}

// pushBlob uploads b monolithically: POST to start, PUT with the digest.
func pushBlob(tok, repo string, b []byte) error {
	req, _ := http.NewRequest(http.MethodPost, harborURL()+"/v2/"+repo+"/blobs/uploads/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("start upload to %s: HTTP %d", repo, resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		return err
	}
	// Harbor may answer with its external URL; keep only path and query.
	q := loc.Query()
	q.Set("digest", digestOf(b))
	put, _ := http.NewRequest(http.MethodPut, harborURL()+loc.Path+"?"+q.Encode(), bytes.NewReader(b))
	put.Header.Set("Authorization", "Bearer "+tok)
	put.Header.Set("Content-Type", "application/octet-stream")
	return expectStatus(http.DefaultClient.Do(put))(http.StatusCreated)
}

func expectStatus(resp *http.Response, err error) func(int) error {
	return func(want int) error {
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != want {
			data, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("%s %s: HTTP %d, want %d: %s",
				resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, want, data)
		}
		return nil
	}
}

// readyCondition fetches obj and returns its Ready condition (nil if absent).
func readyCondition(g Gomega, obj client.Object) *readyStatus {
	g.Expect(k8s.Get(context.Background(), client.ObjectKeyFromObject(obj), obj)).To(Succeed())
	raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	g.Expect(err).NotTo(HaveOccurred())
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

// readyStatus is the part of the Ready condition the tests look at.
type readyStatus struct{ Status, Reason, Message string }

// expectReady waits until obj reports Ready=True.
func expectReady(obj client.Object, timeout time.Duration) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		c := readyCondition(g, obj)
		g.Expect(c).NotTo(BeNil(), "no Ready condition yet")
		g.Expect(c.Status).To(Equal("True"), "Ready=%s reason=%s: %s", c.Status, c.Reason, c.Message)
	}, timeout, 2*time.Second).Should(Succeed())
}

// expectNotReady waits until obj reports Ready=False with reason.
func expectNotReady(obj client.Object, reason string, timeout time.Duration) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		c := readyCondition(g, obj)
		g.Expect(c).NotTo(BeNil(), "no Ready condition yet")
		g.Expect(c.Status).To(Equal("False"))
		g.Expect(c.Reason).To(Equal(reason), "message: %s", c.Message)
	}, timeout, 2*time.Second).Should(Succeed())
}

// expectGone waits until obj no longer exists.
func expectGone(obj client.Object, timeout time.Duration) {
	GinkgoHelper()
	Eventually(func() bool {
		err := k8s.Get(context.Background(), client.ObjectKeyFromObject(obj), obj)
		return err != nil && client.IgnoreNotFound(err) == nil
	}, timeout, 2*time.Second).Should(BeTrue(), "%s still exists", obj.GetName())
}

// eventReasons lists the event reasons recorded for an object.
func eventReasons(g Gomega, ns, name string) []string {
	var list eventsv1.EventList
	g.Expect(k8s.List(context.Background(), &list, client.InNamespace(ns))).To(Succeed())
	var out []string
	for _, e := range list.Items {
		if e.Regarding.Name == name {
			out = append(out, e.Reason)
		}
	}
	return out
}

// dumpDebug prints operator logs, events and CRs after a failure.
func dumpDebug(namespaces ...string) {
	for _, args := range [][]string{
		{"logs", "-n", namespace, "deployment/harbor-resource-operator-controller-manager", "--tail=300"},
		{"get", "harborconnections,harborprojects,harborrobotaccounts,harborregistries,harborreplicationpolicies",
			"-A", "-o", "wide"},
	} {
		out, _ := exec.Command("kubectl", args...).CombinedOutput()
		_, _ = fmt.Fprintf(GinkgoWriter, "\n$ kubectl %s\n%s", strings.Join(args, " "), out)
	}
	for _, ns := range namespaces {
		out, _ := exec.Command("kubectl", "get", "events", "-n", ns, "--sort-by=.lastTimestamp").CombinedOutput()
		_, _ = fmt.Fprintf(GinkgoWriter, "\n$ kubectl get events -n %s\n%s", ns, out)
	}
}

// createNamespace creates ns, waiting while a namespace of that name left by a
// previous run is still terminating. An existing active namespace is reused.
func createNamespace(name string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
		err := k8s.Create(context.Background(), ns)
		if err == nil {
			return
		}
		g.Expect(apierrors.IsAlreadyExists(err)).To(BeTrue(), "create namespace %s: %v", name, err)
		g.Expect(k8s.Get(context.Background(), client.ObjectKeyFromObject(ns), ns)).To(Succeed())
		g.Expect(ns.DeletionTimestamp).To(BeNil(), "namespace %s from a previous run is still terminating", name)
	}, 5*time.Minute, 2*time.Second).Should(Succeed())
}
