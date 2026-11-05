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

// HarborRegistryReconciler reconciles a HarborRegistry object
type HarborRegistryReconciler struct {
	client.Client
	// APIReader reads the reconciled object uncached; see getFresh.
	APIReader client.Reader
	Scheme    *runtime.Scheme
	Recorder  events.EventRecorder
}

// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborregistries,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborregistries/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborregistries/finalizers,verbs=update

// Reconcile drives a Harbor replication endpoint to the declared state.
func (r *HarborRegistryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	obj := &harborv1alpha1.HarborRegistry{}
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
		log.Info("HarborRegistry not ready", "reason", rerr.reason, "message", rerr.message)
		if rerr.retry {
			result = ctrl.Result{RequeueAfter: errorRetryAfter}
		} else {
			result = ctrl.Result{RequeueAfter: syncPeriodFor(conn, obj.Spec.SyncPeriod)}
		}
	}
	return patchStatus(ctx, r.Client, obj, base, result)
}

type registryCreds struct {
	accessKey, accessSecret, version string
}

func (r *HarborRegistryReconciler) loadCreds(ctx context.Context, obj *harborv1alpha1.HarborRegistry) (*registryCreds, *reconcileError) {
	if obj.Spec.CredentialsSecretRef == nil {
		return nil, nil
	}
	sec := &corev1.Secret{}
	key := types.NamespacedName{Name: obj.Spec.CredentialsSecretRef.Name, Namespace: obj.Namespace}
	if err := r.Get(ctx, key, sec); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, &reconcileError{reason: harborv1alpha1.ReasonSecretMissing, message: fmt.Sprintf("credentials Secret %s not found", key), retry: true}
		}
		return nil, transient(harborv1alpha1.ReasonSecretMissing, err, "get credentials Secret %s", key)
	}
	c := &registryCreds{accessKey: string(sec.Data["accessKey"]), accessSecret: string(sec.Data["accessSecret"]), version: sec.ResourceVersion}
	if c.accessKey == "" && c.accessSecret == "" {
		return nil, terminal(harborv1alpha1.ReasonSecretMissing, "credentials Secret %s needs keys accessKey and accessSecret", key)
	}
	return c, nil
}

func (r *HarborRegistryReconciler) reconcileNormal(ctx context.Context, obj *harborv1alpha1.HarborRegistry) (*harborv1alpha1.HarborConnection, ctrl.Result, *reconcileError) {
	conn, hc, rerr := connectAndClient(ctx, r.Client, obj.Spec.ConnectionRef, obj.Namespace)
	if rerr != nil {
		return conn, ctrl.Result{}, rerr
	}
	period := syncPeriodFor(conn, obj.Spec.SyncPeriod)
	name := defaultName(obj.Spec.Name, obj.Name)

	creds, rerr := r.loadCreds(ctx, obj)
	if rerr != nil {
		return conn, ctrl.Result{}, rerr
	}

	desired := harbor.Registry{
		Name:        name,
		Type:        obj.Spec.Type,
		URL:         strings.TrimRight(obj.Spec.URL, "/"),
		Insecure:    obj.Spec.Insecure,
		Description: obj.Spec.Description,
	}
	if creds != nil {
		credType := obj.Spec.CredentialType
		if credType == "" {
			credType = "basic"
		}
		desired.Credential = &harbor.RegistryCredential{Type: credType, AccessKey: creds.accessKey, AccessSecret: creds.accessSecret}
	}

	// Locate.
	var reg *harbor.Registry
	if obj.Status.RegistryID != nil {
		got, err := hc.GetRegistry(ctx, *obj.Status.RegistryID)
		switch {
		case err == nil:
			reg = got
		case harbor.IsNotFound(err):
			r.Recorder.Eventf(obj, nil, corev1.EventTypeWarning, harborv1alpha1.ReasonExternalResourceMissing, "Recreate",
				"Registry %d disappeared from Harbor; recreating", *obj.Status.RegistryID)
			obj.Status.RegistryID = nil
			obj.Status.CredentialsSecretVersion = ""
		default:
			return conn, ctrl.Result{}, classifyHarborError(err, "get registry")
		}
	}
	if reg == nil {
		list, err := hc.ListRegistries(ctx, name)
		if err != nil {
			return conn, ctrl.Result{}, classifyHarborError(err, "list registries")
		}
		for i := range list {
			if list[i].Name == name {
				if !obj.Spec.AdoptExisting {
					return conn, ctrl.Result{}, terminal(harborv1alpha1.ReasonAlreadyExists,
						"registry %q already exists in Harbor (id %d); set spec.adoptExisting to manage it", name, list[i].ID)
				}
				reg = &list[i]
				r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Adopted", "Adopt", "Adopted existing registry %q (id %d)", name, reg.ID)
				break
			}
		}
	}

	if reg == nil {
		if err := hc.PingRegistry(ctx, desired); err != nil {
			obj.Status.Health = "unhealthy"
			if !obj.Spec.CreateEvenIfUnhealthy {
				return conn, ctrl.Result{}, &reconcileError{reason: harborv1alpha1.ReasonPingFailed,
					message: fmt.Sprintf("Harbor could not reach %s: %v (set spec.createEvenIfUnhealthy to create anyway)", desired.URL, err), retry: true}
			}
			r.Recorder.Eventf(obj, nil, corev1.EventTypeWarning, harborv1alpha1.ReasonPingFailed, "Create", "Ping failed, creating anyway: %v", err)
		}
		id, err := hc.CreateRegistry(ctx, desired)
		if err != nil && !harbor.IsConflict(err) {
			return conn, ctrl.Result{}, classifyHarborError(err, "create registry")
		}
		if id == 0 {
			list, lerr := hc.ListRegistries(ctx, name)
			if lerr != nil {
				return conn, ctrl.Result{}, classifyHarborError(lerr, "list registries after create")
			}
			for i := range list {
				if list[i].Name == name {
					id = list[i].ID
				}
			}
			if id == 0 {
				return conn, ctrl.Result{}, transient(harborv1alpha1.ReasonHarborError, nil, "registry %q not found after create", name)
			}
		}
		got, err := hc.GetRegistry(ctx, id)
		if err != nil {
			return conn, ctrl.Result{}, classifyHarborError(err, "read registry after create")
		}
		reg = got
		if creds != nil {
			obj.Status.CredentialsSecretVersion = creds.version
		}
		r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Created", "Create", "Created registry %q (id %d)", name, reg.ID)
	}
	obj.Status.RegistryID = new(reg.ID)

	// Type cannot change in Harbor.
	if !strings.EqualFold(reg.Type, obj.Spec.Type) {
		return conn, ctrl.Result{}, terminal(harborv1alpha1.ReasonImmutableField,
			"registry %q has type %q in Harbor but spec.type is %q; Harbor cannot change it", name, reg.Type, obj.Spec.Type)
	}

	// Drift.
	var changed []string
	upd := harbor.RegistryUpdate{}
	if reg.Name != desired.Name {
		upd.Name = new(desired.Name)
		changed = append(changed, "name")
	}
	if strings.TrimRight(reg.URL, "/") != desired.URL {
		upd.URL = new(desired.URL)
		changed = append(changed, "url")
	}
	if reg.Insecure != desired.Insecure {
		upd.Insecure = new(desired.Insecure)
		changed = append(changed, "insecure")
	}
	if reg.Description != desired.Description {
		upd.Description = new(desired.Description)
		changed = append(changed, "description")
	}
	if creds != nil {
		gotKey, gotType := "", ""
		if reg.Credential != nil {
			gotKey, gotType = reg.Credential.AccessKey, reg.Credential.Type
		}
		if gotKey != creds.accessKey || !strings.EqualFold(gotType, desired.Credential.Type) || obj.Status.CredentialsSecretVersion != creds.version {
			upd.CredentialType = new(desired.Credential.Type)
			upd.AccessKey = new(creds.accessKey)
			upd.AccessSecret = new(creds.accessSecret)
			changed = append(changed, "credentials")
		}
	}
	if len(changed) > 0 {
		if err := hc.UpdateRegistry(ctx, reg.ID, upd); err != nil {
			return conn, ctrl.Result{}, classifyHarborError(err, "update registry")
		}
		if creds != nil {
			obj.Status.CredentialsSecretVersion = creds.version
		}
		reason := changeReason("HarborRegistry", obj.Generation, obj.Status.ObservedGeneration, obj.Status.Conditions)
		r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, reason, "Update", "Updated registry fields: %s", strings.Join(changed, ", "))
		got, err := hc.GetRegistry(ctx, reg.ID)
		if err == nil {
			reg = got
		}
	}
	obj.Status.Health = reg.Status

	setReady(&obj.Status.Conditions, obj.Generation, metav1.ConditionTrue, harborv1alpha1.ReasonSynced,
		fmt.Sprintf("registry %q (id %d) matches spec", reg.Name, reg.ID))
	return conn, ctrl.Result{RequeueAfter: period}, nil
}

