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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	harborv1alpha1 "github.com/oreondev/harbor-resource-operator/api/v1alpha1"
	"github.com/oreondev/harbor-resource-operator/pkg/harbor"
)

// harborResource is implemented by the kinds driven by reconcileResource.
type harborResource interface {
	managedObject
	StatusConditions() *[]metav1.Condition
	SetObservedGeneration(int64)
	GetSyncPeriod() *metav1.Duration
	GetDeletionPolicy() harborv1alpha1.DeletionPolicy
}

// resourceHandler holds the kind-specific part of a reconcile.
type resourceHandler[T harborResource] interface {
	// sync makes Harbor match obj and sets Ready=True on success. A zero
	// result means "resync after the sync period".
	sync(ctx context.Context, obj T, hc *harbor.Client, conn *harborv1alpha1.HarborConnection) (ctrl.Result, *reconcileError)
	// hasExternal reports whether obj points at an object in Harbor.
	hasExternal(obj T) bool
	// remove deletes the Harbor object. A 404 must count as success.
	remove(ctx context.Context, obj T, hc *harbor.Client) error
}

// reconcileResource runs the shared reconcile flow: finalizer, connection,
// kind-specific sync, deletion policy and status patch. Status and finalizer
// writes use Patch with MergeFrom, never Update.
func reconcileResource[T harborResource](ctx context.Context, c client.Client, reader client.Reader, rec events.EventRecorder,
	req ctrl.Request, obj T, kind string, h resourceHandler[T]) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	if err := getFresh(ctx, c, reader, req.NamespacedName, obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	base := obj.DeepCopyObject().(T)

	if !obj.GetDeletionTimestamp().IsZero() {
		return deleteResource(ctx, c, rec, obj, base, kind, h)
	}
	if !controllerutil.ContainsFinalizer(obj, harborv1alpha1.Finalizer) {
		controllerutil.AddFinalizer(obj, harborv1alpha1.Finalizer)
		if err := c.Patch(ctx, obj, client.MergeFrom(base)); err != nil {
			return ctrl.Result{}, err
		}
		base = obj.DeepCopyObject().(T)
	}

	conn, hc, rerr := connectAndClient(ctx, c, obj.GetConnectionRef(), obj.GetNamespace())
	var result ctrl.Result
	if rerr == nil {
		result, rerr = h.sync(ctx, obj, hc, conn)
	}
	obj.SetObservedGeneration(obj.GetGeneration())
	period := syncPeriodFor(conn, obj.GetSyncPeriod())
	if rerr != nil {
		setReady(obj.StatusConditions(), obj.GetGeneration(), metav1.ConditionFalse, rerr.reason, rerr.message)
		log.Info(kind+" not ready", "reason", rerr.reason, "message", rerr.message)
		switch {
		case result.RequeueAfter > 0:
		case rerr.retry:
			result = ctrl.Result{RequeueAfter: errorRetryAfter}
		default:
			result = ctrl.Result{RequeueAfter: period}
		}
	} else if result.RequeueAfter == 0 {
		result = ctrl.Result{RequeueAfter: period}
	}
	return patchStatus(ctx, c, obj, base, result)
}

// deleteResource applies the deletion policy and releases the finalizer.
func deleteResource[T harborResource](ctx context.Context, c client.Client, rec events.EventRecorder,
	obj, base T, kind string, h resourceHandler[T]) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(obj, harborv1alpha1.Finalizer) {
		return ctrl.Result{}, nil
	}
	if obj.GetDeletionPolicy() == harborv1alpha1.DeletionPolicyDelete && h.hasExternal(obj) {
		_, hc, rerr := connectAndClient(ctx, c, obj.GetConnectionRef(), obj.GetNamespace())
		switch {
		case rerr != nil && rerr.reason == harborv1alpha1.ReasonConnectionNotFound:
			rec.Eventf(obj, nil, corev1.EventTypeWarning, rerr.reason, "Delete",
				"Connection missing; the %s is left in Harbor", kind)
		case rerr != nil:
			setReady(obj.StatusConditions(), obj.GetGeneration(), metav1.ConditionFalse, rerr.reason, rerr.message)
			_ = c.Status().Patch(ctx, obj, client.MergeFrom(base))
			return ctrl.Result{RequeueAfter: errorRetryAfter}, nil
		default:
			if err := h.remove(ctx, obj, hc); err != nil && !harbor.IsNotFound(err) {
				re := classifyHarborError(err, "delete "+kind)
				rec.Eventf(obj, nil, corev1.EventTypeWarning, re.reason, "Delete", "%s", re.message)
				setReady(obj.StatusConditions(), obj.GetGeneration(), metav1.ConditionFalse, re.reason, re.message)
				_ = c.Status().Patch(ctx, obj, client.MergeFrom(base))
				return ctrl.Result{RequeueAfter: errorRetryAfter}, nil
			}
			rec.Eventf(obj, nil, corev1.EventTypeNormal, "Deleted", "Delete", "Deleted the %s from Harbor", kind)
		}
	}
	controllerutil.RemoveFinalizer(obj, harborv1alpha1.Finalizer)
	return ctrl.Result{}, c.Patch(ctx, obj, client.MergeFrom(base))
}

