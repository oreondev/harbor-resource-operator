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
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// ProjectRole is a Harbor project role.
// +kubebuilder:validation:Enum=projectAdmin;maintainer;developer;guest;limitedGuest
type ProjectRole string

const (
	RoleProjectAdmin ProjectRole = "projectAdmin"
	RoleMaintainer   ProjectRole = "maintainer"
	RoleDeveloper    ProjectRole = "developer"
	RoleGuest        ProjectRole = "guest"
	RoleLimitedGuest ProjectRole = "limitedGuest"
)

// GroupType is the kind of user group in Harbor.
// +kubebuilder:validation:Enum=ldap;http;oidc
type GroupType string

const (
	GroupTypeLDAP GroupType = "ldap"
	GroupTypeHTTP GroupType = "http"
	GroupTypeOIDC GroupType = "oidc"
)

// MemberGroup names a user group.
// +kubebuilder:validation:XValidation:rule="self.type != 'ldap' || has(self.ldapGroupDN)",message="ldap groups need ldapGroupDN"
type MemberGroup struct {
	// name of the group as Harbor shows it.
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// type of the group: ldap, http or oidc.
	// +required
	Type GroupType `json:"type"`

	// ldapGroupDN is the distinguished name of an LDAP group.
	// +optional
	LDAPGroupDN string `json:"ldapGroupDN,omitempty"`
}

// HarborProjectMemberSpec grants a user or group a role in a project.
// +kubebuilder:validation:XValidation:rule="has(self.user) != has(self.group)",message="set exactly one of user or group"
// +kubebuilder:validation:XValidation:rule="has(self.projectRef) != has(self.projectName)",message="set exactly one of projectRef or projectName"
// +kubebuilder:validation:XValidation:rule="has(self.user) ? (has(oldSelf.user) && self.user == oldSelf.user) : (has(oldSelf.group) && self.group.name == oldSelf.group.name && self.group.type == oldSelf.group.type)",message="the member (user or group) cannot change; create a new object instead"
// +kubebuilder:validation:XValidation:rule="has(self.projectRef) == has(oldSelf.projectRef) && (!has(self.projectRef) || self.projectRef.name == oldSelf.projectRef.name) && has(self.projectName) == has(oldSelf.projectName) && (!has(self.projectName) || self.projectName == oldSelf.projectName)",message="the project cannot change; create a new object instead"
type HarborProjectMemberSpec struct {
	// connectionRef selects the Harbor instance.
	// +required
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Harbor Connection"
	ConnectionRef ConnectionRef `json:"connectionRef"`

	ProjectReference `json:",inline"`

	// user is the Harbor username. With LDAP or OIDC authentication the user
	// must have logged in to Harbor at least once.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="User"
	User *string `json:"user,omitempty"`

	// group is a user group.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Group"
	Group *MemberGroup `json:"group,omitempty"`

	// role granted in the project.
	// +required
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Role"
	Role ProjectRole `json:"role"`

	// adoptExisting takes over an existing membership of the same user or group.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Adopt Existing"
	AdoptExisting bool `json:"adoptExisting,omitempty"`

	// deletionPolicy controls whether the membership is removed with this object.
	// +optional
	// +kubebuilder:default=Delete
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Deletion Policy"
	DeletionPolicy DeletionPolicy `json:"deletionPolicy,omitempty"`

	// syncPeriod overrides the connection's drift-correction interval.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Sync Period"
	SyncPeriod *metav1.Duration `json:"syncPeriod,omitempty"`
}

// HarborProjectMemberStatus is the observed state of a HarborProjectMember.
type HarborProjectMemberStatus struct {
	// memberID in Harbor.
	// +optional
	MemberID *int64 `json:"memberID,omitempty"`

	// projectID of the project the member belongs to.
	// +optional
	ProjectID *int64 `json:"projectID,omitempty"`

	// projectName of the project the member belongs to.
	// +optional
	ProjectName string `json:"projectName,omitempty"`

	// observedGeneration is the spec generation the status reflects.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions: Ready=True when the membership matches the spec.
	// +listType=map
	// +listMapKey=type
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=status,displayName="Conditions",xDescriptors="urn:alm:descriptor:io.kubernetes.conditions"
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hmember
// +kubebuilder:printcolumn:name="Project",type=string,JSONPath=".status.projectName"
// +kubebuilder:printcolumn:name="User",type=string,JSONPath=".spec.user"
// +kubebuilder:printcolumn:name="Group",type=string,JSONPath=".spec.group.name"
// +kubebuilder:printcolumn:name="Role",type=string,JSONPath=".spec.role"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
// +operator-sdk:csv:customresourcedefinitions:displayName="Harbor Project Member",resources={{HarborConnection,v1alpha1,""},{HarborProject,v1alpha1,""}}

// HarborProjectMember grants a user or group a role in a Harbor project.
type HarborProjectMember struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of HarborProjectMember
	// +required
	Spec HarborProjectMemberSpec `json:"spec"`

	// status defines the observed state of HarborProjectMember
	// +optional
	Status HarborProjectMemberStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// HarborProjectMemberList contains a list of HarborProjectMember
type HarborProjectMemberList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []HarborProjectMember `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &HarborProjectMember{}, &HarborProjectMemberList{})
		return nil
	})
}