func (r *HarborRegistryReconciler) reconcileDelete(ctx context.Context, obj *harborv1alpha1.HarborRegistry, base *harborv1alpha1.HarborRegistry) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(obj, harborv1alpha1.Finalizer) {
		return ctrl.Result{}, nil
	}
	if obj.Spec.DeletionPolicy != harborv1alpha1.DeletionPolicyRetain && obj.Status.RegistryID != nil {
		_, hc, rerr := connectAndClient(ctx, r.Client, obj.Spec.ConnectionRef, obj.Namespace)
		if rerr != nil && rerr.reason != harborv1alpha1.ReasonConnectionNotFound {
			setReady(&obj.Status.Conditions, obj.Generation, metav1.ConditionFalse, rerr.reason, rerr.message)
			_ = r.Status().Patch(ctx, obj, client.MergeFrom(base))
			return ctrl.Result{RequeueAfter: errorRetryAfter}, nil
		}
		if rerr == nil {
			if err := hc.DeleteRegistry(ctx, *obj.Status.RegistryID); err != nil && !harbor.IsNotFound(err) {
				msg := fmt.Sprintf("Delete registry %d: %v", *obj.Status.RegistryID, err)
				r.Recorder.Eventf(obj, nil, corev1.EventTypeWarning, harborv1alpha1.ReasonHarborError, "Delete", "%s", msg)
				setReady(&obj.Status.Conditions, obj.Generation, metav1.ConditionFalse, harborv1alpha1.ReasonHarborError, msg)
				_ = r.Status().Patch(ctx, obj, client.MergeFrom(base))
				return ctrl.Result{RequeueAfter: errorRetryAfter}, nil
			}
			r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Deleted", "Delete", "Deleted registry %d from Harbor", *obj.Status.RegistryID)
		}
	}
	controllerutil.RemoveFinalizer(obj, harborv1alpha1.Finalizer)
	return ctrl.Result{}, r.Patch(ctx, obj, client.MergeFrom(base))
}

// SetupWithManager sets up the controller with the Manager.
func (r *HarborRegistryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&harborv1alpha1.HarborRegistry{}).
		Watches(&harborv1alpha1.HarborConnection{}, handler.EnqueueRequestsFromMapFunc(enqueueRegistriesForConnection(mgr.GetClient()))).
		Named("harborregistry").
		Complete(r)
}
