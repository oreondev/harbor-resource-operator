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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

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

// HarborRobotAccountReconciler reconciles a HarborRobotAccount object
type HarborRobotAccountReconciler struct {
	client.Client
	// APIReader reads the reconciled object uncached; see getFresh.
	APIReader client.Reader
	Scheme    *runtime.Scheme
	Recorder  events.EventRecorder
}

// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborrobotaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborrobotaccounts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborrobotaccounts/finalizers,verbs=update
// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborprojects,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile drives a Harbor robot account and its Secret to the declared state.
func (r *HarborRobotAccountReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	obj := &harborv1alpha1.HarborRobotAccount{}
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
		log.Info("HarborRobotAccount not ready", "reason", rerr.reason, "message", rerr.message)
		if rerr.retry {
			result = ctrl.Result{RequeueAfter: errorRetryAfter}
		} else {
			result = ctrl.Result{RequeueAfter: syncPeriodFor(conn, obj.Spec.SyncPeriod)}
		}
	}
	return patchStatus(ctx, r.Client, obj, base, result)
}

// resolveProjectName returns the Harbor project a project-level robot belongs to.
func (r *HarborRobotAccountReconciler) resolveProjectName(ctx context.Context, obj *harborv1alpha1.HarborRobotAccount) (string, *reconcileError) {
	if obj.Spec.Level == harborv1alpha1.RobotLevelSystem {
		return "", nil
	}
	if obj.Spec.ProjectName != "" {
		return obj.Spec.ProjectName, nil
	}
	if obj.Spec.ProjectRef == nil {
		return "", terminal(harborv1alpha1.ReasonInvalidSpec, "project level robots need spec.projectRef or spec.projectName")
	}
	proj := &harborv1alpha1.HarborProject{}
	key := types.NamespacedName{Name: obj.Spec.ProjectRef.Name, Namespace: obj.Namespace}
	if err := r.Get(ctx, key, proj); err != nil {
		if apierrors.IsNotFound(err) {
			return "", &reconcileError{reason: harborv1alpha1.ReasonDependencyNotReady, message: fmt.Sprintf("HarborProject %s not found", key), retry: true}
		}
		return "", transient(harborv1alpha1.ReasonDependencyNotReady, err, "get HarborProject %s", key)
	}
	if !isReady(proj.Status.Conditions) || proj.Status.ProjectName == "" {
		return "", &reconcileError{reason: harborv1alpha1.ReasonDependencyNotReady, message: fmt.Sprintf("HarborProject %s is not Ready", key), retry: true}
	}
	if proj.Spec.ConnectionRef.Name != obj.Spec.ConnectionRef.Name ||
		defaultName(proj.Spec.ConnectionRef.Namespace, obj.Namespace) != defaultName(obj.Spec.ConnectionRef.Namespace, obj.Namespace) {
		return "", terminal(harborv1alpha1.ReasonInvalidSpec, "HarborProject %s uses a different HarborConnection", key)
	}
	return proj.Status.ProjectName, nil
}

// desiredPermissions converts spec permissions to Harbor's shape.
func desiredPermissions(obj *harborv1alpha1.HarborRobotAccount, projectName string) []harbor.RobotPermission {
	out := make([]harbor.RobotPermission, 0, len(obj.Spec.Permissions))
	for _, p := range obj.Spec.Permissions {
		ns := p.Namespace
		if obj.Spec.Level != harborv1alpha1.RobotLevelSystem {
			ns = projectName
		}
		acc := make([]harbor.Access, 0, len(p.Access))
		for _, a := range p.Access {
			eff := a.Effect
			if eff == "" {
				eff = "allow"
			}
			acc = append(acc, harbor.Access{Resource: a.Resource, Action: a.Action, Effect: eff})
		}
		out = append(out, harbor.RobotPermission{Kind: "project", Namespace: ns, Access: acc})
	}
	return out
}

