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

// HarborImmutableRuleSpec makes matching tags in a project immutable: they
// can be neither overwritten nor deleted.
// +kubebuilder:validation:XValidation:rule="has(self.projectRef) != has(self.projectName)",message="set exactly one of projectRef or projectName"
// +kubebuilder:validation:XValidation:rule="has(self.projectRef) == has(oldSelf.projectRef) && (!has(self.projectRef) || self.projectRef.name == oldSelf.projectRef.name) && has(self.projectName) == has(oldSelf.projectName) && (!has(self.projectName) || self.projectName == oldSelf.projectName)",message="the project cannot change; create a new object instead"
type HarborImmutableRuleSpec struct {
	// connectionRef selects the Harbor instance.
	// +required
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Harbor Connection"
	ConnectionRef ConnectionRef `json:"connectionRef"`

	ProjectReference `json:",inline"`

	// repositories selects the repositories the rule applies to. Defaults to all.
	// +optional
	// +kubebuilder:default={}
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Repositories"
	Repositories PatternSelector `json:"repositories,omitzero"`

	// tags selects the tags made immutable, for example pattern "v*".
	// +required
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Tags"
	Tags PatternSelector `json:"tags"`

	// disabled keeps the rule in Harbor without enforcing it.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Disabled"
	Disabled bool `json:"disabled,omitempty"`

	// adoptExisting takes over an existing rule with the same selectors.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Adopt Existing"
	AdoptExisting bool `json:"adoptExisting,omitempty"`

	// deletionPolicy controls whether the rule is deleted with this object.
	// +optional
	// +kubebuilder:default=Delete
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Deletion Policy"
	DeletionPolicy DeletionPolicy `json:"deletionPolicy,omitempty"`

	// syncPeriod overrides the connection's drift-correction interval.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Sync Period"
	SyncPeriod *metav1.Duration `json:"syncPeriod,omitempty"`
}

// HarborImmutableRuleStatus is the observed state of a HarborImmutableRule.
type HarborImmutableRuleStatus struct {
	// ruleID in Harbor.
	// +optional
	RuleID *int64 `json:"ruleID,omitempty"`

	// projectID of the project the rule belongs to.
	// +optional
	ProjectID *int64 `json:"projectID,omitempty"`

	// projectName of the project the rule belongs to.
	// +optional
	ProjectName string `json:"projectName,omitempty"`

	// observedGeneration is the spec generation the status reflects.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions: Ready=True when the rule matches the spec.
	// +listType=map
	// +listMapKey=type
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=status,displayName="Conditions",xDescriptors="urn:alm:descriptor:io.kubernetes.conditions"
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=himmutable
// +kubebuilder:printcolumn:name="Project",type=string,JSONPath=".status.projectName"
// +kubebuilder:printcolumn:name="Repositories",type=string,JSONPath=".spec.repositories.pattern"
// +kubebuilder:printcolumn:name="Tags",type=string,JSONPath=".spec.tags.pattern"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
// +operator-sdk:csv:customresourcedefinitions:displayName="Harbor Immutable Rule",resources={{HarborConnection,v1alpha1,""},{HarborProject,v1alpha1,""}}

// HarborImmutableRule manages a tag immutability rule in a Harbor project.
type HarborImmutableRule struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of HarborImmutableRule
	// +required
	Spec HarborImmutableRuleSpec `json:"spec"`

	// status defines the observed state of HarborImmutableRule
	// +optional
	Status HarborImmutableRuleStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// HarborImmutableRuleList contains a list of HarborImmutableRule
type HarborImmutableRuleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []HarborImmutableRule `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &HarborImmutableRule{}, &HarborImmutableRuleList{})
		return nil
	})
}
