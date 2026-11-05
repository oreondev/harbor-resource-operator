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

// HarborProjectReconciler reconciles a HarborProject object
type HarborProjectReconciler struct {
	client.Client
	// APIReader reads the reconciled object uncached; see getFresh.
	APIReader client.Reader
	Scheme    *runtime.Scheme
	Recorder  events.EventRecorder
}

// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborprojects,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborprojects/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborprojects/finalizers,verbs=update
// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborconnections,verbs=get;list;watch
// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborregistries,verbs=get;list;watch

// Reconcile drives a Harbor project to the declared state.
func (r *HarborProjectReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	obj := &harborv1alpha1.HarborProject{}
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
		log.Info("HarborProject not ready", "reason", rerr.reason, "message", rerr.message)
		if rerr.retry {
			result = ctrl.Result{RequeueAfter: errorRetryAfter}
		} else {
			result = ctrl.Result{RequeueAfter: syncPeriodFor(conn, obj.Spec.SyncPeriod)}
		}
	}
	return patchStatus(ctx, r.Client, obj, base, result)
}

func (r *HarborProjectReconciler) reconcileNormal(ctx context.Context, obj *harborv1alpha1.HarborProject) (*harborv1alpha1.HarborConnection, ctrl.Result, *reconcileError) {
	conn, hc, rerr := connectAndClient(ctx, r.Client, obj.Spec.ConnectionRef, obj.Namespace)
	if rerr != nil {
		return conn, ctrl.Result{}, rerr
	}
	period := syncPeriodFor(conn, obj.Spec.SyncPeriod)
	name := defaultName(obj.Spec.Name, obj.Name)

	// Proxy cache upstream must be a Ready HarborRegistry on the same connection.
	var registryID *int64
	if obj.Spec.ProxyCache != nil {
		reg := &harborv1alpha1.HarborRegistry{}
		key := types.NamespacedName{Name: obj.Spec.ProxyCache.RegistryRef.Name, Namespace: obj.Namespace}
		if err := r.Get(ctx, key, reg); err != nil {
			if apierrors.IsNotFound(err) {
				return conn, ctrl.Result{RequeueAfter: dependencyRetryAfter}, &reconcileError{reason: harborv1alpha1.ReasonDependencyNotReady,
					message: fmt.Sprintf("HarborRegistry %s not found", key)}
			}
			return conn, ctrl.Result{}, transient(harborv1alpha1.ReasonDependencyNotReady, err, "get HarborRegistry %s", key)
		}
		if !isReady(reg.Status.Conditions) || reg.Status.RegistryID == nil {
			return conn, ctrl.Result{RequeueAfter: dependencyRetryAfter}, &reconcileError{reason: harborv1alpha1.ReasonDependencyNotReady,
				message: fmt.Sprintf("HarborRegistry %s is not Ready", key)}
		}
		registryID = reg.Status.RegistryID
	}

	// Locate the project: by stored ID, then by name.
	var project *harbor.Project
	if obj.Status.ProjectID != nil {
		p, err := hc.GetProject(ctx, *obj.Status.ProjectID)
		switch {
		case err == nil:
			project = p
		case harbor.IsNotFound(err):
			r.Recorder.Eventf(obj, nil, corev1.EventTypeWarning, harborv1alpha1.ReasonExternalResourceMissing, "Recreate",
				"Project %d disappeared from Harbor; recreating", *obj.Status.ProjectID)
			obj.Status.ProjectID = nil
		default:
			return conn, ctrl.Result{}, classifyHarborError(err, "get project")
		}
	}
	if project == nil {
		p, err := hc.GetProjectByName(ctx, name)
		switch {
		case err == nil:
			if !obj.Spec.AdoptExisting {
				return conn, ctrl.Result{}, terminal(harborv1alpha1.ReasonAlreadyExists,
					"project %q already exists in Harbor (id %d); set spec.adoptExisting to manage it", name, p.ProjectID)
			}
			project = p
			r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Adopted", "Adopt", "Adopted existing project %q (id %d)", name, p.ProjectID)
		case harbor.IsNotFound(err):
			// create below
		default:
			return conn, ctrl.Result{}, classifyHarborError(err, "look up project by name")
		}
	}

	if project == nil {
		req := r.desiredProjectReq(obj, name, true)
		req.RegistryID = registryID
		if _, err := hc.CreateProject(ctx, req); err != nil && !harbor.IsConflict(err) {
			return conn, ctrl.Result{}, classifyHarborError(err, "create project")
		}
		p, err := hc.GetProjectByName(ctx, name)
		if err != nil {
			return conn, ctrl.Result{}, classifyHarborError(err, "read project after create")
		}
		project = p
		r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Created", "Create", "Created project %q (id %d)", name, p.ProjectID)
	}
	obj.Status.ProjectID = new(project.ProjectID)
	obj.Status.ProjectName = project.Name

	// Immutable: proxy cache upstream.
	if registryID != nil && project.RegistryID != *registryID {
		return conn, ctrl.Result{}, terminal(harborv1alpha1.ReasonImmutableField,
			"project %q has registry_id %d in Harbor but spec.proxyCache points at %d; Harbor cannot change it", name, project.RegistryID, *registryID)
	}
	if registryID == nil && obj.Spec.ProxyCache == nil && project.RegistryID != 0 {
		// Spec does not mention proxy cache; leave it alone (managed fields rule).
		_ = project.RegistryID
	}

	// Drift on metadata and allowlist.
	changed := projectDrift(obj, project)
	if len(changed) > 0 {
		req := r.desiredProjectReq(obj, name, false)
		if err := hc.UpdateProject(ctx, project.ProjectID, req); err != nil {
			return conn, ctrl.Result{}, classifyHarborError(err, "update project")
		}
		r.recordChange(obj, "project", changed)
	}

	// Quota.
	if obj.Spec.StorageQuotaBytes != nil {
		q, err := hc.GetProjectQuota(ctx, project.ProjectID)
		if err != nil {
			return conn, ctrl.Result{}, classifyHarborError(err, "get project quota")
		}
		if q != nil && q.Hard["storage"] != *obj.Spec.StorageQuotaBytes {
			if err := hc.UpdateQuota(ctx, q.ID, map[string]int64{"storage": *obj.Spec.StorageQuotaBytes}); err != nil {
				return conn, ctrl.Result{}, classifyHarborError(err, "update project quota")
			}
			r.recordChange(obj, "project", []string{"storageQuotaBytes"})
		}
	}

	setReady(&obj.Status.Conditions, obj.Generation, metav1.ConditionTrue, harborv1alpha1.ReasonSynced,
		fmt.Sprintf("project %q (id %d) matches spec", project.Name, project.ProjectID))
	return conn, ctrl.Result{RequeueAfter: period}, nil
}