// permissionsKey renders permissions in a canonical order for comparison.
func permissionsKey(perms []harbor.RobotPermission) string {
	parts := make([]string, 0, len(perms))
	for _, p := range perms {
		acc := make([]string, 0, len(p.Access))
		for _, a := range p.Access {
			eff := a.Effect
			if eff == "" {
				eff = "allow"
			}
			acc = append(acc, a.Resource+":"+a.Action+":"+eff)
		}
		slices.Sort(acc)
		parts = append(parts, p.Kind+"/"+p.Namespace+"["+strings.Join(acc, ",")+"]")
	}
	slices.Sort(parts)
	return strings.Join(parts, ";")
}

// matchesRobot reports whether a listed robot is the one the spec describes.
func matchesRobot(rb harbor.Robot, level harborv1alpha1.RobotLevel, short, projectName string) bool {
	if level == harborv1alpha1.RobotLevelSystem {
		return rb.Level == "system" && !strings.Contains(rb.Name, "+") && strings.HasSuffix(rb.Name, short) &&
			(len(rb.Name) == len(short) || !isNameChar(rb.Name[len(rb.Name)-len(short)-1]))
	}
	if rb.Level != "project" || !strings.HasSuffix(rb.Name, "+"+short) {
		return false
	}
	for _, p := range rb.Permissions {
		if p.Namespace == projectName {
			return true
		}
	}
	return false
}

func isNameChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-'
}

// locateRobot finds the robot by stored ID, then by name. When a robot of
// the same name exists and adoption is allowed, its secret is refreshed and
// returned, because Harbor never reveals an existing secret.
func (r *HarborRobotAccountReconciler) locateRobot(ctx context.Context, hc *harbor.Client, obj *harborv1alpha1.HarborRobotAccount,
	level harborv1alpha1.RobotLevel, short, projectName string) (*harbor.Robot, string, *reconcileError) {
	if obj.Status.RobotID != nil {
		got, err := hc.GetRobot(ctx, *obj.Status.RobotID)
		switch {
		case err == nil:
			return got, "", nil
		case harbor.IsNotFound(err):
			r.Recorder.Eventf(obj, nil, corev1.EventTypeWarning, harborv1alpha1.ReasonExternalResourceMissing, "Recreate",
				"Robot %d disappeared from Harbor; recreating", *obj.Status.RobotID)
			obj.Status.RobotID = nil
		default:
			return nil, "", classifyHarborError(err, "get robot")
		}
	}
	list, err := hc.ListRobots(ctx, "name=~"+short)
	if err != nil {
		return nil, "", classifyHarborError(err, "list robots")
	}
	for i := range list {
		if !matchesRobot(list[i], level, short, projectName) {
			continue
		}
		if !obj.Spec.AdoptExisting {
			return nil, "", terminal(harborv1alpha1.ReasonAlreadyExists,
				"robot %q already exists in Harbor (id %d); set spec.adoptExisting to manage it (its secret will be refreshed)", list[i].Name, list[i].ID)
		}
		sec, serr := hc.RefreshRobotSecret(ctx, list[i].ID)
		if serr != nil {
			return nil, "", classifyHarborError(serr, "refresh secret of adopted robot")
		}
		r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Adopted", "Adopt", "Adopted existing robot %q (id %d) and refreshed its secret", list[i].Name, list[i].ID)
		return &list[i], sec, nil
	}
	return nil, "", nil
}

// rotateIfExpiring deletes a robot that is within rotation.beforeExpiry of
// expiring, so that it is recreated with a fresh expiry and secret.
func (r *HarborRobotAccountReconciler) rotateIfExpiring(ctx context.Context, hc *harbor.Client, obj *harborv1alpha1.HarborRobotAccount, robot *harbor.Robot) (*harbor.Robot, *reconcileError) {
	if robot == nil || obj.Spec.Rotation == nil || obj.Spec.Rotation.BeforeExpiry == nil || robot.ExpiresAt <= 0 {
		return robot, nil
	}
	exp := time.Unix(robot.ExpiresAt, 0)
	if time.Until(exp) >= obj.Spec.Rotation.BeforeExpiry.Duration {
		return robot, nil
	}
	if err := hc.DeleteRobot(ctx, robot.ID); err != nil && !harbor.IsNotFound(err) {
		return robot, classifyHarborError(err, "delete expiring robot for rotation")
	}
	r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Rotated", "Rotate", "Recreated robot %q before expiry at %s", robot.Name, exp.Format(time.RFC3339))
	obj.Status.RobotID = nil
	return nil, nil
}

