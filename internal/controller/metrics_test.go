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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	harborv1alpha1 "github.com/oreondev/harbor-resource-operator/api/v1alpha1"
)

// gaugeValue finds the sample of family name whose labels include want.
func gaugeValue(families []*dto.MetricFamily, name string, want map[string]string) (float64, bool) {
	for _, mf := range families {
		if mf.GetName() != name {
			continue
		}
	next:
		for _, m := range mf.GetMetric() {
			labels := map[string]string{}
			for _, lp := range m.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}
			for k, v := range want {
				if labels[k] != v {
					continue next
				}
			}
			return m.GetGauge().GetValue(), true
		}
	}
	return 0, false
}

var _ = Describe("Metrics", func() {
	It("counts API requests and drift corrections, and exports resource and robot expiry gauges", func() {
		ns := newNamespace()
		newConnection(ns, "conn", 2*time.Second)
		connLabel := ns + "/conn"
		Expect(testutil.ToFloat64(apiRequests.WithLabelValues(connLabel, "GET", "200"))).To(BeNumerically(">", 0))

		proj := &harborv1alpha1.HarborProject{
			ObjectMeta: metav1.ObjectMeta{Name: "metrics", Namespace: ns},
			Spec: harborv1alpha1.HarborProjectSpec{
				ConnectionRef: harborv1alpha1.ConnectionRef{Name: "conn"},
				Metadata:      &harborv1alpha1.ProjectMetadata{AutoScan: new(true)},
			},
		}
		Expect(k8sClient.Create(ctx, proj)).To(Succeed())
		robot := &harborv1alpha1.HarborRobotAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "metrics-ci", Namespace: ns},
			Spec: harborv1alpha1.HarborRobotAccountSpec{
				ConnectionRef: harborv1alpha1.ConnectionRef{Name: "conn"},
				ProjectRef:    &harborv1alpha1.LocalObjectRef{Name: "metrics"},
				Duration:      30,
				Permissions: []harborv1alpha1.RobotPermission{{Access: []harborv1alpha1.RobotAccess{
					{Resource: "repository", Action: "pull"}}}},
			},
		}
		Expect(k8sClient.Create(ctx, robot)).To(Succeed())
		Eventually(func() string {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(robot), robot)
			return readyReason(robot.Status.Conditions)
		}, timeout, interval).Should(Equal("True/" + harborv1alpha1.ReasonSynced))

		By("counting a drift correction")
		before := testutil.ToFloat64(driftCorrections.WithLabelValues("HarborProject"))
		hp := harborSrv.ProjectByName("metrics")
		harborSrv.Locked(func() { harborSrv.Projects[hp.ProjectID].Metadata.AutoScan = "false" })
		Eventually(func() float64 {
			return testutil.ToFloat64(driftCorrections.WithLabelValues("HarborProject"))
		}, timeout, interval).Should(BeNumerically(">", before))

		By("collecting gauges from the objects")
		reg := prometheus.NewPedanticRegistry()
		Expect(reg.Register(&resourceCollector{reader: k8sClient})).To(Succeed())
		Eventually(func(g Gomega) {
			families, err := reg.Gather()
			g.Expect(err).NotTo(HaveOccurred())
			v, ok := gaugeValue(families, "harbor_resources", map[string]string{
				"kind": "HarborProject", "connection": connLabel, "ready": "true"})
			g.Expect(ok).To(BeTrue())
			g.Expect(v).To(Equal(1.0))
			v, ok = gaugeValue(families, "harbor_resources", map[string]string{
				"kind": "HarborConnection", "connection": connLabel, "ready": "true"})
			g.Expect(ok).To(BeTrue())
			g.Expect(v).To(Equal(1.0))
			v, ok = gaugeValue(families, "harbor_robot_expiration_timestamp_seconds", map[string]string{
				"namespace": ns, "name": "metrics-ci", "robot": "robot$metrics+metrics-ci"})
			g.Expect(ok).To(BeTrue())
			g.Expect(v).To(BeNumerically("~", float64(robot.Status.ExpiresAt.Unix()), 1))
		}, timeout, interval).Should(Succeed())
	})
})