// desiredProjectReq builds the request body from spec. On create, Harbor
// needs project_name and accepts storage_limit; on update only the managed
// metadata and allowlist are sent.
func (r *HarborProjectReconciler) desiredProjectReq(obj *harborv1alpha1.HarborProject, name string, create bool) harbor.ProjectReq {
	req := harbor.ProjectReq{}
	if create {
		req.ProjectName = name
		if obj.Spec.StorageQuotaBytes != nil {
			req.StorageLimit = obj.Spec.StorageQuotaBytes
		}
	}
	md := &harbor.ProjectMetadata{}
	has := false
	if obj.Spec.Public != nil {
		req.Public = obj.Spec.Public
		md.Public = harbor.BoolString(*obj.Spec.Public)
		has = true
	}
	if m := obj.Spec.Metadata; m != nil {
		if m.AutoScan != nil {
			md.AutoScan = harbor.BoolString(*m.AutoScan)
			has = true
		}
		if m.AutoSBOMGeneration != nil {
			md.AutoSBOMGeneration = harbor.BoolString(*m.AutoSBOMGeneration)
			has = true
		}
		if m.PreventVulnerable != nil {
			md.PreventVul = harbor.BoolString(*m.PreventVulnerable)
			has = true
		}
		if m.Severity != nil {
			md.Severity = *m.Severity
			has = true
		}
		if m.EnableContentTrustCosign != nil {
			md.EnableContentTrustCosign = harbor.BoolString(*m.EnableContentTrustCosign)
			has = true
		}
		if m.ReuseSystemCVEAllowlist != nil {
			md.ReuseSysCVEAllowlist = harbor.BoolString(*m.ReuseSystemCVEAllowlist)
			has = true
		}
	}
	if has {
		req.Metadata = md
	}
	if al := obj.Spec.CVEAllowlist; al != nil {
		items := make([]harbor.CVEAllowlistItem, 0, len(al.Items))
		for _, id := range al.Items {
			items = append(items, harbor.CVEAllowlistItem{CVEID: id})
		}
		req.CVEAllowlist = &harbor.CVEAllowlist{Items: items}
		if al.ExpiresAt != nil {
			req.CVEAllowlist.ExpiresAt = new(al.ExpiresAt.Unix())
		}
	}
	return req
}

