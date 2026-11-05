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
	"context"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	harborv1alpha1 "github.com/oreondev/harbor-resource-operator/api/v1alpha1"
)

// Metrics served on the controller-runtime metrics endpoint next to the
// built-in reconcile metrics. Connection labels are "<namespace>/<name>".
const labelConnection = "connection"

var (
	apiRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "harbor_api_requests_total",
		Help: "Requests sent to the Harbor API, by connection, HTTP method and status code (0 when no response arrived).",
	}, []string{labelConnection, "method", "code"})

	apiDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "harbor_api_request_duration_seconds",
		Help:    "Duration of Harbor API requests, excluding time spent waiting for the rate limiter.",
		Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
	}, []string{labelConnection, "method"})

	driftCorrections = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "harbor_drift_corrections_total",
		Help: "Writes to Harbor that restored a value changed outside the operator, by kind.",
	}, []string{"kind"})

	resourcesDesc = prometheus.NewDesc("harbor_resources",
		"Managed resources by kind, connection and Ready status.",
		[]string{"kind", labelConnection, "ready"}, nil)

	robotExpiryDesc = prometheus.NewDesc("harbor_robot_expiration_timestamp_seconds",
		"Unix time at which a robot account expires. Absent for robots that never expire.",
		[]string{"namespace", "name", "robot"}, nil)
)

func init() {
	ctrlmetrics.Registry.MustRegister(apiRequests, apiDuration, driftCorrections)
}

// observeAPI returns the Harbor client hook that records request metrics
// for one connection.
func observeAPI(connection string) func(method string, code int, d time.Duration) {
	return func(method string, code int, d time.Duration) {
		apiRequests.WithLabelValues(connection, method, strconv.Itoa(code)).Inc()
		apiDuration.WithLabelValues(connection, method).Observe(d.Seconds())
	}
}

// RegisterResourceMetrics registers a collector that derives harbor_resources
// and the robot expiry gauge from the objects in reader (normally the
// manager's cache) at scrape time, so deleted objects never leave stale series.
func RegisterResourceMetrics(reader client.Reader) error {
	return ctrlmetrics.Registry.Register(&resourceCollector{reader: reader})
}

type resourceCollector struct {
	reader client.Reader
}

func (c *resourceCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- resourcesDesc
	ch <- robotExpiryDesc
}

// managedObject is implemented by every kind the operator manages.
type managedObject interface {
	client.Object
	GetConditions() []metav1.Condition
	GetConnectionRef() harborv1alpha1.ConnectionRef
}

// managedKinds lists the kinds counted by harbor_resources.
var managedKinds = []struct {
	kind    string
	newList func() client.ObjectList
}{
	{"HarborConnection", func() client.ObjectList { return &harborv1alpha1.HarborConnectionList{} }},
	{"HarborProject", func() client.ObjectList { return &harborv1alpha1.HarborProjectList{} }},
	{"HarborRobotAccount", func() client.ObjectList { return &harborv1alpha1.HarborRobotAccountList{} }},
	{"HarborRegistry", func() client.ObjectList { return &harborv1alpha1.HarborRegistryList{} }},
	{"HarborReplicationPolicy", func() client.ObjectList { return &harborv1alpha1.HarborReplicationPolicyList{} }},
	{"HarborProjectMember", func() client.ObjectList { return &harborv1alpha1.HarborProjectMemberList{} }},
	{"HarborLabel", func() client.ObjectList { return &harborv1alpha1.HarborLabelList{} }},
	{"HarborImmutableRule", func() client.ObjectList { return &harborv1alpha1.HarborImmutableRuleList{} }},
	{"HarborWebhook", func() client.ObjectList { return &harborv1alpha1.HarborWebhookList{} }},
	{"HarborRetentionPolicy", func() client.ObjectList { return &harborv1alpha1.HarborRetentionPolicyList{} }},
	{"HarborGarbageCollection", func() client.ObjectList { return &harborv1alpha1.HarborGarbageCollectionList{} }},
}

func (c *resourceCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	type key struct{ kind, connection, ready string }
	for _, mk := range managedKinds {
		list := mk.newList()
		if err := c.reader.List(ctx, list); err != nil {
			ch <- prometheus.NewInvalidMetric(resourcesDesc, err)
			continue
		}
		objs, err := meta.ExtractList(list)
		if err != nil {
			ch <- prometheus.NewInvalidMetric(resourcesDesc, err)
			continue
		}
		counts := map[key]int{}
		for _, o := range objs {
			m, ok := o.(managedObject)
			if !ok {
				continue
			}
			counts[key{mk.kind, m.GetConnectionRef().Key(), strconv.FormatBool(isReady(m.GetConditions()))}]++
		}
		for k, n := range counts {
			ch <- prometheus.MustNewConstMetric(resourcesDesc, prometheus.GaugeValue, float64(n), k.kind, k.connection, k.ready)
		}
	}

	var robots harborv1alpha1.HarborRobotAccountList
	if err := c.reader.List(ctx, &robots); err != nil {
		ch <- prometheus.NewInvalidMetric(robotExpiryDesc, err)
		return
	}
	for i := range robots.Items {
		rb := &robots.Items[i]
		if rb.Status.ExpiresAt == nil {
			continue
		}
		ch <- prometheus.MustNewConstMetric(robotExpiryDesc, prometheus.GaugeValue,
			float64(rb.Status.ExpiresAt.Unix()), rb.Namespace, rb.Name, rb.Status.RobotName)
	}
}
