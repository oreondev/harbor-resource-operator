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
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	harborv1alpha1 "github.com/oreondev/harbor-resource-operator/api/v1alpha1"
	"github.com/oreondev/harbor-resource-operator/pkg/harbor"
)

// HarborReplicationPolicyReconciler reconciles a HarborReplicationPolicy object
type HarborReplicationPolicyReconciler struct {
	client.Client
	// APIReader reads the reconciled object uncached; see getFresh.
	APIReader client.Reader
	Scheme    *runtime.Scheme
	Recorder  events.EventRecorder
}

// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborreplicationpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborreplicationpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborreplicationpolicies/finalizers,verbs=update

// Reconcile drives a Harbor replication policy to the declared state.
func (r *HarborReplicationPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	obj := &harborv1alpha1.HarborReplicationPolicy{}
	if err := getFresh(ctx, r.Client, r.APIReader, req.NamespacedName, obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	base := obj.DeepCopy()
	if !obj.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, obj, base)
	}
	if !controllerutil.ContainsFinalizer(obj, harborv1alpha1.Finalizer) {
		controllerutil.AddFinalizer(obj, harborv1alpha1.Finalizer)
		if err := r.Patch(ctx, obj, client.MergeFrom(base)); err != nil {
			return ctrl.Result{}, err
		}
		base = obj.DeepCopy()
	}

	conn, result, rerr := r.reconcileNormal(ctx, obj)
	obj.Status.ObservedGeneration = obj.Generation
	if rerr != nil {
		setReady(&obj.Status.Conditions, obj.Generation, metav1.ConditionFalse, rerr.reason, rerr.message)
		log.Info("HarborReplicationPolicy not ready", "reason", rerr.reason, "message", rerr.message)
		if rerr.retry {
			result = ctrl.Result{RequeueAfter: errorRetryAfter}
		} else if result.RequeueAfter == 0 {
			result = ctrl.Result{RequeueAfter: syncPeriodFor(conn, obj.Spec.SyncPeriod)}
		}
	}
	return patchStatus(ctx, r.Client, obj, base, result)
}

// registryID resolves a HarborRegistry reference to its Harbor ID.
func (r *HarborReplicationPolicyReconciler) registryID(ctx context.Context, ns string, ref *harborv1alpha1.LocalObjectRef, connRef harborv1alpha1.ConnectionRef) (int64, *reconcileError) {
	reg := &harborv1alpha1.HarborRegistry{}
	key := types.NamespacedName{Name: ref.Name, Namespace: ns}
	if err := r.Get(ctx, key, reg); err != nil {
		if apierrors.IsNotFound(err) {
			return 0, &reconcileError{reason: harborv1alpha1.ReasonRegistryNotReady, message: fmt.Sprintf("HarborRegistry %s not found", key), retry: true}
		}
		return 0, transient(harborv1alpha1.ReasonRegistryNotReady, err, "get HarborRegistry %s", key)
	}
	if !isReady(reg.Status.Conditions) || reg.Status.RegistryID == nil {
		return 0, &reconcileError{reason: harborv1alpha1.ReasonRegistryNotReady, message: fmt.Sprintf("HarborRegistry %s is not Ready", key), retry: true}
	}
	if reg.Spec.ConnectionRef.Name != connRef.Name || defaultName(reg.Spec.ConnectionRef.Namespace, ns) != defaultName(connRef.Namespace, ns) {
		return 0, terminal(harborv1alpha1.ReasonInvalidSpec, "HarborRegistry %s uses a different HarborConnection", key)
	}
	return *reg.Status.RegistryID, nil
}

func desiredFilters(spec []harborv1alpha1.ReplicationFilter) []harbor.ReplicationFilter {
	out := make([]harbor.ReplicationFilter, 0, len(spec))
	for _, f := range spec {
		var v any = f.Value
		if f.Type == "label" {
			v = []string{f.Value}
		}
		out = append(out, harbor.ReplicationFilter{Type: f.Type, Value: v, Decoration: f.Decoration})
	}
	return out
}

// filtersKey renders filters canonically for comparison.
func filtersKey(fs []harbor.ReplicationFilter) string {
	parts := make([]string, 0, len(fs))
	for _, f := range fs {
		var v string
		switch x := f.Value.(type) {
		case string:
			v = x
		case []string:
			v = strings.Join(x, "|")
		case []any:
			ss := make([]string, 0, len(x))
			for _, e := range x {
				ss = append(ss, fmt.Sprint(e))
			}
			v = strings.Join(ss, "|")
		default:
			v = fmt.Sprint(x)
		}
		parts = append(parts, f.Type+"="+v+"/"+f.Decoration)
	}
	slices.Sort(parts)
	return strings.Join(parts, ";")
}