// resolvedProject is the Harbor project a project-scoped resource belongs to.
type resolvedProject struct {
	name string
	id   int64
}

// resolveProject finds the project of a project-scoped resource: a Ready
// HarborProject on the same connection, or an existing Harbor project by name.
func resolveProject(ctx context.Context, c client.Client, hc *harbor.Client, ns string,
	ref harborv1alpha1.ProjectReference, connRef harborv1alpha1.ConnectionRef) (*resolvedProject, ctrl.Result, *reconcileError) {
	wait := ctrl.Result{RequeueAfter: dependencyRetryAfter}
	if ref.ProjectRef == nil {
		if ref.ProjectName == "" {
			return nil, ctrl.Result{}, terminal(harborv1alpha1.ReasonInvalidSpec, "set spec.projectRef or spec.projectName")
		}
		p, err := hc.GetProjectByName(ctx, ref.ProjectName)
		switch {
		case harbor.IsNotFound(err):
			return nil, wait, &reconcileError{reason: harborv1alpha1.ReasonDependencyNotReady,
				message: fmt.Sprintf("project %q does not exist in Harbor", ref.ProjectName)}
		case err != nil:
			return nil, ctrl.Result{}, classifyHarborError(err, "look up project")
		}
		return &resolvedProject{name: p.Name, id: p.ProjectID}, ctrl.Result{}, nil
	}
	key := types.NamespacedName{Name: ref.ProjectRef.Name, Namespace: ns}
	proj := &harborv1alpha1.HarborProject{}
	if err := c.Get(ctx, key, proj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, wait, &reconcileError{reason: harborv1alpha1.ReasonDependencyNotReady,
				message: fmt.Sprintf("HarborProject %s not found", key)}
		}
		return nil, ctrl.Result{}, transient(harborv1alpha1.ReasonDependencyNotReady, err, "get HarborProject %s", key)
	}
	if proj.GetConnectionRef() != connRef {
		return nil, ctrl.Result{}, terminal(harborv1alpha1.ReasonInvalidSpec, "HarborProject %s uses a different HarborConnection", key)
	}
	if !isReady(proj.Status.Conditions) || proj.Status.ProjectID == nil {
		return nil, wait, &reconcileError{reason: harborv1alpha1.ReasonDependencyNotReady,
			message: fmt.Sprintf("HarborProject %s is not Ready", key)}
	}
	return &resolvedProject{name: proj.Status.ProjectName, id: *proj.Status.ProjectID}, ctrl.Result{}, nil
}

// projectScoped is implemented by kinds that belong to a project.
type projectScoped interface {
	client.Object
	GetProjectReference() harborv1alpha1.ProjectReference
}

// enqueueForConnection maps a HarborConnection event to every object of the
// listed kind that uses it.
func enqueueForConnection(c client.Client, newList func() client.ObjectList) handler.MapFunc {
	return func(ctx context.Context, conn client.Object) []reconcile.Request {
		want := harborv1alpha1.ConnectionRef{Name: conn.GetName(), Namespace: conn.GetNamespace()}
		return listRequests(ctx, c, newList(), nil, func(o client.Object) bool {
			m, ok := o.(managedObject)
			return ok && m.GetConnectionRef() == want
		})
	}
}

// enqueueForProject maps a HarborProject event to the objects of the listed
// kind in the same namespace that reference it.
func enqueueForProject(c client.Client, newList func() client.ObjectList) handler.MapFunc {
	return func(ctx context.Context, proj client.Object) []reconcile.Request {
		return listRequests(ctx, c, newList(), []client.ListOption{client.InNamespace(proj.GetNamespace())}, func(o client.Object) bool {
			p, ok := o.(projectScoped)
			return ok && p.GetProjectReference().ProjectRef != nil && p.GetProjectReference().ProjectRef.Name == proj.GetName()
		})
	}
}

func listRequests(ctx context.Context, c client.Client, list client.ObjectList, opts []client.ListOption,
	match func(client.Object) bool) []reconcile.Request {
	if err := c.List(ctx, list, opts...); err != nil {
		return nil
	}
	objs, err := meta.ExtractList(list)
	if err != nil {
		return nil
	}
	var out []reconcile.Request
	for _, o := range objs {
		co, ok := o.(client.Object)
		if ok && match(co) {
			out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(co)})
		}
	}
	return out
}
