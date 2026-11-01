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
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/oreondev/harbor-resource-operator/test/utils"
)

var (
	// managerImage is the manager image to be built and loaded for testing.
	managerImage = "example.com/harbor-resource-operator:v0.0.1"

	// harborNamespace is where the Harbor chart was installed (make setup-harbor-e2e).
	harborNamespace = envOr("HARBOR_NAMESPACE", "harbor")
	// harborInClusterURL is how the operator reaches Harbor from inside the cluster.
	harborInClusterURL = "http://harbor." + harborNamespace + ".svc.cluster.local"
	// harborAdminPassword matches test/e2e/testdata/harbor-values.yaml.
	harborAdminPassword = "Harbor12345"

	// harborPF forwards a local port to the Harbor service so the tests can
	// assert Harbor state through its API.
	harborPF *portForward
)

// TestE2E runs the e2e suite against a real Harbor on a Kind cluster.
// Run it with "make test-e2e", which creates the cluster and installs Harbor.
//
// To enable kubectl kuberc (use custom kubectl configurations), set: KUBECTL_KUBERC=true
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting harbor-resource-operator e2e test suite\n")
	RunSpecs(t, "e2e suite")
}

var _ = BeforeSuite(func() {
	configureKubectlKubeRC()

	By("building the manager image")
	cmd := exec.Command("make", "docker-build", fmt.Sprintf("IMG=%s", managerImage))
	_, err := utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to build the manager image")

	By("loading the manager image on Kind")
	err = utils.LoadImageToKindClusterWithName(managerImage)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to load the manager image into Kind")

	By("checking that Harbor is installed")
	cmd = exec.Command("kubectl", "rollout", "status", "deployment/harbor-core", "-n", harborNamespace, "--timeout=10m")
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Harbor is not running; use make test-e2e or make setup-harbor-e2e")

	By("creating manager namespace")
	cmd = exec.Command("kubectl", "create", "ns", namespace)
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to create namespace")

	By("labeling the namespace to enforce the restricted security policy")
	cmd = exec.Command("kubectl", "label", "--overwrite", "ns", namespace,
		"pod-security.kubernetes.io/enforce=restricted")
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to label namespace with restricted policy")

	By("installing CRDs")
	cmd = exec.Command("make", "install")
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to install CRDs")

	By("deploying the controller-manager")
	cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", managerImage))
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")

	cmd = exec.Command("kubectl", "rollout", "status", "deployment/harbor-resource-operator-controller-manager",
		"-n", namespace, "--timeout=5m")
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "controller-manager did not become ready")

	By("port-forwarding to Harbor")
	harborPF, err = startPortForward(harborNamespace, "svc/harbor", 80)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to port-forward to Harbor")
	EventuallyWithOffset(1, harborPing, 2*time.Minute, 2*time.Second).Should(Succeed())

	setupKubeClient()
})

var _ = AfterSuite(func() {
	if harborPF != nil {
		harborPF.stop()
	}
	if os.Getenv("E2E_SKIP_CLEANUP") == "true" {
		return
	}
	By("undeploying the controller-manager")
	cmd := exec.Command("make", "undeploy", "ignore-not-found=true")
	_, _ = utils.Run(cmd)

	By("uninstalling CRDs")
	cmd = exec.Command("make", "uninstall", "ignore-not-found=true")
	_, _ = utils.Run(cmd)

	By("removing manager namespace")
	cmd = exec.Command("kubectl", "delete", "ns", namespace, "--ignore-not-found")
	_, _ = utils.Run(cmd)
})

// Disable kubectl kuberc by default for test isolation.
// This prevents local kubectl configurations from affecting test behavior.
// To enable kuberc, set: KUBECTL_KUBERC=true
func configureKubectlKubeRC() {
	if os.Getenv("KUBECTL_KUBERC") != "true" {
		By("disabling kubectl kuberc for test isolation")
		err := os.Setenv("KUBECTL_KUBERC", "false")
		ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to disable kubectl kuberc")
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