// createRobot creates the robot in Harbor and returns it with its one-time secret.
func (r *HarborRobotAccountReconciler) createRobot(ctx context.Context, hc *harbor.Client, obj *harborv1alpha1.HarborRobotAccount,
	level harborv1alpha1.RobotLevel, short string, duration int64, perms []harbor.RobotPermission) (*harbor.Robot, string, *reconcileError) {
	created, err := hc.CreateRobot(ctx, harbor.RobotCreate{
		Name:        short,
		Description: obj.Spec.Description,
		Level:       string(level),
		Disable:     obj.Spec.Disabled,
		Duration:    duration,
		Permissions: perms,
	})
	if err != nil {
		return nil, "", classifyHarborError(err, "create robot")
	}
	got, err := hc.GetRobot(ctx, created.ID)
	if err != nil {
		return nil, "", classifyHarborError(err, "read robot after create")
	}
	r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Created", "Create", "Created robot %q (id %d)", got.Name, got.ID)
	return got, created.Secret, nil
}

// updateIfDrifted pushes the editable fields when Harbor differs from spec.
func (r *HarborRobotAccountReconciler) updateIfDrifted(ctx context.Context, hc *harbor.Client, obj *harborv1alpha1.HarborRobotAccount,
	robot *harbor.Robot, wantPerms []harbor.RobotPermission, duration int64) *reconcileError {
	var changed []string
	if robot.Description != obj.Spec.Description {
		changed = append(changed, "description")
	}
	if robot.Disable != obj.Spec.Disabled {
		changed = append(changed, "disabled")
	}
	if permissionsKey(robot.Permissions) != permissionsKey(wantPerms) {
		changed = append(changed, "permissions")
	}
	if robot.Duration != nil && *robot.Duration != duration {
		changed = append(changed, "duration")
	}
	if len(changed) == 0 {
		return nil
	}
	upd := *robot
	upd.Description = obj.Spec.Description
	upd.Disable = obj.Spec.Disabled
	upd.Permissions = wantPerms
	upd.Duration = new(duration)
	if err := hc.UpdateRobot(ctx, robot.ID, upd); err != nil {
		return classifyHarborError(err, "update robot")
	}
	reason := changeReason("HarborRobotAccount", obj.Generation, obj.Status.ObservedGeneration, obj.Status.Conditions)
	r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, reason, "Update", "Updated robot fields: %s", strings.Join(changed, ", "))
	return nil
}

