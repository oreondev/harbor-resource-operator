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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	harborv1alpha1 "github.com/oreondev/harbor-resource-operator/api/v1alpha1"
)

// HarborConnectionReconciler verifies that a Harbor instance is reachable
// with the given credentials and publishes its version.
type HarborConnectionReconciler struct {
	client.Client
	// APIReader reads the reconciled object uncached; see getFresh.
	APIReader client.Reader
	Scheme    *runtime.Scheme
	Recorder  events.EventRecorder
}

// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborconnections,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborconnections/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborconnections/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile checks the connection and records version, user and Ready.
func (r *HarborConnectionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	conn := &harborv1alpha1.HarborConnection{}
	if err := getFresh(ctx, r.Client, r.APIReader, req.NamespacedName, conn); err != nil {
		if apierrors.IsNotFound(err) {
			forgetConnection(req.String())
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	base := conn.DeepCopy()
	if !conn.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	period := syncPeriodFor(conn, nil)
	result := ctrl.Result{RequeueAfter: period}

	hc, rerr := clientForConnection(ctx, r.Client, conn)
	if rerr == nil {
		info, err := hc.SystemInfo(ctx)
		if err != nil {
			rerr = classifyHarborError(err, "GET /systeminfo")
		} else {
			conn.Status.HarborVersion = info.HarborVersion
			user, uerr := hc.CurrentUser(ctx)
			if uerr != nil {
				rerr = classifyHarborError(uerr, "GET /users/current")
			} else {
				conn.Status.Username = user.Username
			}
		}
	}

	now := metav1.Now()
	conn.Status.LastCheckTime = &now
	conn.Status.ObservedGeneration = conn.Generation
	if rerr != nil {
		wasReady := isReady(conn.Status.Conditions)
		setReady(&conn.Status.Conditions, conn.Generation, metav1.ConditionFalse, rerr.reason, rerr.message)
		if wasReady {
			r.Recorder.Eventf(conn, nil, corev1.EventTypeWarning, rerr.reason, "Connect", "%s", rerr.message)
		}
		log.Info("Harbor connection check failed", "reason", rerr.reason, "message", rerr.message)
		if rerr.retry {
			result = ctrl.Result{RequeueAfter: min(period, errorRetryAfter)}
		}
	} else {
		if !isReady(conn.Status.Conditions) {
			r.Recorder.Eventf(conn, nil, corev1.EventTypeNormal, harborv1alpha1.ReasonConnected, "Connect",
				"Connected to Harbor %s as %s", conn.Status.HarborVersion, conn.Status.Username)
		}
		setReady(&conn.Status.Conditions, conn.Generation, metav1.ConditionTrue, harborv1alpha1.ReasonConnected,
			fmt.Sprintf("Harbor %s reachable as %s", conn.Status.HarborVersion, conn.Status.Username))
	}

	return patchStatus(ctx, r.Client, conn, base, result)
}

// SetupWithManager sets up the controller with the Manager.
func (r *HarborConnectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&harborv1alpha1.HarborConnection{}).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(enqueueConnectionsForSecret(mgr.GetClient()))).
		Named("harborconnection").
		Complete(r)
}

// enqueueConnectionsForSecret maps a Secret to the connections in its
// namespace that read credentials or a CA bundle from it, so a rotation takes
// effect at once instead of on the next sync.
func enqueueConnectionsForSecret(c client.Client) handler.MapFunc {
	return func(ctx context.Context, sec client.Object) []reconcile.Request {
		var list harborv1alpha1.HarborConnectionList
		if err := c.List(ctx, &list, client.InNamespace(sec.GetNamespace())); err != nil {
			return nil
		}
		var out []reconcile.Request
		for i := range list.Items {
			spec := list.Items[i].Spec
			if spec.CredentialsSecretRef.Name == sec.GetName() ||
				(spec.CABundleSecretRef != nil && spec.CABundleSecretRef.Name == sec.GetName()) {
				out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
			}
		}
		return out
	}
}