// desiredPolicy resolves registry references and builds the Harbor policy the spec describes.
func (r *HarborReplicationPolicyReconciler) desiredPolicy(ctx context.Context, obj *harborv1alpha1.HarborReplicationPolicy, name string) (harbor.ReplicationPolicy, ctrl.Result, *reconcileError) {
	// Registry references.
	var src, dst *harbor.Registry
	switch obj.Spec.Direction {
	case harborv1alpha1.ReplicationPush:
		if obj.Spec.DestRegistryRef == nil {
			return harbor.ReplicationPolicy{}, ctrl.Result{}, terminal(harborv1alpha1.ReasonInvalidSpec, "direction push needs spec.destRegistryRef")
		}
		id, rerr := r.registryID(ctx, obj.Namespace, obj.Spec.DestRegistryRef, obj.Spec.ConnectionRef)
		if rerr != nil {
			return harbor.ReplicationPolicy{}, ctrl.Result{RequeueAfter: dependencyRetryAfter}, rerr
		}
		dst = &harbor.Registry{ID: id}
	case harborv1alpha1.ReplicationPull:
		if obj.Spec.SrcRegistryRef == nil {
			return harbor.ReplicationPolicy{}, ctrl.Result{}, terminal(harborv1alpha1.ReasonInvalidSpec, "direction pull needs spec.srcRegistryRef")
		}
		id, rerr := r.registryID(ctx, obj.Namespace, obj.Spec.SrcRegistryRef, obj.Spec.ConnectionRef)
		if rerr != nil {
			return harbor.ReplicationPolicy{}, ctrl.Result{RequeueAfter: dependencyRetryAfter}, rerr
		}
		src = &harbor.Registry{ID: id}
	default:
		return harbor.ReplicationPolicy{}, ctrl.Result{}, terminal(harborv1alpha1.ReasonInvalidSpec, "spec.direction must be push or pull")
	}

	triggerType := obj.Spec.Trigger.Type
	if triggerType == "" {
		triggerType = "manual"
	}
	if triggerType == "scheduled" && obj.Spec.Trigger.Cron == "" {
		return harbor.ReplicationPolicy{}, ctrl.Result{}, terminal(harborv1alpha1.ReasonInvalidSpec, "trigger type scheduled needs spec.trigger.cron")
	}
	desired := harbor.ReplicationPolicy{
		Name:                      name,
		Description:               obj.Spec.Description,
		SrcRegistry:               src,
		DestRegistry:              dst,
		DestNamespace:             obj.Spec.DestNamespace,
		DestNamespaceReplaceCount: obj.Spec.DestNamespaceReplaceCount,
		Trigger:                   &harbor.ReplicationTrigger{Type: triggerType},
		Filters:                   desiredFilters(obj.Spec.Filters),
		ReplicateDeletion:         obj.Spec.ReplicateDeletion,
		Override:                  deref(obj.Spec.Override) || obj.Spec.Override == nil,
		Enabled:                   deref(obj.Spec.Enabled) || obj.Spec.Enabled == nil,
		Speed:                     new(obj.Spec.SpeedKBps),
		CopyByChunk:               new(obj.Spec.CopyByChunk),
		SingleActiveReplication:   new(obj.Spec.SingleActiveReplication),
	}
	if desired.DestNamespaceReplaceCount == nil {
		desired.DestNamespaceReplaceCount = new(int32(-1))
	}
	if triggerType == "scheduled" {
		desired.Trigger.TriggerSettings = &harbor.ReplicationTriggerSettings{Cron: obj.Spec.Trigger.Cron}
	}

	return desired, ctrl.Result{}, nil
}