// ensureSecret makes sure the credentials Secret exists, is owned by obj and
// holds a password, refreshing the robot secret when it does not or when the
// rotation period has elapsed. It returns the Secret name and whether a new
// secret was written.
func (r *HarborRobotAccountReconciler) ensureSecret(ctx context.Context, hc *harbor.Client, obj *harborv1alpha1.HarborRobotAccount,
	robot *harbor.Robot, newSecret string) (string, bool, *reconcileError) {
	secretName := obj.Spec.Secret.Name
	if secretName == "" {
		secretName = obj.Name + "-robot"
	}
	secretKey := types.NamespacedName{Name: secretName, Namespace: obj.Namespace}
	rotated := newSecret != ""

	refresh := func(what string) *reconcileError {
		sec, err := hc.RefreshRobotSecret(ctx, robot.ID)
		if err != nil {
			return classifyHarborError(err, what)
		}
		newSecret, rotated = sec, true
		return nil
	}

	existing := &corev1.Secret{}
	err := r.Get(ctx, secretKey, existing)
	switch {
	case err == nil:
		if !metav1.IsControlledBy(existing, obj) {
			return secretName, false, terminal(harborv1alpha1.ReasonSecretConflict,
				"Secret %s exists and is not owned by this HarborRobotAccount", secretKey)
		}
		if newSecret == "" && !secretHasCredentials(existing) {
			if rerr := refresh("refresh robot secret"); rerr != nil {
				return secretName, false, rerr
			}
			r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "SecretRecreated", "WriteSecret", "Secret %s lacked credentials; refreshed robot secret", secretKey)
		}
	case apierrors.IsNotFound(err):
		if newSecret == "" {
			if rerr := refresh("refresh robot secret"); rerr != nil {
				return secretName, false, rerr
			}
			if obj.Status.SecretRef != nil {
				r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "SecretRecreated", "WriteSecret", "Secret %s was missing; refreshed robot secret", secretKey)
			}
		}
		existing = nil
	default:
		return secretName, false, transient(harborv1alpha1.ReasonHarborError, err, "get Secret %s", secretKey)
	}
	if newSecret == "" && obj.Spec.Rotation != nil && obj.Spec.Rotation.Period != nil && obj.Status.LastRotation != nil &&
		time.Since(obj.Status.LastRotation.Time) >= obj.Spec.Rotation.Period.Duration {
		if rerr := refresh("rotate robot secret"); rerr != nil {
			return secretName, false, rerr
		}
		r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Rotated", "Rotate", "Refreshed robot secret after rotation period")
	}

	if newSecret != "" {
		if err := r.writeSecret(ctx, obj, existing, secretName, robot.Name, newSecret, hc.Host(), obj.Status.ExpiresAt); err != nil {
			return secretName, false, transient(harborv1alpha1.ReasonHarborError, err, "write Secret %s", secretKey)
		}
	} else if existing != nil {
		if err := r.syncSecretMetadata(ctx, obj, existing, robot.Name, obj.Status.ExpiresAt); err != nil {
			return secretName, false, transient(harborv1alpha1.ReasonHarborError, err, "update Secret %s", secretKey)
		}
	}
	return secretName, rotated, nil
}

// nextRequeue shortens the resync so rotation deadlines are not missed.
func nextRequeue(obj *harborv1alpha1.HarborRobotAccount, period time.Duration) time.Duration {
	next := period
	if obj.Spec.Rotation != nil {
		if obj.Spec.Rotation.Period != nil && obj.Status.LastRotation != nil {
			if d := time.Until(obj.Status.LastRotation.Add(obj.Spec.Rotation.Period.Duration)); d > 0 && d < next {
				next = d
			}
		}
		if obj.Spec.Rotation.BeforeExpiry != nil && obj.Status.ExpiresAt != nil {
			if d := time.Until(obj.Status.ExpiresAt.Add(-obj.Spec.Rotation.BeforeExpiry.Duration)); d > 0 && d < next {
				next = d
			}
		}
	}
	return max(next, 10*time.Second)
}

