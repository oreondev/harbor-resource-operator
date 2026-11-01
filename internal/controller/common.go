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
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	harborv1alpha1 "github.com/oreondev/harbor-resource-operator/api/v1alpha1"
	"github.com/oreondev/harbor-resource-operator/internal/harbor"
)

const (
	defaultSyncPeriod    = 10 * time.Minute
	dependencyRetryAfter = 30 * time.Second
	errorRetryAfter      = time.Minute

	eventUpdated        = "Updated"
	eventDriftCorrected = "DriftCorrected"
)

// changeReason picks the event reason for a write to Harbor: a spec change
// is Updated, a correction of out-of-band change is DriftCorrected. Drift
// corrections are counted in harbor_drift_corrections_total.
func changeReason(kind string, generation, observedGeneration int64, conds []metav1.Condition) string {
	if generation == observedGeneration && isReady(conds) {
		driftCorrections.WithLabelValues(kind).Inc()
		return eventDriftCorrected
	}
	return eventUpdated
}

// reconcileError carries a condition reason and whether the reconcile should
// be retried with backoff (transient) or wait for a spec change (terminal).
type reconcileError struct {
	reason  string
	message string
	err     error
	retry   bool
}

func (e *reconcileError) Error() string {
	if e.err != nil {
		return fmt.Sprintf("%s: %s: %v", e.reason, e.message, e.err)
	}
	return fmt.Sprintf("%s: %s", e.reason, e.message)
}

func (e *reconcileError) Unwrap() error { return e.err }

// terminal returns a reconcileError that waits for a spec change.
func terminal(reason, format string, args ...any) *reconcileError {
	return &reconcileError{reason: reason, message: fmt.Sprintf(format, args...)}
}

// transient returns a reconcileError that is retried with backoff.
func transient(reason string, err error, format string, args ...any) *reconcileError {
	return &reconcileError{reason: reason, message: fmt.Sprintf(format, args...), err: err, retry: true}
}

// classifyHarborError turns a Harbor client error into a reconcileError.
func classifyHarborError(err error, what string) *reconcileError {
	switch {
	case harbor.IsUnauthorized(err):
		return &reconcileError{reason: harborv1alpha1.ReasonAuthenticationFailed, message: what + ": Harbor rejected the connection credentials", err: err}
	case harbor.IsClientError(err):
		return &reconcileError{reason: harborv1alpha1.ReasonHarborError, message: what + ": " + err.Error(), err: err}
	default:
		return &reconcileError{reason: harborv1alpha1.ReasonHarborError, message: what + ": " + err.Error(), err: err, retry: true}
	}
}

// patchStatus writes the status with a plain merge patch. It deliberately
// takes no optimistic lock: a conflict would drop an ID recorded right after
// creating the object in Harbor. Stale decisions are avoided instead by
// reading the object with getFresh at the start of each reconcile.
func patchStatus(ctx context.Context, c client.Client, obj, base client.Object, result ctrl.Result) (ctrl.Result, error) {
	if err := c.Status().Patch(ctx, obj, client.MergeFrom(base)); err != nil {
		return ctrl.Result{}, err
	}
	return result, nil
}

// getFresh reads obj from the API server when reader is set, bypassing the
// cache. The cache can lag behind the status written by the previous
// reconcile; deciding from it would lose freshly recorded Harbor IDs.
func getFresh(ctx context.Context, c client.Client, reader client.Reader, key types.NamespacedName, obj client.Object) error {
	if reader != nil {
		return reader.Get(ctx, key, obj)
	}
	return c.Get(ctx, key, obj)
}

// setReady writes the Ready condition.
func setReady(conds *[]metav1.Condition, gen int64, status metav1.ConditionStatus, reason, message string) {
	if len(message) > 32*1024 {
		message = message[:32*1024]
	}
	meta.SetStatusCondition(conds, metav1.Condition{
		Type:               harborv1alpha1.ConditionReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: gen,
	})
}

// isReady reports whether a Ready=True condition is present.
func isReady(conds []metav1.Condition) bool {
	return meta.IsStatusConditionTrue(conds, harborv1alpha1.ConditionReady)
}

// limiters are shared per connection so that every controller together stays
// under the connection's request budget.
var limiters sync.Map // map[string]*rate.Limiter

func limiterFor(key string, rps int32) *rate.Limiter {
	if rps <= 0 {
		rps = 10
	}
	v, _ := limiters.LoadOrStore(key, rate.NewLimiter(rate.Limit(rps), int(rps)*2))
	l := v.(*rate.Limiter)
	if l.Limit() != rate.Limit(rps) {
		l.SetLimit(rate.Limit(rps))
		l.SetBurst(int(rps) * 2)
	}
	return l
}

// transports are shared per connection so HTTP keep-alive connections are
// reused across reconciles. Credentials are not part of a transport; they are
// still read from the Secret on every reconcile.
var transports sync.Map // map[string]*cachedTransport

type cachedTransport struct {
	fingerprint string
	transport   *http.Transport
}

// transportFor returns the shared transport for a connection, replacing it
// when the CA bundle or the TLS verification setting changes.
func transportFor(key string, ca []byte, insecure bool) (*http.Transport, error) {
	sum := sha256.Sum256(ca)
	fp := fmt.Sprintf("%x/%t", sum, insecure)
	if v, ok := transports.Load(key); ok {
		if ct := v.(*cachedTransport); ct.fingerprint == fp {
			return ct.transport, nil
		}
	}
	t, err := harbor.NewTransport(ca, insecure)
	if err != nil {
		return nil, err
	}
	if old, loaded := transports.Swap(key, &cachedTransport{fingerprint: fp, transport: t}); loaded {
		old.(*cachedTransport).transport.CloseIdleConnections()
	}
	return t, nil
}