func (r *HarborReplicationPolicyReconciler) reconcileNormal(ctx context.Context, obj *harborv1alpha1.HarborReplicationPolicy) (*harborv1alpha1.HarborConnection, ctrl.Result, *reconcileError) {
	conn, hc, rerr := connectAndClient(ctx, r.Client, obj.Spec.ConnectionRef, obj.Namespace)
	if rerr != nil {
		return conn, ctrl.Result{}, rerr
	}
	period := syncPeriodFor(conn, obj.Spec.SyncPeriod)
	name := defaultName(obj.Spec.Name, obj.Name)

	desired, res, rerr := r.desiredPolicy(ctx, obj, name)
	if rerr != nil {
		return conn, res, rerr
	}

	// Locate.
	var pol *harbor.ReplicationPolicy
	if obj.Status.PolicyID != nil {
		got, err := hc.GetReplicationPolicy(ctx, *obj.Status.PolicyID)
		switch {
		case err == nil:
			pol = got
		case harbor.IsNotFound(err):
			r.Recorder.Eventf(obj, nil, corev1.EventTypeWarning, harborv1alpha1.ReasonExternalResourceMissing, "Recreate",
				"Replication policy %d disappeared from Harbor; recreating", *obj.Status.PolicyID)
			obj.Status.PolicyID = nil
			obj.Status.ExecutedOnCreate = false
		default:
			return conn, ctrl.Result{}, classifyHarborError(err, "get replication policy")
		}
	}
	if pol == nil {
		list, err := hc.ListReplicationPolicies(ctx, name)
		if err != nil {
			return conn, ctrl.Result{}, classifyHarborError(err, "list replication policies")
		}
		for i := range list {
			if list[i].Name == name {
				if !obj.Spec.AdoptExisting {
					return conn, ctrl.Result{}, terminal(harborv1alpha1.ReasonAlreadyExists,
						"replication policy %q already exists in Harbor (id %d); set spec.adoptExisting to manage it", name, list[i].ID)
				}
				pol = &list[i]
				obj.Status.ExecutedOnCreate = true // never auto-run an adopted policy
				r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Adopted", "Adopt", "Adopted existing replication policy %q (id %d)", name, pol.ID)
				break
			}
		}
	}
	created := false
	if pol == nil {
		id, err := hc.CreateReplicationPolicy(ctx, desired)
		if err != nil && !harbor.IsConflict(err) {
			return conn, ctrl.Result{}, classifyHarborError(err, "create replication policy")
		}
		if id == 0 {
			list, lerr := hc.ListReplicationPolicies(ctx, name)
			if lerr != nil {
				return conn, ctrl.Result{}, classifyHarborError(lerr, "list replication policies after create")
			}
			for i := range list {
				if list[i].Name == name {
					id = list[i].ID
				}
			}
			if id == 0 {
				return conn, ctrl.Result{}, transient(harborv1alpha1.ReasonHarborError, nil, "replication policy %q not found after create", name)
			}
		}
		got, err := hc.GetReplicationPolicy(ctx, id)
		if err != nil {
			return conn, ctrl.Result{}, classifyHarborError(err, "read replication policy after create")
		}
		pol = got
		created = true
		r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Created", "Create", "Created replication policy %q (id %d)", name, pol.ID)
	}
	obj.Status.PolicyID = new(pol.ID)

	// Drift.
	if !created {
		if changed := policyDrift(desired, pol); len(changed) > 0 {
			upd := desired
			upd.ID = pol.ID
			if err := hc.UpdateReplicationPolicy(ctx, pol.ID, upd); err != nil {
				return conn, ctrl.Result{}, classifyHarborError(err, "update replication policy")
			}
			reason := changeReason("HarborReplicationPolicy", obj.Generation, obj.Status.ObservedGeneration, obj.Status.Conditions)
			r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, reason, "Update", "Updated replication policy fields: %s", strings.Join(changed, ", "))
		}
	}

	// One-off execution after creation.
	if obj.Spec.ExecuteOnCreate && !obj.Status.ExecutedOnCreate {
		if err := hc.StartReplication(ctx, pol.ID); err != nil {
			return conn, ctrl.Result{}, classifyHarborError(err, "start replication")
		}
		obj.Status.ExecutedOnCreate = true
		r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Executed", "Execute", "Started replication for policy %d", pol.ID)
	}

	// Mirror latest execution.
	if exec, err := hc.LatestReplicationExecution(ctx, pol.ID); err == nil && exec != nil {
		st := &harborv1alpha1.ReplicationExecutionStatus{ID: exec.ID, Status: exec.Status}
		if !exec.StartTime.IsZero() {
			st.StartTime = new(metav1.NewTime(exec.StartTime))
		}
		if !exec.EndTime.IsZero() && exec.EndTime.Unix() > 0 {
			st.EndTime = new(metav1.NewTime(exec.EndTime))
		}
		obj.Status.LastExecution = st
	}

	setReady(&obj.Status.Conditions, obj.Generation, metav1.ConditionTrue, harborv1alpha1.ReasonSynced,
		fmt.Sprintf("replication policy %q (id %d) matches spec", pol.Name, pol.ID))
	return conn, ctrl.Result{RequeueAfter: period}, nil
}

