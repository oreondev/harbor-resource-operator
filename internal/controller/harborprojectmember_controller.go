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

// HarborProjectMemberReconciler reconciles a HarborProjectMember object
type HarborProjectMemberReconciler struct {
	client.Client
	// APIReader reads the reconciled object uncached; see getFresh.
	APIReader client.Reader
	Scheme    *runtime.Scheme
	Recorder  events.EventRecorder
}

// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborprojectmembers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborprojectmembers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=harbor.oreonit.com,resources=harborprojectmembers/finalizers,verbs=update

var memberRoleIDs = map[harborv1alpha1.ProjectRole]int64{
	harborv1alpha1.RoleProjectAdmin: harbor.RoleProjectAdmin,
	harborv1alpha1.RoleMaintainer:   harbor.RoleMaintainer,
	harborv1alpha1.RoleDeveloper:    harbor.RoleDeveloper,
	harborv1alpha1.RoleGuest:        harbor.RoleGuest,
	harborv1alpha1.RoleLimitedGuest: harbor.RoleLimitedGuest,
}

var groupTypeIDs = map[harborv1alpha1.GroupType]int64{
	harborv1alpha1.GroupTypeLDAP: harbor.GroupTypeLDAP,
	harborv1alpha1.GroupTypeHTTP: harbor.GroupTypeHTTP,
	harborv1alpha1.GroupTypeOIDC: harbor.GroupTypeOIDC,
}

// Reconcile drives a Harbor project membership to the declared state.
func (r *HarborProjectMemberReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return reconcileResource(ctx, r.Client, r.APIReader, r.Recorder, req, &harborv1alpha1.HarborProjectMember{}, "project member", r)
}

// memberIdentity returns Harbor's entity type ("u" or "g") and name.
func memberIdentity(obj *harborv1alpha1.HarborProjectMember) (string, string) {
	if obj.Spec.User != nil {
		return "u", *obj.Spec.User
	}
	return "g", obj.Spec.Group.Name
}

func (r *HarborProjectMemberReconciler) sync(ctx context.Context, obj *harborv1alpha1.HarborProjectMember,
	hc *harbor.Client, _ *harborv1alpha1.HarborConnection) (ctrl.Result, *reconcileError) {
	proj, res, rerr := resolveProject(ctx, r.Client, hc, obj.Namespace, obj.Spec.ProjectReference, obj.GetConnectionRef())
	if rerr != nil {
		return res, rerr
	}
	obj.Status.ProjectID = new(proj.id)
	obj.Status.ProjectName = proj.name
	roleID := memberRoleIDs[obj.Spec.Role]
	entityType, entityName := memberIdentity(obj)
	what := map[string]string{"u": "user", "g": "group"}[entityType]

	member, rerr := r.locate(ctx, hc, obj, proj, entityType, entityName)
	if rerr != nil {
		return ctrl.Result{}, rerr
	}
	if member == nil {
		req := harbor.ProjectMemberReq{RoleID: roleID}
		if obj.Spec.User != nil {
			req.MemberUser = &harbor.MemberUser{Username: *obj.Spec.User}
		} else {
			g := obj.Spec.Group
			req.MemberGroup = &harbor.MemberGroup{GroupName: g.Name, GroupType: groupTypeIDs[g.Type], LDAPGroupDN: g.LDAPGroupDN}
		}
		id, err := hc.CreateProjectMember(ctx, proj.id, req)
		if obj.Spec.User != nil && harbor.IsNotFound(err) {
			// Harbor answers 404 for an unknown user. The next sync retries,
			// so the member is added once the user exists (e.g. after a first
			// OIDC or LDAP login).
			return ctrl.Result{}, terminal(harborv1alpha1.ReasonUserNotFound,
				"user %q does not exist in Harbor; it is added to project %q once the user exists", entityName, proj.name)
		}
		if err != nil {
			return ctrl.Result{}, classifyHarborError(err, fmt.Sprintf("add %s %q to project %q", what, entityName, proj.name))
		}
		m, err := hc.GetProjectMember(ctx, proj.id, id)
		if err != nil {
			return ctrl.Result{}, classifyHarborError(err, "read member after create")
		}
		member = m
		r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Created", "Create",
			"Added %s %q to project %q as %s", what, entityName, proj.name, obj.Spec.Role)
	}
	obj.Status.MemberID = new(member.ID)

	if member.RoleID != roleID {
		if err := hc.UpdateProjectMemberRole(ctx, proj.id, member.ID, roleID); err != nil {
			return ctrl.Result{}, classifyHarborError(err, "update member role")
		}
		reason := changeReason("HarborProjectMember", obj.Generation, obj.Status.ObservedGeneration, obj.Status.Conditions)
		r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, reason, "Update", "Set role of %s %q to %s", what, entityName, obj.Spec.Role)
	}
	setReady(&obj.Status.Conditions, obj.Generation, metav1.ConditionTrue, harborv1alpha1.ReasonSynced,
		fmt.Sprintf("%s %q is %s in project %q", what, entityName, obj.Spec.Role, proj.name))
	return ctrl.Result{}, nil
}

