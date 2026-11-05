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
	"strings"

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

// HarborLabelReconciler reconciles a HarborLabel object
type HarborLabelReconciler struct {
	client.Client
	// APIReader reads the reconciled object uncached; see getFresh.
	APIReader client.Reader
	Scheme    *runtime.Scheme
	Recorder  events.EventRecorder
}

// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborlabels,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborlabels/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborlabels/finalizers,verbs=update

// Reconcile drives a Harbor label to the declared state.
func (r *HarborLabelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return reconcileResource(ctx, r.Client, r.APIReader, r.Recorder, req, &harborv1alpha1.HarborLabel{}, "label", r)
}

func (r *HarborLabelReconciler) sync(ctx context.Context, obj *harborv1alpha1.HarborLabel,
	hc *harbor.Client, _ *harborv1alpha1.HarborConnection) (ctrl.Result, *reconcileError) {
	name := defaultName(obj.Spec.Name, obj.Name)
	scope, projectID := harbor.LabelScopeGlobal, int64(0)
	where := "global"
	if obj.Spec.Scope != harborv1alpha1.LabelScopeGlobal {
		proj, res, rerr := resolveProject(ctx, r.Client, hc, obj.Namespace, obj.Spec.ProjectReference, obj.GetConnectionRef())
		if rerr != nil {
			return res, rerr
		}
		scope, projectID = harbor.LabelScopeProject, proj.id
		obj.Status.ProjectID = new(proj.id)
		where = fmt.Sprintf("project %q", proj.name)
	}

	label, rerr := r.locate(ctx, hc, obj, name, scope, projectID)
	if rerr != nil {
		return ctrl.Result{}, rerr
	}
	if label == nil {
		want := harbor.Label{Name: name, Color: obj.Spec.Color, Description: deref(obj.Spec.Description), Scope: scope, ProjectID: projectID}
		id, err := hc.CreateLabel(ctx, want)
		if err != nil {
			return ctrl.Result{}, classifyHarborError(err, "create label")
		}
		if label, err = hc.GetLabel(ctx, id); err != nil {
			return ctrl.Result{}, classifyHarborError(err, "read label after create")
		}
		r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Created", "Create", "Created %s label %q (id %d)", where, name, id)
	}
	obj.Status.LabelID = new(label.ID)

	var changed []string
	upd := *label
	if label.Name != name {
		upd.Name = name
		changed = append(changed, "name")
	}
	if obj.Spec.Color != "" && !strings.EqualFold(label.Color, obj.Spec.Color) {
		upd.Color = obj.Spec.Color
		changed = append(changed, "color")
	}
	if obj.Spec.Description != nil && label.Description != *obj.Spec.Description {
		upd.Description = *obj.Spec.Description
		changed = append(changed, "description")
	}
	if len(changed) > 0 {
		if err := hc.UpdateLabel(ctx, label.ID, upd); err != nil {
			return ctrl.Result{}, classifyHarborError(err, "update label")
		}
		reason := changeReason("HarborLabel", obj.Generation, obj.Status.ObservedGeneration, obj.Status.Conditions)
		r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, reason, "Update", "Updated label fields: %s", strings.Join(changed, ", "))
	}
	setReady(&obj.Status.Conditions, obj.Generation, metav1.ConditionTrue, harborv1alpha1.ReasonSynced,
		fmt.Sprintf("%s label %q (id %d) matches spec", where, name, label.ID))
	return ctrl.Result{}, nil
}

// locate finds the label by stored ID, then by exact name in its scope.
func (r *HarborLabelReconciler) locate(ctx context.Context, hc *harbor.Client, obj *harborv1alpha1.HarborLabel,
	name, scope string, projectID int64) (*harbor.Label, *reconcileError) {
	if obj.Status.LabelID != nil {
		l, err := hc.GetLabel(ctx, *obj.Status.LabelID)
		switch {
		case err == nil && l.Scope == scope && l.ProjectID == projectID:
			return l, nil
		case err == nil, harbor.IsNotFound(err):
			r.Recorder.Eventf(obj, nil, corev1.EventTypeWarning, harborv1alpha1.ReasonExternalResourceMissing, "Recreate",
				"Label %d disappeared from Harbor; recreating", *obj.Status.LabelID)
			obj.Status.LabelID = nil
		default:
			return nil, classifyHarborError(err, "get label")
		}
	}
	list, err := hc.ListLabels(ctx, scope, projectID, name)
	if err != nil {
		return nil, classifyHarborError(err, "list labels")
	}
	for i := range list {
		if list[i].Name != name {
			continue
		}
		if !obj.Spec.AdoptExisting {
			return nil, terminal(harborv1alpha1.ReasonAlreadyExists,
				"label %q already exists in Harbor (id %d); set spec.adoptExisting to manage it", name, list[i].ID)
		}
		r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Adopted", "Adopt", "Adopted existing label %q (id %d)", name, list[i].ID)
		return &list[i], nil
	}
	return nil, nil
}

func (r *HarborLabelReconciler) hasExternal(obj *harborv1alpha1.HarborLabel) bool {
	return obj.Status.LabelID != nil
}

func (r *HarborLabelReconciler) remove(ctx context.Context, obj *harborv1alpha1.HarborLabel, hc *harbor.Client) error {
	return hc.DeleteLabel(ctx, *obj.Status.LabelID)
}

// SetupWithManager sets up the controller with the Manager.
func (r *HarborLabelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	newList := func() client.ObjectList { return &harborv1alpha1.HarborLabelList{} }
	return ctrl.NewControllerManagedBy(mgr).
		For(&harborv1alpha1.HarborLabel{}).
		Watches(&harborv1alpha1.HarborConnection{}, handler.EnqueueRequestsFromMapFunc(enqueueForConnection(mgr.GetClient(), newList))).
		Watches(&harborv1alpha1.HarborProject{}, handler.EnqueueRequestsFromMapFunc(enqueueForProject(mgr.GetClient(), newList))).
		Named("harborlabel").
		Complete(r)
}
