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
	"fmt"

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

// HarborImmutableRuleReconciler reconciles a HarborImmutableRule object
type HarborImmutableRuleReconciler struct {
	client.Client
	// APIReader reads the reconciled object uncached; see getFresh.
	APIReader client.Reader
	Scheme    *runtime.Scheme
	Recorder  events.EventRecorder
}

// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborimmutablerules,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborimmutablerules/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborimmutablerules/finalizers,verbs=update

const (
	immutableAction   = "immutable"
	immutableTemplate = "immutable_template"
)

// Reconcile drives a Harbor immutable tag rule to the declared state.
func (r *HarborImmutableRuleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return reconcileResource(ctx, r.Client, r.APIReader, r.Recorder, req, &harborv1alpha1.HarborImmutableRule{}, "immutable tag rule", r)
}

func desiredImmutableRule(obj *harborv1alpha1.HarborImmutableRule) harbor.TagRule {
	return harbor.TagRule{
		Action:         immutableAction,
		Template:       immutableTemplate,
		Disabled:       obj.Spec.Disabled,
		Params:         map[string]any{},
		TagSelectors:   tagSelectors(obj.Spec.Tags, nil),
		ScopeSelectors: repositorySelectors(obj.Spec.Repositories),
	}
}

// sameSelectors reports whether two rules select the same tags, ignoring the disabled flag.
func sameSelectors(a, b harbor.TagRule) bool {
	a.Disabled, b.Disabled = false, false
	return ruleKey(a) == ruleKey(b)
}

func (r *HarborImmutableRuleReconciler) sync(ctx context.Context, obj *harborv1alpha1.HarborImmutableRule,
	hc *harbor.Client, _ *harborv1alpha1.HarborConnection) (ctrl.Result, *reconcileError) {
	proj, res, rerr := resolveProject(ctx, r.Client, hc, obj.Namespace, obj.Spec.ProjectReference, obj.GetConnectionRef())
	if rerr != nil {
		return res, rerr
	}
	obj.Status.ProjectID = new(proj.id)
	obj.Status.ProjectName = proj.name
	want := desiredImmutableRule(obj)

	rules, err := hc.ListImmutableRules(ctx, proj.id)
	if err != nil {
		return ctrl.Result{}, classifyHarborError(err, "list immutable tag rules")
	}
	var rule *harbor.TagRule
	if obj.Status.RuleID != nil {
		for i := range rules {
			if rules[i].ID == *obj.Status.RuleID {
				rule = &rules[i]
			}
		}
		if rule == nil {
			r.Recorder.Eventf(obj, nil, corev1.EventTypeWarning, harborv1alpha1.ReasonExternalResourceMissing, "Recreate",
				"Immutable tag rule %d disappeared from project %q; recreating", *obj.Status.RuleID, proj.name)
			obj.Status.RuleID = nil
		}
	}
	if rule == nil {
		for i := range rules {
			if !sameSelectors(rules[i], want) {
				continue
			}
			if !obj.Spec.AdoptExisting {
				return ctrl.Result{}, terminal(harborv1alpha1.ReasonAlreadyExists,
					"project %q already has an immutable tag rule with these selectors (id %d); set spec.adoptExisting to manage it", proj.name, rules[i].ID)
			}
			rule = &rules[i]
			r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Adopted", "Adopt", "Adopted existing immutable tag rule %d", rule.ID)
			break
		}
	}
	if rule == nil {
		id, err := hc.CreateImmutableRule(ctx, proj.id, want)
		if err != nil {
			return ctrl.Result{}, classifyHarborError(err, "create immutable tag rule")
		}
		if id == 0 {
			// No Location header: find the rule we just created by its selectors.
			if rules, err = hc.ListImmutableRules(ctx, proj.id); err != nil {
				return ctrl.Result{}, classifyHarborError(err, "list immutable tag rules after create")
			}
			for i := range rules {
				if sameSelectors(rules[i], want) {
					id = rules[i].ID
				}
			}
		}
		created := want
		created.ID = id
		rule = &created
		r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Created", "Create", "Created immutable tag rule %d in project %q", id, proj.name)
	} else if ruleKey(*rule) != ruleKey(want) {
		upd := want
		upd.ID = rule.ID
		upd.Priority = rule.Priority
		if err := hc.UpdateImmutableRule(ctx, proj.id, rule.ID, upd); err != nil {
			return ctrl.Result{}, classifyHarborError(err, "update immutable tag rule")
		}
		reason := changeReason("HarborImmutableRule", obj.Generation, obj.Status.ObservedGeneration, obj.Status.Conditions)
		r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, reason, "Update", "Updated immutable tag rule %d", rule.ID)
	}
	if rule.ID == 0 {
		return ctrl.Result{}, transient(harborv1alpha1.ReasonHarborError, nil, "Harbor did not return the ID of the new rule")
	}
	obj.Status.RuleID = new(rule.ID)
	setReady(&obj.Status.Conditions, obj.Generation, metav1.ConditionTrue, harborv1alpha1.ReasonSynced,
		fmt.Sprintf("immutable tag rule %d in project %q matches spec", rule.ID, proj.name))
	return ctrl.Result{}, nil
}

func (r *HarborImmutableRuleReconciler) hasExternal(obj *harborv1alpha1.HarborImmutableRule) bool {
	return obj.Status.RuleID != nil && obj.Status.ProjectID != nil
}

func (r *HarborImmutableRuleReconciler) remove(ctx context.Context, obj *harborv1alpha1.HarborImmutableRule, hc *harbor.Client) error {
	return hc.DeleteImmutableRule(ctx, *obj.Status.ProjectID, *obj.Status.RuleID)
}

// SetupWithManager sets up the controller with the Manager.
func (r *HarborImmutableRuleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	newList := func() client.ObjectList { return &harborv1alpha1.HarborImmutableRuleList{} }
	return ctrl.NewControllerManagedBy(mgr).
		For(&harborv1alpha1.HarborImmutableRule{}).
		Watches(&harborv1alpha1.HarborConnection{}, handler.EnqueueRequestsFromMapFunc(enqueueForConnection(mgr.GetClient(), newList))).
		Watches(&harborv1alpha1.HarborProject{}, handler.EnqueueRequestsFromMapFunc(enqueueForProject(mgr.GetClient(), newList))).
		Named("harborimmutablerule").
		Complete(r)
}