// locate finds the membership by stored ID, then by entity name.
func (r *HarborProjectMemberReconciler) locate(ctx context.Context, hc *harbor.Client, obj *harborv1alpha1.HarborProjectMember,
	proj *resolvedProject, entityType, entityName string) (*harbor.ProjectMemberEntity, *reconcileError) {
	if obj.Status.MemberID != nil && obj.Status.ProjectID != nil && *obj.Status.ProjectID == proj.id {
		m, err := hc.GetProjectMember(ctx, proj.id, *obj.Status.MemberID)
		switch {
		case err == nil && m.EntityType == entityType && strings.EqualFold(m.EntityName, entityName):
			return m, nil
		case err == nil, harbor.IsNotFound(err):
			r.Recorder.Eventf(obj, nil, corev1.EventTypeWarning, harborv1alpha1.ReasonExternalResourceMissing, "Recreate",
				"Member %d disappeared from project %q; adding it again", *obj.Status.MemberID, proj.name)
			obj.Status.MemberID = nil
		default:
			return nil, classifyHarborError(err, "get project member")
		}
	}
	list, err := hc.ListProjectMembers(ctx, proj.id, entityName)
	if err != nil {
		return nil, classifyHarborError(err, "list project members")
	}
	for i := range list {
		m := &list[i]
		if m.EntityType != entityType || !strings.EqualFold(m.EntityName, entityName) {
			continue
		}
		if !obj.Spec.AdoptExisting {
			return nil, terminal(harborv1alpha1.ReasonAlreadyExists,
				"%q is already a member of project %q (member id %d); set spec.adoptExisting to manage it", entityName, proj.name, m.ID)
		}
		r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Adopted", "Adopt",
			"Adopted existing membership of %q in project %q (member id %d)", entityName, proj.name, m.ID)
		return m, nil
	}
	return nil, nil
}

func (r *HarborProjectMemberReconciler) hasExternal(obj *harborv1alpha1.HarborProjectMember) bool {
	return obj.Status.MemberID != nil && obj.Status.ProjectID != nil
}

func (r *HarborProjectMemberReconciler) remove(ctx context.Context, obj *harborv1alpha1.HarborProjectMember, hc *harbor.Client) error {
	return hc.DeleteProjectMember(ctx, *obj.Status.ProjectID, *obj.Status.MemberID)
}

// SetupWithManager sets up the controller with the Manager.
func (r *HarborProjectMemberReconciler) SetupWithManager(mgr ctrl.Manager) error {
	newList := func() client.ObjectList { return &harborv1alpha1.HarborProjectMemberList{} }
	return ctrl.NewControllerManagedBy(mgr).
		For(&harborv1alpha1.HarborProjectMember{}).
		Watches(&harborv1alpha1.HarborConnection{}, handler.EnqueueRequestsFromMapFunc(enqueueForConnection(mgr.GetClient(), newList))).
		Watches(&harborv1alpha1.HarborProject{}, handler.EnqueueRequestsFromMapFunc(enqueueForProject(mgr.GetClient(), newList))).
		Named("harborprojectmember").
		Complete(r)
}
