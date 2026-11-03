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
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	harborv1alpha1 "github.com/oreondev/harbor-resource-operator/api/v1alpha1"
	"github.com/oreondev/harbor-resource-operator/pkg/harbor"
)

// HarborGarbageCollectionReconciler reconciles a HarborGarbageCollection object
type HarborGarbageCollectionReconciler struct {
	client.Client
	// APIReader reads the reconciled object uncached; see getFresh.
	APIReader client.Reader
	Scheme    *runtime.Scheme
	Recorder  events.EventRecorder
}

// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborgarbagecollections,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborgarbagecollections/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborgarbagecollections/finalizers,verbs=update

const (
	gcParamDeleteUntagged = "delete_untagged"
	gcParamWorkers        = "workers"
)

// presetCrons are the cron expressions Harbor stores for its preset schedules.
var presetCrons = map[string]string{
	harbor.ScheduleHourly: "0 0 * * * *",
	harbor.ScheduleDaily:  "0 0 0 * * *",
	harbor.ScheduleWeekly: "0 0 0 * * 0",
}

// Reconcile drives Harbor's garbage collection schedule to the declared state.
func (r *HarborGarbageCollectionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return reconcileResource(ctx, r.Client, r.APIReader, r.Recorder, req, &harborv1alpha1.HarborGarbageCollection{}, "garbage collection schedule", r)
}

// desiredSchedule maps spec.schedule to Harbor's schedule type and cron.
func desiredSchedule(spec string) harbor.ScheduleObj {
	switch spec {
	case harbor.ScheduleNone:
		return harbor.ScheduleObj{Type: harbor.ScheduleNone}
	case harbor.ScheduleHourly, harbor.ScheduleDaily, harbor.ScheduleWeekly:
		return harbor.ScheduleObj{Type: spec, Cron: presetCrons[spec]}
	default:
		return harbor.ScheduleObj{Type: harbor.ScheduleCustom, Cron: spec}
	}
}

// scheduleExists reports whether Harbor has a schedule record to PUT to.
func scheduleExists(s *harbor.GCSchedule) bool {
	return s != nil && (s.ID != 0 || (s.Schedule != nil && s.Schedule.Type != "" && s.Schedule.Type != harbor.ScheduleNone))
}

// gcDrift lists the differences between Harbor's schedule and the spec.
func gcDrift(got *harbor.GCSchedule, want harbor.ScheduleObj, deleteUntagged bool, workers int32) []string {
	var changed []string
	gs := harbor.ScheduleObj{Type: harbor.ScheduleNone}
	if got != nil && got.Schedule != nil && got.Schedule.Type != "" {
		gs = *got.Schedule
	}
	if !strings.EqualFold(gs.Type, want.Type) || (want.Type == harbor.ScheduleCustom && gs.Cron != want.Cron) {
		changed = append(changed, "schedule")
	}
	if want.Type == harbor.ScheduleNone {
		return changed
	}
	var params map[string]any
	if got != nil {
		params = got.Parameters
	}
	if b, _ := params[gcParamDeleteUntagged].(bool); b != deleteUntagged {
		changed = append(changed, "deleteUntagged")
	}
	if w, _ := normalizeNumber(params[gcParamWorkers]).(int64); w != int64(workers) {
		changed = append(changed, "workers")
	}
	return changed
}

// isWinner reports whether obj is the oldest HarborGarbageCollection on its connection.
func (r *HarborGarbageCollectionReconciler) isWinner(ctx context.Context, obj *harborv1alpha1.HarborGarbageCollection) (string, *reconcileError) {
	var list harborv1alpha1.HarborGarbageCollectionList
	if err := r.List(ctx, &list); err != nil {
		return "", transient(harborv1alpha1.ReasonHarborError, err, "list HarborGarbageCollections")
	}
	winner := obj
	for i := range list.Items {
		o := &list.Items[i]
		if o.GetConnectionRef() != obj.GetConnectionRef() || !o.DeletionTimestamp.IsZero() {
			continue
		}
		if o.CreationTimestamp.Before(&winner.CreationTimestamp) ||
			(o.CreationTimestamp.Equal(&winner.CreationTimestamp) && o.Namespace+"/"+o.Name < winner.Namespace+"/"+winner.Name) {
			winner = o
		}
	}
	if winner.UID != obj.UID {
		return winner.Namespace + "/" + winner.Name, nil
	}
	return "", nil
}

