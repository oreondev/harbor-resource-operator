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
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	harborv1alpha1 "github.com/oreondev/harbor-resource-operator/api/v1alpha1"
	"github.com/oreondev/harbor-resource-operator/pkg/harbor"
)

// HarborWebhookReconciler reconciles a HarborWebhook object
type HarborWebhookReconciler struct {
	client.Client
	// APIReader reads the reconciled object uncached; see getFresh.
	APIReader client.Reader
	Scheme    *runtime.Scheme
	Recorder  events.EventRecorder
}

// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborwebhooks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborwebhooks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborwebhooks/finalizers,verbs=update

const payloadDefault = "Default"

// Reconcile drives a Harbor webhook policy to the declared state.
func (r *HarborWebhookReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return reconcileResource(ctx, r.Client, r.APIReader, r.Recorder, req, &harborv1alpha1.HarborWebhook{}, "webhook policy", r)
}

// desiredWebhook builds the policy from spec, reading auth headers from
// Secrets. It returns the policy and the Secret versions it used.
func (r *HarborWebhookReconciler) desiredWebhook(ctx context.Context, obj *harborv1alpha1.HarborWebhook,
	name string, projectID int64) (harbor.WebhookPolicy, string, *reconcileError) {
	p := harbor.WebhookPolicy{
		Name:        name,
		Description: obj.Spec.Description,
		ProjectID:   projectID,
		Enabled:     obj.Spec.Enabled == nil || *obj.Spec.Enabled,
		EventTypes:  slices.Sorted(slices.Values(obj.Spec.EventTypes)),
	}
	var versions []string
	for i, t := range obj.Spec.Targets {
		ht := harbor.WebhookTarget{Type: string(t.Type), Address: t.Address, SkipCertVerify: t.SkipCertVerify, PayloadFormat: t.PayloadFormat}
		if ht.Type == "" {
			ht.Type = string(harborv1alpha1.WebhookTargetHTTP)
		}
		if ht.Type == string(harborv1alpha1.WebhookTargetHTTP) && ht.PayloadFormat == "" {
			ht.PayloadFormat = payloadDefault
		}
		if ref := t.AuthHeaderSecretRef; ref != nil {
			sec := &corev1.Secret{}
			key := types.NamespacedName{Name: ref.Name, Namespace: obj.Namespace}
			if err := r.Get(ctx, key, sec); err != nil {
				if apierrors.IsNotFound(err) {
					return p, "", &reconcileError{reason: harborv1alpha1.ReasonSecretMissing, retry: true,
						message: fmt.Sprintf("auth header Secret %s of target %d not found", key, i)}
				}
				return p, "", transient(harborv1alpha1.ReasonSecretMissing, err, "get Secret %s", key)
			}
			v, ok := sec.Data[ref.Key]
			if !ok || len(v) == 0 {
				return p, "", terminal(harborv1alpha1.ReasonSecretMissing, "Secret %s has no key %q", key, ref.Key)
			}
			ht.AuthHeader = strings.TrimSpace(string(v))
			versions = append(versions, ref.Name+"@"+sec.ResourceVersion)
		}
		p.Targets = append(p.Targets, ht)
	}
	return p, strings.Join(versions, ","), nil
}

// targetKey renders a target without its auth header, normalizing Harbor's defaults.
func targetKey(t harbor.WebhookTarget) string {
	pf := t.PayloadFormat
	if pf == "" && t.Type == string(harborv1alpha1.WebhookTargetHTTP) {
		pf = payloadDefault
	}
	return fmt.Sprintf("%s|%s|%t|%s", t.Type, t.Address, t.SkipCertVerify, pf)
}

// webhookDrift lists the fields that differ. Auth headers are compared only
// when Harbor returns them, and through the Secret version otherwise.
func webhookDrift(got, want harbor.WebhookPolicy, gotVersion, wantVersion string) []string {
	var changed []string
	if got.Name != want.Name {
		changed = append(changed, "name")
	}
	if got.Description != want.Description {
		changed = append(changed, "description")
	}
	if got.Enabled != want.Enabled {
		changed = append(changed, "enabled")
	}
	if !slices.Equal(slices.Sorted(slices.Values(got.EventTypes)), want.EventTypes) {
		changed = append(changed, "eventTypes")
	}
	sameTargets := len(got.Targets) == len(want.Targets)
	for i := 0; sameTargets && i < len(want.Targets); i++ {
		sameTargets = targetKey(got.Targets[i]) == targetKey(want.Targets[i]) &&
			(got.Targets[i].AuthHeader == "" || got.Targets[i].AuthHeader == want.Targets[i].AuthHeader)
	}
	if !sameTargets {
		changed = append(changed, "targets")
	}
	if gotVersion != wantVersion {
		changed = append(changed, "authHeaderSecretRef")
	}
	return changed
}

