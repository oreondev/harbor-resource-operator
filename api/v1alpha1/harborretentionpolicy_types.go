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
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// RetentionTemplate is what a retention rule keeps.
// +kubebuilder:validation:Enum=latestPushedK;latestPulledN;nDaysSinceLastPush;nDaysSinceLastPull;always
type RetentionTemplate string

const (
	RetainLatestPushedK      RetentionTemplate = "latestPushedK"
	RetainLatestPulledN      RetentionTemplate = "latestPulledN"
	RetainNDaysSinceLastPush RetentionTemplate = "nDaysSinceLastPush"
	RetainNDaysSinceLastPull RetentionTemplate = "nDaysSinceLastPull"
	RetainAlways             RetentionTemplate = "always"
)

// RetentionTagSelector selects the tags a rule looks at.
type RetentionTagSelector struct {
	PatternSelector `json:",inline"`

	// untagged also applies the rule to untagged artifacts. Defaults to true, as in Harbor's UI.
	// +optional
	// +kubebuilder:default=true
	Untagged *bool `json:"untagged,omitempty"`
}

// RetentionRule keeps the artifacts it selects. Artifacts no rule keeps are
// deleted when the policy runs.
// +kubebuilder:validation:XValidation:rule="self.template == 'always' || has(self.count)",message="count is required unless template is always"
type RetentionRule struct {
	// template is what to keep: latestPushedK (the K most recently pushed),
	// latestPulledN (the N most recently pulled), nDaysSinceLastPush,
	// nDaysSinceLastPull, or always.
	// +required
	Template RetentionTemplate `json:"template"`

	// count is K, N or the number of days, depending on the template.
	// +optional
	// +kubebuilder:validation:Minimum=0
	Count *int64 `json:"count,omitempty"`

	// repositories selects the repositories the rule applies to. Defaults to all.
	// +optional
	// +kubebuilder:default={}
	Repositories PatternSelector `json:"repositories,omitzero"`

	// tags selects the tags the rule applies to. Defaults to all tags and untagged artifacts.
	// +optional
	// +kubebuilder:default={}
	Tags RetentionTagSelector `json:"tags,omitzero"`

	// disabled keeps the rule without applying it.
	// +optional
	Disabled bool `json:"disabled,omitempty"`
}

// HarborRetentionPolicySpec defines a project's tag retention policy. A
// project has at most one policy; it holds up to 15 rules.
// +kubebuilder:validation:XValidation:rule="has(self.projectRef) != has(self.projectName)",message="set exactly one of projectRef or projectName"
// +kubebuilder:validation:XValidation:rule="has(self.projectRef) == has(oldSelf.projectRef) && (!has(self.projectRef) || self.projectRef.name == oldSelf.projectRef.name) && has(self.projectName) == has(oldSelf.projectName) && (!has(self.projectName) || self.projectName == oldSelf.projectName)",message="the project cannot change; create a new object instead"
type HarborRetentionPolicySpec struct {
	// connectionRef selects the Harbor instance.
	// +required
	ConnectionRef ConnectionRef `json:"connectionRef"`

	ProjectReference `json:",inline"`

	// rules are combined with OR: an artifact kept by any rule is retained.
	// +required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=15
	Rules []RetentionRule `json:"rules"`

	// schedule is a six-field cron expression (seconds first), for example
	// "0 0 2 * * *". Empty means the policy only runs when triggered by hand.
	// +optional
	Schedule string `json:"schedule,omitempty"`

	// adoptExisting takes over the project's existing retention policy.
	// +optional
	AdoptExisting bool `json:"adoptExisting,omitempty"`

	// deletionPolicy controls whether the policy is deleted with this object.
	// +optional
	// +kubebuilder:default=Delete
	DeletionPolicy DeletionPolicy `json:"deletionPolicy,omitempty"`

	// syncPeriod overrides the connection's drift-correction interval.
	// +optional
	SyncPeriod *metav1.Duration `json:"syncPeriod,omitempty"`
}

// HarborRetentionPolicyStatus is the observed state of a HarborRetentionPolicy.
type HarborRetentionPolicyStatus struct {
	// retentionID in Harbor.
	// +optional
	RetentionID *int64 `json:"retentionID,omitempty"`

	// projectID of the project the policy belongs to.
	// +optional
	ProjectID *int64 `json:"projectID,omitempty"`

	// projectName of the project the policy belongs to.
	// +optional
	ProjectName string `json:"projectName,omitempty"`

	// observedGeneration is the spec generation the status reflects.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions: Ready=True when the policy matches the spec.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hretention
// +kubebuilder:printcolumn:name="Project",type=string,JSONPath=".status.projectName"
// +kubebuilder:printcolumn:name="Schedule",type=string,JSONPath=".spec.schedule"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// HarborRetentionPolicy is the Schema for the harborretentionpolicies API
type HarborRetentionPolicy struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of HarborRetentionPolicy
	// +required
	Spec HarborRetentionPolicySpec `json:"spec"`

	// status defines the observed state of HarborRetentionPolicy
	// +optional
	Status HarborRetentionPolicyStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// HarborRetentionPolicyList contains a list of HarborRetentionPolicy
type HarborRetentionPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []HarborRetentionPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &HarborRetentionPolicy{}, &HarborRetentionPolicyList{})
		return nil
	})
}
