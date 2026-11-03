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
	"errors"
	"fmt"
	"net/http"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	harborv1alpha1 "github.com/oreondev/harbor-resource-operator/api/v1alpha1"
	"github.com/oreondev/harbor-resource-operator/pkg/harbor"
)

// HarborRetentionPolicyReconciler reconciles a HarborRetentionPolicy object
type HarborRetentionPolicyReconciler struct {
	client.Client
	// APIReader reads the reconciled object uncached; see getFresh.
	APIReader client.Reader
	Scheme    *runtime.Scheme
	Recorder  events.EventRecorder
}

// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborretentionpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborretentionpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborretentionpolicies/finalizers,verbs=update

const (
	retentionAlgorithm   = "or"
	retentionAction      = "retain"
	retentionTriggerKind = "Schedule"
	retentionScopeLevel  = "project"
	metaRetentionID      = "retention_id"
)

// Reconcile drives a Harbor tag retention policy to the declared state.
func (r *HarborRetentionPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return reconcileResource(ctx, r.Client, r.APIReader, r.Recorder, req, &harborv1alpha1.HarborRetentionPolicy{}, "retention policy", r)
}

func desiredRetention(obj *harborv1alpha1.HarborRetentionPolicy, projectID int64) harbor.RetentionPolicy {
	p := harbor.RetentionPolicy{
		Algorithm: retentionAlgorithm,
		Trigger: &harbor.RetentionTrigger{Kind: retentionTriggerKind,
			Settings: map[string]string{"cron": obj.Spec.Schedule}, References: map[string]any{}},
		Scope: &harbor.RetentionScope{Level: retentionScopeLevel, Ref: projectID},
	}
	for _, rule := range obj.Spec.Rules {
		params := map[string]any{}
		if rule.Template != harborv1alpha1.RetainAlways && rule.Count != nil {
			params[string(rule.Template)] = *rule.Count
		}
		untagged := rule.Tags.Untagged
		if untagged == nil {
			untagged = new(true)
		}
		p.Rules = append(p.Rules, harbor.TagRule{
			Action:         retentionAction,
			Template:       string(rule.Template),
			Disabled:       rule.Disabled,
			Params:         params,
			TagSelectors:   tagSelectors(rule.Tags.PatternSelector, untagged),
			ScopeSelectors: repositorySelectors(rule.Repositories),
		})
	}
	return p
}

// retentionMatches compares rules in order and the schedule.
func retentionMatches(got *harbor.RetentionPolicy, want harbor.RetentionPolicy) bool {
	if len(got.Rules) != len(want.Rules) {
		return false
	}
	for i := range want.Rules {
		if ruleKey(got.Rules[i]) != ruleKey(want.Rules[i]) {
			return false
		}
	}
	gotCron := ""
	if got.Trigger != nil {
		gotCron = got.Trigger.Settings["cron"]
	}
	return gotCron == want.Trigger.Settings["cron"]
}

func (r *HarborRetentionPolicyReconciler) sync(ctx context.Context, obj *harborv1alpha1.HarborRetentionPolicy,
	hc *harbor.Client, _ *harborv1alpha1.HarborConnection) (ctrl.Result, *reconcileError) {
	proj, res, rerr := resolveProject(ctx, r.Client, hc, obj.Namespace, obj.Spec.ProjectReference, obj.GetConnectionRef())
	if rerr != nil {
		return res, rerr
	}
	obj.Status.ProjectID = new(proj.id)
	obj.Status.ProjectName = proj.name
	want := desiredRetention(obj, proj.id)

	policy, rerr := r.locate(ctx, hc, obj, proj)
	if rerr != nil {
		return ctrl.Result{}, rerr
	}
	if policy == nil {
		id, err := hc.CreateRetention(ctx, want)
		if err != nil {
			return ctrl.Result{}, classifyHarborError(err, "create retention policy")
		}
		if id == 0 {
			if id, rerr = projectRetentionID(ctx, hc, proj.id); rerr != nil {
				return ctrl.Result{}, rerr
			}
		}
		if id == 0 {
			return ctrl.Result{}, transient(harborv1alpha1.ReasonHarborError, nil, "Harbor did not return the ID of the new retention policy")
		}
		obj.Status.RetentionID = new(id)
		r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Created", "Create", "Created retention policy %d for project %q", id, proj.name)
	} else {
		obj.Status.RetentionID = new(policy.ID)
		if !retentionMatches(policy, want) {
			want.ID = policy.ID
			if err := hc.UpdateRetention(ctx, policy.ID, want); err != nil {
				return ctrl.Result{}, classifyHarborError(err, "update retention policy")
			}
			reason := changeReason("HarborRetentionPolicy", obj.Generation, obj.Status.ObservedGeneration, obj.Status.Conditions)
			r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, reason, "Update", "Updated retention policy %d", policy.ID)
		}
	}
	setReady(&obj.Status.Conditions, obj.Generation, metav1.ConditionTrue, harborv1alpha1.ReasonSynced,
		fmt.Sprintf("retention policy %d of project %q matches spec", *obj.Status.RetentionID, proj.name))
	return ctrl.Result{}, nil
}