// policyDrift lists managed fields whose Harbor value differs from desired.
func policyDrift(want harbor.ReplicationPolicy, got *harbor.ReplicationPolicy) []string {
	var changed []string
	regID := func(r *harbor.Registry) int64 {
		if r == nil {
			return 0
		}
		return r.ID
	}
	if want.Name != got.Name {
		changed = append(changed, "name")
	}
	if want.Description != got.Description {
		changed = append(changed, "description")
	}
	if regID(want.SrcRegistry) != regID(got.SrcRegistry) {
		changed = append(changed, "srcRegistryRef")
	}
	if regID(want.DestRegistry) != regID(got.DestRegistry) {
		changed = append(changed, "destRegistryRef")
	}
	if want.DestNamespace != got.DestNamespace {
		changed = append(changed, "destNamespace")
	}
	if got.DestNamespaceReplaceCount == nil || *want.DestNamespaceReplaceCount != *got.DestNamespaceReplaceCount {
		changed = append(changed, "destNamespaceReplaceCount")
	}
	gotType, gotCron := "manual", ""
	if got.Trigger != nil {
		gotType = got.Trigger.Type
		if got.Trigger.TriggerSettings != nil {
			gotCron = got.Trigger.TriggerSettings.Cron
		}
	}
	wantCron := ""
	if want.Trigger.TriggerSettings != nil {
		wantCron = want.Trigger.TriggerSettings.Cron
	}
	if want.Trigger.Type != gotType || wantCron != gotCron {
		changed = append(changed, "trigger")
	}
	if filtersKey(want.Filters) != filtersKey(got.Filters) {
		changed = append(changed, "filters")
	}
	if want.ReplicateDeletion != got.ReplicateDeletion {
		changed = append(changed, "replicateDeletion")
	}
	if want.Override != got.Override {
		changed = append(changed, "override")
	}
	if want.Enabled != got.Enabled {
		changed = append(changed, "enabled")
	}
	if deref(want.Speed) != deref(got.Speed) {
		changed = append(changed, "speedKBps")
	}
	if deref(want.CopyByChunk) != deref(got.CopyByChunk) {
		changed = append(changed, "copyByChunk")
	}
	if deref(want.SingleActiveReplication) != deref(got.SingleActiveReplication) {
		changed = append(changed, "singleActiveReplication")
	}
	return changed
}

func (r *HarborReplicationPolicyReconciler) reconcileDelete(ctx context.Context, obj *harborv1alpha1.HarborReplicationPolicy, base *harborv1alpha1.HarborReplicationPolicy) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(obj, harborv1alpha1.Finalizer) {
		return ctrl.Result{}, nil
	}
	if obj.Spec.DeletionPolicy != harborv1alpha1.DeletionPolicyRetain && obj.Status.PolicyID != nil {
		_, hc, rerr := connectAndClient(ctx, r.Client, obj.Spec.ConnectionRef, obj.Namespace)
		if rerr != nil && rerr.reason != harborv1alpha1.ReasonConnectionNotFound {
			setReady(&obj.Status.Conditions, obj.Generation, metav1.ConditionFalse, rerr.reason, rerr.message)
			_ = r.Status().Patch(ctx, obj, client.MergeFrom(base))
			return ctrl.Result{RequeueAfter: errorRetryAfter}, nil
		}
		if rerr == nil {
			if err := hc.DeleteReplicationPolicy(ctx, *obj.Status.PolicyID); err != nil && !harbor.IsNotFound(err) {
				msg := fmt.Sprintf("Delete replication policy %d: %v", *obj.Status.PolicyID, err)
				r.Recorder.Eventf(obj, nil, corev1.EventTypeWarning, harborv1alpha1.ReasonHarborError, "Delete", "%s", msg)
				setReady(&obj.Status.Conditions, obj.Generation, metav1.ConditionFalse, harborv1alpha1.ReasonHarborError, msg)
				_ = r.Status().Patch(ctx, obj, client.MergeFrom(base))
				return ctrl.Result{RequeueAfter: errorRetryAfter}, nil
			}
			r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Deleted", "Delete", "Deleted replication policy %d from Harbor", *obj.Status.PolicyID)
		}
	}
	controllerutil.RemoveFinalizer(obj, harborv1alpha1.Finalizer)
	return ctrl.Result{}, r.Patch(ctx, obj, client.MergeFrom(base))
}

// SetupWithManager sets up the controller with the Manager.
func (r *HarborReplicationPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&harborv1alpha1.HarborReplicationPolicy{}).
		Watches(&harborv1alpha1.HarborRegistry{}, handler.EnqueueRequestsFromMapFunc(enqueuePoliciesForRegistry(mgr.GetClient()))).
		Watches(&harborv1alpha1.HarborConnection{}, handler.EnqueueRequestsFromMapFunc(enqueuePoliciesForConnection(mgr.GetClient()))).
		Named("harborreplicationpolicy").
		Complete(r)
}