// forgetConnection drops the shared transport and rate limiter of a deleted connection.
func forgetConnection(key string) {
	if old, ok := transports.LoadAndDelete(key); ok {
		old.(*cachedTransport).transport.CloseIdleConnections()
	}
	limiters.Delete(key)
}

// connectionAllows reports whether resources in ns may use the connection.
func connectionAllows(conn *harborv1alpha1.HarborConnection, ns string) bool {
	if ns == conn.Namespace {
		return true
	}
	return slices.Contains(conn.Spec.AllowedNamespaces, "*") || slices.Contains(conn.Spec.AllowedNamespaces, ns)
}

// resolveConnection loads the HarborConnection a resource points at and
// checks the namespace policy.
func resolveConnection(ctx context.Context, c client.Client, ref harborv1alpha1.ConnectionRef, ns string) (*harborv1alpha1.HarborConnection, *reconcileError) {
	connNS := ref.Namespace
	if connNS == "" {
		connNS = ns
	}
	conn := &harborv1alpha1.HarborConnection{}
	if err := c.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: connNS}, conn); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, &reconcileError{reason: harborv1alpha1.ReasonConnectionNotFound,
				message: fmt.Sprintf("HarborConnection %s/%s not found", connNS, ref.Name), retry: true}
		}
		return nil, transient(harborv1alpha1.ReasonConnectionNotFound, err, "get HarborConnection %s/%s", connNS, ref.Name)
	}
	if !connectionAllows(conn, ns) {
		return nil, terminal(harborv1alpha1.ReasonConnectionNotAllowed,
			"HarborConnection %s/%s does not allow namespace %s (see spec.allowedNamespaces)", connNS, ref.Name, ns)
	}
	return conn, nil
}

// clientForConnection builds a Harbor client from the connection's secrets.
func clientForConnection(ctx context.Context, c client.Client, conn *harborv1alpha1.HarborConnection) (*harbor.Client, *reconcileError) {
	creds := &corev1.Secret{}
	key := types.NamespacedName{Name: conn.Spec.CredentialsSecretRef.Name, Namespace: conn.Namespace}
	if err := c.Get(ctx, key, creds); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, &reconcileError{reason: harborv1alpha1.ReasonSecretMissing,
				message: fmt.Sprintf("credentials Secret %s not found", key), retry: true}
		}
		return nil, transient(harborv1alpha1.ReasonSecretMissing, err, "get credentials Secret %s", key)
	}
	user, pass := string(creds.Data["username"]), string(creds.Data["password"])
	if user == "" || pass == "" {
		return nil, terminal(harborv1alpha1.ReasonSecretMissing, "credentials Secret %s needs keys username and password", key)
	}
	var ca []byte
	if conn.Spec.CABundleSecretRef != nil {
		caSecret := &corev1.Secret{}
		caKey := types.NamespacedName{Name: conn.Spec.CABundleSecretRef.Name, Namespace: conn.Namespace}
		if err := c.Get(ctx, caKey, caSecret); err != nil {
			return nil, transient(harborv1alpha1.ReasonSecretMissing, err, "get CA bundle Secret %s", caKey)
		}
		ca = caSecret.Data["ca.crt"]
		if len(ca) == 0 {
			return nil, terminal(harborv1alpha1.ReasonSecretMissing, "CA bundle Secret %s needs key ca.crt", caKey)
		}
	}
	transport, err := transportFor(conn.Namespace+"/"+conn.Name, ca, conn.Spec.InsecureSkipTLSVerify)
	if err != nil {
		return nil, terminal(harborv1alpha1.ReasonInvalidSpec, "%v", err)
	}
	hc, err := harbor.New(harbor.Options{
		URL:       conn.Spec.URL,
		Username:  user,
		Password:  pass,
		Transport: transport,
		Limiter:   limiterFor(conn.Namespace+"/"+conn.Name, conn.Spec.RequestsPerSecond),
		Observe:   observeAPI(conn.Namespace + "/" + conn.Name),
	})
	if err != nil {
		return nil, terminal(harborv1alpha1.ReasonInvalidSpec, "%v", err)
	}
	return hc, nil
}

// connectAndClient resolves the connection, requires it to be Ready, and
// returns a client.
func connectAndClient(ctx context.Context, c client.Client, ref harborv1alpha1.ConnectionRef, ns string) (*harborv1alpha1.HarborConnection, *harbor.Client, *reconcileError) {
	conn, rerr := resolveConnection(ctx, c, ref, ns)
	if rerr != nil {
		return nil, nil, rerr
	}
	if !isReady(conn.Status.Conditions) {
		return conn, nil, &reconcileError{reason: harborv1alpha1.ReasonConnectionUnavailable,
			message: fmt.Sprintf("HarborConnection %s/%s is not Ready", conn.Namespace, conn.Name), retry: true}
	}
	hc, rerr := clientForConnection(ctx, c, conn)
	if rerr != nil {
		return conn, nil, rerr
	}
	return conn, hc, nil
}

// syncPeriodFor picks the resync interval: resource override, then connection, then default.
func syncPeriodFor(conn *harborv1alpha1.HarborConnection, override *metav1.Duration) time.Duration {
	if override != nil && override.Duration > 0 {
		return override.Duration
	}
	if conn != nil && conn.Spec.SyncPeriod != nil && conn.Spec.SyncPeriod.Duration > 0 {
		return conn.Spec.SyncPeriod.Duration
	}
	return defaultSyncPeriod
}

// defaultName returns spec name or the object name.
func defaultName(specName, objName string) string {
	if specName != "" {
		return specName
	}
	return objName
}

// deref returns *p or the zero value.
func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