// projectRetentionID reads the retention_id metadata of a project; 0 when unset.
func projectRetentionID(ctx context.Context, hc *harbor.Client, projectID int64) (int64, *reconcileError) {
	p, err := hc.GetProject(ctx, projectID)
	if err != nil {
		return 0, classifyHarborError(err, "get project")
	}
	if p.Metadata == nil || p.Metadata.RetentionID == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(p.Metadata.RetentionID, 10, 64)
	if err != nil {
		return 0, nil
	}
	return id, nil
}

// locate finds the policy by stored ID, then through the project's retention_id.
func (r *HarborRetentionPolicyReconciler) locate(ctx context.Context, hc *harbor.Client, obj *harborv1alpha1.HarborRetentionPolicy,
	proj *resolvedProject) (*harbor.RetentionPolicy, *reconcileError) {
	if obj.Status.RetentionID != nil {
		p, err := hc.GetRetention(ctx, *obj.Status.RetentionID)
		if err != nil && retentionMissing(ctx, hc, err) {
			err = errRetentionMissing
		}
		switch {
		case err == nil && p.Scope != nil && p.Scope.Ref == proj.id:
			return p, nil
		case err == nil, harbor.IsNotFound(err):
			r.Recorder.Eventf(obj, nil, corev1.EventTypeWarning, harborv1alpha1.ReasonExternalResourceMissing, "Recreate",
				"Retention policy %d disappeared from project %q; recreating", *obj.Status.RetentionID, proj.name)
			obj.Status.RetentionID = nil
		default:
			return nil, classifyHarborError(err, "get retention policy")
		}
	}
	id, rerr := projectRetentionID(ctx, hc, proj.id)
	if rerr != nil || id == 0 {
		return nil, rerr
	}
	p, err := hc.GetRetention(ctx, id)
	switch {
	case err != nil && retentionMissing(ctx, hc, err):
		// The project points at a policy that no longer exists; clear the stale pointer.
		if derr := hc.DeleteProjectMetadata(ctx, proj.id, metaRetentionID); derr != nil && !harbor.IsNotFound(derr) {
			return nil, classifyHarborError(derr, "clear stale retention_id")
		}
		return nil, nil
	case err != nil:
		return nil, classifyHarborError(err, "get retention policy")
	}
	if !obj.Spec.AdoptExisting {
		return nil, terminal(harborv1alpha1.ReasonAlreadyExists,
			"project %q already has retention policy %d; set spec.adoptExisting to manage it", proj.name, id)
	}
	r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Adopted", "Adopt", "Adopted retention policy %d of project %q", id, proj.name)
	return p, nil
}

func (r *HarborRetentionPolicyReconciler) hasExternal(obj *harborv1alpha1.HarborRetentionPolicy) bool {
	return obj.Status.RetentionID != nil
}

func (r *HarborRetentionPolicyReconciler) remove(ctx context.Context, obj *harborv1alpha1.HarborRetentionPolicy, hc *harbor.Client) error {
	id := *obj.Status.RetentionID
	if err := hc.DeleteRetention(ctx, id); err != nil && !retentionMissing(ctx, hc, err) {
		return err
	}
	// Harbor 2.14 and later clear the project's retention_id on delete; 2.12
	// and 2.13 leave it pointing at the deleted policy. Clear it if it still points here.
	if obj.Status.ProjectID == nil {
		return nil
	}
	cur, rerr := projectRetentionID(ctx, hc, *obj.Status.ProjectID)
	if rerr != nil || cur != id {
		return nil
	}
	if err := hc.DeleteProjectMetadata(ctx, *obj.Status.ProjectID, metaRetentionID); err != nil && !harbor.IsNotFound(err) {
		return err
	}
	return nil
}

// errRetentionMissing stands in for the 404 Harbor should return for a
// deleted retention policy.
var errRetentionMissing = &harbor.APIError{StatusCode: http.StatusNotFound, Method: http.MethodGet, Path: "/retentions"}

// retentionMissing reports whether err from a /retentions/{id} call means the
// policy does not exist. Harbor (2.12 to 2.15) answers 500 instead of 404 for
// a deleted policy; that is told apart from an outage by checking that Harbor
// otherwise answers.
func retentionMissing(ctx context.Context, hc *harbor.Client, err error) bool {
	if harbor.IsNotFound(err) {
		return true
	}
	var ae *harbor.APIError
	if !errors.As(err, &ae) || ae.StatusCode != http.StatusInternalServerError {
		return false
	}
	_, perr := hc.SystemInfo(ctx)
	return perr == nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HarborRetentionPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	newList := func() client.ObjectList { return &harborv1alpha1.HarborRetentionPolicyList{} }
	return ctrl.NewControllerManagedBy(mgr).
		For(&harborv1alpha1.HarborRetentionPolicy{}).
		Watches(&harborv1alpha1.HarborConnection{}, handler.EnqueueRequestsFromMapFunc(enqueueForConnection(mgr.GetClient(), newList))).
		Watches(&harborv1alpha1.HarborProject{}, handler.EnqueueRequestsFromMapFunc(enqueueForProject(mgr.GetClient(), newList))).
		Named("harborretentionpolicy").
		Complete(r)
}