func (r *HarborWebhookReconciler) sync(ctx context.Context, obj *harborv1alpha1.HarborWebhook,
	hc *harbor.Client, _ *harborv1alpha1.HarborConnection) (ctrl.Result, *reconcileError) {
	proj, res, rerr := resolveProject(ctx, r.Client, hc, obj.Namespace, obj.Spec.ProjectReference, obj.GetConnectionRef())
	if rerr != nil {
		return res, rerr
	}
	obj.Status.ProjectID = new(proj.id)
	obj.Status.ProjectName = proj.name
	name := defaultName(obj.Spec.Name, obj.Name)
	want, version, rerr := r.desiredWebhook(ctx, obj, name, proj.id)
	if rerr != nil {
		return ctrl.Result{}, rerr
	}

	policy, rerr := r.locate(ctx, hc, obj, proj, name)
	if rerr != nil {
		return ctrl.Result{}, rerr
	}
	if policy == nil {
		id, err := hc.CreateWebhookPolicy(ctx, proj.id, want)
		if err != nil {
			return ctrl.Result{}, classifyHarborError(err, "create webhook policy")
		}
		if id == 0 {
			if policy, rerr = r.findByName(ctx, hc, proj.id, name); rerr != nil {
				return ctrl.Result{}, rerr
			}
			if policy == nil {
				return ctrl.Result{}, transient(harborv1alpha1.ReasonHarborError, nil, "webhook policy %q not found after create", name)
			}
			id = policy.ID
		}
		obj.Status.WebhookID = new(id)
		obj.Status.AuthSecretsVersion = version
		r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Created", "Create", "Created webhook policy %q (id %d) in project %q", name, id, proj.name)
	} else {
		obj.Status.WebhookID = new(policy.ID)
		if changed := webhookDrift(*policy, want, obj.Status.AuthSecretsVersion, version); len(changed) > 0 {
			want.ID = policy.ID
			if err := hc.UpdateWebhookPolicy(ctx, proj.id, policy.ID, want); err != nil {
				return ctrl.Result{}, classifyHarborError(err, "update webhook policy")
			}
			obj.Status.AuthSecretsVersion = version
			reason := changeReason("HarborWebhook", obj.Generation, obj.Status.ObservedGeneration, obj.Status.Conditions)
			r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, reason, "Update", "Updated webhook policy fields: %s", strings.Join(changed, ", "))
		}
	}
	setReady(&obj.Status.Conditions, obj.Generation, metav1.ConditionTrue, harborv1alpha1.ReasonSynced,
		fmt.Sprintf("webhook policy %q (id %d) in project %q matches spec", name, *obj.Status.WebhookID, proj.name))
	return ctrl.Result{}, nil
}

func (r *HarborWebhookReconciler) findByName(ctx context.Context, hc *harbor.Client, projectID int64, name string) (*harbor.WebhookPolicy, *reconcileError) {
	list, err := hc.ListWebhookPolicies(ctx, projectID)
	if err != nil {
		return nil, classifyHarborError(err, "list webhook policies")
	}
	for i := range list {
		if list[i].Name == name {
			return &list[i], nil
		}
	}
	return nil, nil
}

// locate finds the policy by stored ID, then by name.
func (r *HarborWebhookReconciler) locate(ctx context.Context, hc *harbor.Client, obj *harborv1alpha1.HarborWebhook,
	proj *resolvedProject, name string) (*harbor.WebhookPolicy, *reconcileError) {
	if obj.Status.WebhookID != nil {
		p, err := hc.GetWebhookPolicy(ctx, proj.id, *obj.Status.WebhookID)
		switch {
		case err == nil:
			return p, nil
		case harbor.IsNotFound(err):
			r.Recorder.Eventf(obj, nil, corev1.EventTypeWarning, harborv1alpha1.ReasonExternalResourceMissing, "Recreate",
				"Webhook policy %d disappeared from project %q; recreating", *obj.Status.WebhookID, proj.name)
			obj.Status.WebhookID = nil
			obj.Status.AuthSecretsVersion = ""
		default:
			return nil, classifyHarborError(err, "get webhook policy")
		}
	}
	p, rerr := r.findByName(ctx, hc, proj.id, name)
	if rerr != nil || p == nil {
		return nil, rerr
	}
	if !obj.Spec.AdoptExisting {
		return nil, terminal(harborv1alpha1.ReasonAlreadyExists,
			"webhook policy %q already exists in project %q (id %d); set spec.adoptExisting to manage it", name, proj.name, p.ID)
	}
	r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Adopted", "Adopt", "Adopted existing webhook policy %q (id %d)", name, p.ID)
	obj.Status.AuthSecretsVersion = "adopted"
	return p, nil
}

func (r *HarborWebhookReconciler) hasExternal(obj *harborv1alpha1.HarborWebhook) bool {
	return obj.Status.WebhookID != nil && obj.Status.ProjectID != nil
}

func (r *HarborWebhookReconciler) remove(ctx context.Context, obj *harborv1alpha1.HarborWebhook, hc *harbor.Client) error {
	return hc.DeleteWebhookPolicy(ctx, *obj.Status.ProjectID, *obj.Status.WebhookID)
}

// enqueueWebhooksForSecret maps a Secret event to the webhooks whose targets reference it.
func enqueueWebhooksForSecret(c client.Client) handler.MapFunc {
	return func(ctx context.Context, sec client.Object) []reconcile.Request {
		var list harborv1alpha1.HarborWebhookList
		if err := c.List(ctx, &list, client.InNamespace(sec.GetNamespace())); err != nil {
			return nil
		}
		var out []reconcile.Request
		for i := range list.Items {
			for _, t := range list.Items[i].Spec.Targets {
				if t.AuthHeaderSecretRef != nil && t.AuthHeaderSecretRef.Name == sec.GetName() {
					out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
					break
				}
			}
		}
		return out
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *HarborWebhookReconciler) SetupWithManager(mgr ctrl.Manager) error {
	newList := func() client.ObjectList { return &harborv1alpha1.HarborWebhookList{} }
	return ctrl.NewControllerManagedBy(mgr).
		For(&harborv1alpha1.HarborWebhook{}).
		Watches(&harborv1alpha1.HarborConnection{}, handler.EnqueueRequestsFromMapFunc(enqueueForConnection(mgr.GetClient(), newList))).
		Watches(&harborv1alpha1.HarborProject{}, handler.EnqueueRequestsFromMapFunc(enqueueForProject(mgr.GetClient(), newList))).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(enqueueWebhooksForSecret(mgr.GetClient()))).
		Named("harborwebhook").
		Complete(r)
}