func (r *HarborGarbageCollectionReconciler) sync(ctx context.Context, obj *harborv1alpha1.HarborGarbageCollection,
	hc *harbor.Client, _ *harborv1alpha1.HarborConnection) (ctrl.Result, *reconcileError) {
	other, rerr := r.isWinner(ctx, obj)
	if rerr != nil {
		return ctrl.Result{}, rerr
	}
	if other != "" {
		return ctrl.Result{}, terminal(harborv1alpha1.ReasonDuplicate,
			"HarborGarbageCollection %s already manages garbage collection for this connection; this object has no effect", other)
	}
	workers := obj.Spec.Workers
	if workers == 0 {
		workers = 1
	}
	want := desiredSchedule(obj.Spec.Schedule)

	got, err := hc.GetGCSchedule(ctx)
	if err != nil {
		return ctrl.Result{}, classifyHarborError(err, "get garbage collection schedule")
	}
	if changed := gcDrift(got, want, obj.Spec.DeleteUntagged, workers); len(changed) > 0 {
		body := harbor.GCSchedule{Schedule: &want, Parameters: map[string]any{
			gcParamDeleteUntagged: obj.Spec.DeleteUntagged, gcParamWorkers: workers}}
		if err := r.write(ctx, hc, got, body); err != nil {
			return ctrl.Result{}, classifyHarborError(err, "set garbage collection schedule")
		}
		reason := changeReason("HarborGarbageCollection", obj.Generation, obj.Status.ObservedGeneration, obj.Status.Conditions)
		r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, reason, "Update", "Updated garbage collection fields: %s", strings.Join(changed, ", "))
		if got, err = hc.GetGCSchedule(ctx); err != nil {
			return ctrl.Result{}, classifyHarborError(err, "read garbage collection schedule")
		}
	}
	obj.Status.NextScheduledTime = ""
	if got != nil && got.Schedule != nil {
		obj.Status.NextScheduledTime = got.Schedule.NextScheduledTime
	}
	if last, err := hc.LatestGC(ctx); err == nil && last != nil {
		run := &harborv1alpha1.GCRunStatus{ID: last.ID, Status: last.JobStatus}
		if !last.CreationTime.IsZero() {
			run.StartTime = new(metav1.NewTime(last.CreationTime))
		}
		if !last.UpdateTime.IsZero() {
			run.UpdateTime = new(metav1.NewTime(last.UpdateTime))
		}
		obj.Status.LastRun = run
	}
	setReady(&obj.Status.Conditions, obj.Generation, metav1.ConditionTrue, harborv1alpha1.ReasonSynced,
		fmt.Sprintf("garbage collection schedule %q matches spec", obj.Spec.Schedule))
	return ctrl.Result{}, nil
}

// write creates or updates the schedule. Harbor wants POST for the first
// schedule and PUT afterwards.
func (r *HarborGarbageCollectionReconciler) write(ctx context.Context, hc *harbor.Client, got *harbor.GCSchedule, body harbor.GCSchedule) error {
	if scheduleExists(got) {
		return hc.UpdateGCSchedule(ctx, body)
	}
	err := hc.CreateGCSchedule(ctx, body)
	if harbor.IsConflict(err) {
		return hc.UpdateGCSchedule(ctx, body)
	}
	return err
}

// hasExternal is true only for the object that manages the schedule, so
// deleting a duplicate never touches Harbor.
func (r *HarborGarbageCollectionReconciler) hasExternal(obj *harborv1alpha1.HarborGarbageCollection) bool {
	c := meta.FindStatusCondition(obj.Status.Conditions, harborv1alpha1.ConditionReady)
	return c != nil && c.Reason != harborv1alpha1.ReasonDuplicate
}

func (r *HarborGarbageCollectionReconciler) remove(ctx context.Context, _ *harborv1alpha1.HarborGarbageCollection, hc *harbor.Client) error {
	got, err := hc.GetGCSchedule(ctx)
	if err != nil {
		return err
	}
	if !scheduleExists(got) {
		return nil
	}
	return hc.UpdateGCSchedule(ctx, harbor.GCSchedule{Schedule: &harbor.ScheduleObj{Type: harbor.ScheduleNone}})
}

// enqueueGCSiblings re-reconciles every HarborGarbageCollection on the same
// connection when one changes, so a duplicate takes over when the winner goes.
func enqueueGCSiblings(c client.Client) handler.MapFunc {
	return func(ctx context.Context, o client.Object) []reconcile.Request {
		gc, ok := o.(*harborv1alpha1.HarborGarbageCollection)
		if !ok {
			return nil
		}
		return listRequests(ctx, c, &harborv1alpha1.HarborGarbageCollectionList{}, nil, func(x client.Object) bool {
			s, ok := x.(*harborv1alpha1.HarborGarbageCollection)
			return ok && s.UID != gc.UID && s.GetConnectionRef() == gc.GetConnectionRef()
		})
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *HarborGarbageCollectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	newList := func() client.ObjectList { return &harborv1alpha1.HarborGarbageCollectionList{} }
	return ctrl.NewControllerManagedBy(mgr).
		For(&harborv1alpha1.HarborGarbageCollection{}).
		Watches(&harborv1alpha1.HarborConnection{}, handler.EnqueueRequestsFromMapFunc(enqueueForConnection(mgr.GetClient(), newList))).
		Watches(&harborv1alpha1.HarborGarbageCollection{}, handler.EnqueueRequestsFromMapFunc(enqueueGCSiblings(mgr.GetClient()))).
		Named("harborgarbagecollection").
		Complete(r)
}