func (r *HarborRobotAccountReconciler) reconcileNormal(ctx context.Context, obj *harborv1alpha1.HarborRobotAccount) (*harborv1alpha1.HarborConnection, ctrl.Result, *reconcileError) {
	conn, hc, rerr := connectAndClient(ctx, r.Client, obj.Spec.ConnectionRef, obj.Namespace)
	if rerr != nil {
		return conn, ctrl.Result{}, rerr
	}
	period := syncPeriodFor(conn, obj.Spec.SyncPeriod)
	short := defaultName(obj.Spec.Name, obj.Name)
	level := obj.Spec.Level
	if level == "" {
		level = harborv1alpha1.RobotLevelProject
	}

	projectName, rerr := r.resolveProjectName(ctx, obj)
	if rerr != nil {
		res := ctrl.Result{}
		if rerr.reason == harborv1alpha1.ReasonDependencyNotReady {
			res = ctrl.Result{RequeueAfter: dependencyRetryAfter}
		}
		return conn, res, rerr
	}
	if level == harborv1alpha1.RobotLevelSystem {
		for _, p := range obj.Spec.Permissions {
			if p.Namespace == "" {
				return conn, ctrl.Result{}, terminal(harborv1alpha1.ReasonInvalidSpec, "system level robots need a namespace on every permission (a project name or \"*\")")
			}
		}
	}
	wantPerms := desiredPermissions(obj, projectName)
	duration := obj.Spec.Duration
	if duration == 0 {
		duration = -1
	}

	robot, newSecret, rerr := r.locateRobot(ctx, hc, obj, level, short, projectName)
	if rerr != nil {
		return conn, ctrl.Result{}, rerr
	}
	if robot, rerr = r.rotateIfExpiring(ctx, hc, obj, robot); rerr != nil {
		return conn, ctrl.Result{}, rerr
	}
	if robot == nil {
		if robot, newSecret, rerr = r.createRobot(ctx, hc, obj, level, short, duration, wantPerms); rerr != nil {
			return conn, ctrl.Result{}, rerr
		}
	}
	obj.Status.RobotID = new(robot.ID)
	obj.Status.RobotName = robot.Name
	obj.Status.ExpiresAt = nil
	if robot.ExpiresAt > 0 {
		obj.Status.ExpiresAt = new(metav1.NewTime(time.Unix(robot.ExpiresAt, 0)))
	}

	if rerr := r.updateIfDrifted(ctx, hc, obj, robot, wantPerms, duration); rerr != nil {
		return conn, ctrl.Result{}, rerr
	}
	secretName, rotated, rerr := r.ensureSecret(ctx, hc, obj, robot, newSecret)
	if rerr != nil {
		return conn, ctrl.Result{}, rerr
	}
	if rotated {
		obj.Status.LastRotation = new(metav1.Now())
	}
	obj.Status.SecretRef = &harborv1alpha1.LocalObjectRef{Name: secretName}

	setReady(&obj.Status.Conditions, obj.Generation, metav1.ConditionTrue, harborv1alpha1.ReasonSynced,
		fmt.Sprintf("robot %q (id %d) matches spec; credentials in Secret %s", robot.Name, robot.ID, secretName))
	return conn, ctrl.Result{RequeueAfter: nextRequeue(obj, period)}, nil
}

func secretHasCredentials(s *corev1.Secret) bool {
	if s.Type == corev1.SecretTypeDockerConfigJson {
		return len(s.Data[corev1.DockerConfigJsonKey]) > 0
	}
	return len(s.Data["password"]) > 0 && len(s.Data["username"]) > 0
}

func (r *HarborRobotAccountReconciler) secretMeta(obj *harborv1alpha1.HarborRobotAccount, s *corev1.Secret, robotName string, expires *metav1.Time) {
	if s.Labels == nil {
		s.Labels = map[string]string{}
	}
	maps.Copy(s.Labels, obj.Spec.Secret.Labels)
	s.Labels["app.kubernetes.io/managed-by"] = "harbor-resource-operator"
	if s.Annotations == nil {
		s.Annotations = map[string]string{}
	}
	maps.Copy(s.Annotations, obj.Spec.Secret.Annotations)
	s.Annotations["harbor.oreonit.com/robot-name"] = robotName
	if expires != nil {
		s.Annotations["harbor.oreonit.com/expires-at"] = expires.UTC().Format(time.RFC3339)
	} else {
		delete(s.Annotations, "harbor.oreonit.com/expires-at")
	}
}

