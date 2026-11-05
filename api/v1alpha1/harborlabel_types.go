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

// LabelScope says whether a label is global or belongs to one project.
// +kubebuilder:validation:Enum=global;project
type LabelScope string

const (
	LabelScopeGlobal  LabelScope = "global"
	LabelScopeProject LabelScope = "project"
)

// HarborLabelSpec defines a Harbor label.
// +kubebuilder:validation:XValidation:rule="self.scope != 'project' || (has(self.projectRef) != has(self.projectName))",message="project labels need exactly one of projectRef or projectName"
// +kubebuilder:validation:XValidation:rule="self.scope != 'global' || (!has(self.projectRef) && !has(self.projectName))",message="global labels take no project"
// +kubebuilder:validation:XValidation:rule="self.scope == oldSelf.scope",message="scope is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.projectRef) == has(oldSelf.projectRef) && (!has(self.projectRef) || self.projectRef.name == oldSelf.projectRef.name) && has(self.projectName) == has(oldSelf.projectName) && (!has(self.projectName) || self.projectName == oldSelf.projectName)",message="the project cannot change; create a new object instead"
type HarborLabelSpec struct {
	// connectionRef selects the Harbor instance.
	// +required
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Harbor Connection"
	ConnectionRef ConnectionRef `json:"connectionRef"`

	// name of the label in Harbor. Defaults to metadata.name.
	// +optional
	// +kubebuilder:validation:MaxLength=128
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Name"
	Name string `json:"name,omitempty"`

	// scope is project (default) or global. Global labels need a system administrator connection.
	// +optional
	// +kubebuilder:default=project
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Scope"
	Scope LabelScope `json:"scope,omitempty"`

	ProjectReference `json:",inline"`

	// color as #RRGGBB. Unset leaves the color as Harbor has it.
	// +optional
	// +kubebuilder:validation:Pattern=`^#[0-9A-Fa-f]{6}$`
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Color"
	Color string `json:"color,omitempty"`

	// description shown in Harbor. Unset leaves it as Harbor has it.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Description"
	Description *string `json:"description,omitempty"`

	// adoptExisting takes over a label of the same name in the same scope.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Adopt Existing"
	AdoptExisting bool `json:"adoptExisting,omitempty"`

	// deletionPolicy controls whether the label is deleted with this object.
	// Deleting a label removes it from every artifact that carries it.
	// +optional
	// +kubebuilder:default=Delete
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Deletion Policy"
	DeletionPolicy DeletionPolicy `json:"deletionPolicy,omitempty"`

	// syncPeriod overrides the connection's drift-correction interval.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Sync Period"
	SyncPeriod *metav1.Duration `json:"syncPeriod,omitempty"`
}

// HarborLabelStatus is the observed state of a HarborLabel.
type HarborLabelStatus struct {
	// labelID in Harbor.
	// +optional
	LabelID *int64 `json:"labelID,omitempty"`

	// projectID for project labels.
	// +optional
	ProjectID *int64 `json:"projectID,omitempty"`

	// observedGeneration is the spec generation the status reflects.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions: Ready=True when the label matches the spec.
	// +listType=map
	// +listMapKey=type
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=status,displayName="Conditions",xDescriptors="urn:alm:descriptor:io.kubernetes.conditions"
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hlabel
// +kubebuilder:printcolumn:name="Scope",type=string,JSONPath=".spec.scope"
// +kubebuilder:printcolumn:name="ID",type=integer,JSONPath=".status.labelID"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
// +operator-sdk:csv:customresourcedefinitions:displayName="Harbor Label",resources={{HarborConnection,v1alpha1,""},{HarborProject,v1alpha1,""}}

// HarborLabel manages a global or project label in Harbor.
type HarborLabel struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of HarborLabel
	// +required
	Spec HarborLabelSpec `json:"spec"`

	// status defines the observed state of HarborLabel
	// +optional
	Status HarborLabelStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// HarborLabelList contains a list of HarborLabel
type HarborLabelList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []HarborLabel `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &HarborLabel{}, &HarborLabelList{})
		return nil
	})
}