// projectDrift lists the spec fields whose value differs in Harbor.
func projectDrift(obj *harborv1alpha1.HarborProject, p *harbor.Project) []string {
	var changed []string
	md := p.Metadata
	if md == nil {
		md = &harbor.ProjectMetadata{}
	}
	cmpBool := func(field string, want *bool, got string) {
		if want != nil && harbor.BoolString(*want) != strings.ToLower(got) {
			changed = append(changed, field)
		}
	}
	cmpBool("public", obj.Spec.Public, md.Public)
	if m := obj.Spec.Metadata; m != nil {
		cmpBool("metadata.autoScan", m.AutoScan, md.AutoScan)
		cmpBool("metadata.autoSbomGeneration", m.AutoSBOMGeneration, md.AutoSBOMGeneration)
		cmpBool("metadata.preventVulnerable", m.PreventVulnerable, md.PreventVul)
		cmpBool("metadata.enableContentTrustCosign", m.EnableContentTrustCosign, md.EnableContentTrustCosign)
		cmpBool("metadata.reuseSystemCVEAllowlist", m.ReuseSystemCVEAllowlist, md.ReuseSysCVEAllowlist)
		if m.Severity != nil && !strings.EqualFold(*m.Severity, md.Severity) {
			changed = append(changed, "metadata.severity")
		}
	}
	if al := obj.Spec.CVEAllowlist; al != nil {
		var got []string
		var gotExp *int64
		if p.CVEAllowlist != nil {
			for _, it := range p.CVEAllowlist.Items {
				got = append(got, it.CVEID)
			}
			gotExp = p.CVEAllowlist.ExpiresAt
		}
		want := slices.Clone(al.Items)
		slices.Sort(want)
		slices.Sort(got)
		if !slices.Equal(want, got) {
			changed = append(changed, "cveAllowlist.items")
		}
		var wantExp *int64
		if al.ExpiresAt != nil {
			wantExp = new(al.ExpiresAt.Unix())
		}
		if (wantExp == nil) != (gotExp == nil || *gotExp == 0) || (wantExp != nil && gotExp != nil && *wantExp != *gotExp) {
			changed = append(changed, "cveAllowlist.expiresAt")
		}
	}
	return changed
}

// recordChange emits Updated or DriftCorrected depending on whether the spec changed.
func (r *HarborProjectReconciler) recordChange(obj *harborv1alpha1.HarborProject, what string, fields []string) {
	reason := changeReason("HarborProject", obj.Generation, obj.Status.ObservedGeneration, obj.Status.Conditions)
	r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, reason, "Update", "Updated %s fields: %s", what, strings.Join(fields, ", "))
}

func (r *HarborProjectReconciler) reconcileDelete(ctx context.Context, obj *harborv1alpha1.HarborProject, base *harborv1alpha1.HarborProject) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(obj, harborv1alpha1.Finalizer) {
		return ctrl.Result{}, nil
	}
	if obj.Spec.DeletionPolicy == harborv1alpha1.DeletionPolicyDelete && obj.Status.ProjectID != nil {
		_, hc, rerr := connectAndClient(ctx, r.Client, obj.Spec.ConnectionRef, obj.Namespace)
		if rerr != nil {
			if rerr.reason == harborv1alpha1.ReasonConnectionNotFound {
				// Connection gone: nothing we can do in Harbor; release the object.
				r.Recorder.Eventf(obj, nil, corev1.EventTypeWarning, rerr.reason, "Delete", "Connection missing; Harbor project left in place")
			} else {
				setReady(&obj.Status.Conditions, obj.Generation, metav1.ConditionFalse, rerr.reason, rerr.message)
				_ = r.Status().Patch(ctx, obj, client.MergeFrom(base))
				return ctrl.Result{RequeueAfter: errorRetryAfter}, nil
			}
		} else {
			id := *obj.Status.ProjectID
			d, err := hc.ProjectDeletable(ctx, id)
			switch {
			case err != nil && harbor.IsNotFound(err):
				// already gone
			case err != nil:
				return ctrl.Result{RequeueAfter: errorRetryAfter}, nil
			case !d.Deletable:
				msg := fmt.Sprintf("Harbor refuses to delete project %d: %s", id, d.Message)
				r.Recorder.Eventf(obj, nil, corev1.EventTypeWarning, harborv1alpha1.ReasonProjectNotEmpty, "Delete", "%s", msg)
				setReady(&obj.Status.Conditions, obj.Generation, metav1.ConditionFalse, harborv1alpha1.ReasonProjectNotEmpty, msg)
				_ = r.Status().Patch(ctx, obj, client.MergeFrom(base))
				return ctrl.Result{RequeueAfter: syncPeriodFor(nil, obj.Spec.SyncPeriod)}, nil
			default:
				if err := hc.DeleteProject(ctx, id); err != nil && !harbor.IsNotFound(err) {
					r.Recorder.Eventf(obj, nil, corev1.EventTypeWarning, harborv1alpha1.ReasonHarborError, "Delete", "Delete project %d: %v", id, err)
					return ctrl.Result{RequeueAfter: errorRetryAfter}, nil
				}
				r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Deleted", "Delete", "Deleted project %d from Harbor", id)
			}
		}
	}
	controllerutil.RemoveFinalizer(obj, harborv1alpha1.Finalizer)
	return ctrl.Result{}, r.Patch(ctx, obj, client.MergeFrom(base))
}

// SetupWithManager sets up the controller with the Manager.
func (r *HarborProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&harborv1alpha1.HarborProject{}).
		Watches(&harborv1alpha1.HarborConnection{}, handler.EnqueueRequestsFromMapFunc(enqueueProjectsForConnection(mgr.GetClient()))).
		Watches(&harborv1alpha1.HarborRegistry{}, handler.EnqueueRequestsFromMapFunc(enqueueProjectsForRegistry(mgr.GetClient()))).
		Named("harborproject").
		Complete(r)
}