func (r *HarborRobotAccountReconciler) writeSecret(ctx context.Context, obj *harborv1alpha1.HarborRobotAccount, existing *corev1.Secret,
	name, robotName, password, host string, expires *metav1.Time) error {
	secType := corev1.SecretType(obj.Spec.Secret.Type)
	if secType == "" {
		secType = corev1.SecretTypeOpaque
	}
	data := map[string][]byte{}
	if secType == corev1.SecretTypeDockerConfigJson {
		cfg := map[string]any{"auths": map[string]any{host: map[string]string{
			"username": robotName,
			"password": password,
			"auth":     base64.StdEncoding.EncodeToString([]byte(robotName + ":" + password)),
		}}}
		raw, err := json.Marshal(cfg)
		if err != nil {
			return err
		}
		data[corev1.DockerConfigJsonKey] = raw
	} else {
		data["username"] = []byte(robotName)
		data["password"] = []byte(password)
		data["host"] = []byte(host)
	}
	if existing != nil && existing.Type != secType {
		// Secret type is immutable; recreate.
		if err := r.Delete(ctx, existing); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		existing = nil
	}
	if existing == nil {
		s := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: obj.Namespace}, Type: secType, Data: data}
		r.secretMeta(obj, s, robotName, expires)
		if err := controllerutil.SetControllerReference(obj, s, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, s)
	}
	existing.Data = data
	r.secretMeta(obj, existing, robotName, expires)
	return r.Update(ctx, existing)
}

func (r *HarborRobotAccountReconciler) syncSecretMetadata(ctx context.Context, obj *harborv1alpha1.HarborRobotAccount, s *corev1.Secret, robotName string, expires *metav1.Time) error {
	before := s.DeepCopy()
	r.secretMeta(obj, s, robotName, expires)
	if equalStringMaps(before.Labels, s.Labels) && equalStringMaps(before.Annotations, s.Annotations) {
		return nil
	}
	return r.Update(ctx, s)
}

func equalStringMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func (r *HarborRobotAccountReconciler) reconcileDelete(ctx context.Context, obj *harborv1alpha1.HarborRobotAccount, base *harborv1alpha1.HarborRobotAccount) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(obj, harborv1alpha1.Finalizer) {
		return ctrl.Result{}, nil
	}
	if obj.Spec.DeletionPolicy != harborv1alpha1.DeletionPolicyRetain && obj.Status.RobotID != nil {
		_, hc, rerr := connectAndClient(ctx, r.Client, obj.Spec.ConnectionRef, obj.Namespace)
		if rerr != nil && rerr.reason != harborv1alpha1.ReasonConnectionNotFound {
			setReady(&obj.Status.Conditions, obj.Generation, metav1.ConditionFalse, rerr.reason, rerr.message)
			_ = r.Status().Patch(ctx, obj, client.MergeFrom(base))
			return ctrl.Result{RequeueAfter: errorRetryAfter}, nil
		}
		if rerr == nil {
			if err := hc.DeleteRobot(ctx, *obj.Status.RobotID); err != nil && !harbor.IsNotFound(err) {
				msg := fmt.Sprintf("Delete robot %d: %v", *obj.Status.RobotID, err)
				r.Recorder.Eventf(obj, nil, corev1.EventTypeWarning, harborv1alpha1.ReasonHarborError, "Delete", "%s", msg)
				setReady(&obj.Status.Conditions, obj.Generation, metav1.ConditionFalse, harborv1alpha1.ReasonHarborError, msg)
				_ = r.Status().Patch(ctx, obj, client.MergeFrom(base))
				return ctrl.Result{RequeueAfter: errorRetryAfter}, nil
			}
			r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Deleted", "Delete", "Deleted robot %d from Harbor", *obj.Status.RobotID)
		}
	}
	// The Secret is garbage collected through its owner reference.
	controllerutil.RemoveFinalizer(obj, harborv1alpha1.Finalizer)
	return ctrl.Result{}, r.Patch(ctx, obj, client.MergeFrom(base))
}

// SetupWithManager sets up the controller with the Manager.
func (r *HarborRobotAccountReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&harborv1alpha1.HarborRobotAccount{}).
		Owns(&corev1.Secret{}).
		Watches(&harborv1alpha1.HarborProject{}, handler.EnqueueRequestsFromMapFunc(enqueueRobotsForProject(mgr.GetClient()))).
		Watches(&harborv1alpha1.HarborConnection{}, handler.EnqueueRequestsFromMapFunc(enqueueRobotsForConnection(mgr.GetClient()))).
		Named("harborrobotaccount").
		Complete(r)
}
