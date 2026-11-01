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

// ReplicationDirection says whether artifacts leave or enter this Harbor.
// +kubebuilder:validation:Enum=push;pull
type ReplicationDirection string

const (
	// ReplicationPush replicates from this Harbor to destRegistryRef.
	ReplicationPush ReplicationDirection = "push"
	// ReplicationPull replicates from srcRegistryRef into this Harbor.
	ReplicationPull ReplicationDirection = "pull"
)

// ReplicationFilter narrows what a policy replicates.
type ReplicationFilter struct {
	// type is name, tag, label or resource.
	// +required
	// +kubebuilder:validation:Enum=name;tag;label;resource
	Type string `json:"type"`

	// value is a glob for name and tag (for example "payments/**", "v*"), a
	// label name for label, and image or chart for resource.
	// +required
	// +kubebuilder:validation:MinLength=1
	Value string `json:"value"`

	// decoration is matches or excludes (tag and label filters only).
	// +optional
	// +kubebuilder:validation:Enum=matches;excludes
	Decoration string `json:"decoration,omitempty"`
}

// ReplicationTrigger says when the policy runs.
type ReplicationTrigger struct {
	// type is manual, scheduled or event_based.
	// +optional
	// +kubebuilder:default=manual
	// +kubebuilder:validation:Enum=manual;scheduled;event_based
	Type string `json:"type,omitempty"`

	// cron expression (Harbor uses six fields with seconds) for type scheduled.
	// +optional
	Cron string `json:"cron,omitempty"`
}

// HarborReplicationPolicySpec defines the desired state of a replication policy.
type HarborReplicationPolicySpec struct {
	// connectionRef selects the Harbor instance.
	// +required
	ConnectionRef ConnectionRef `json:"connectionRef"`

	// name of the policy in Harbor. Defaults to metadata.name.
	// +optional
	// +kubebuilder:validation:MaxLength=256
	Name string `json:"name,omitempty"`

	// description shown in Harbor.
	// +optional
	Description string `json:"description,omitempty"`

	// direction is push (local to destRegistryRef) or pull (srcRegistryRef to local).
	// +required
	Direction ReplicationDirection `json:"direction"`

	// destRegistryRef names the HarborRegistry to push to. Required for push.
	// +optional
	DestRegistryRef *LocalObjectRef `json:"destRegistryRef,omitempty"`

	// srcRegistryRef names the HarborRegistry to pull from. Required for pull.
	// +optional
	SrcRegistryRef *LocalObjectRef `json:"srcRegistryRef,omitempty"`

	// destNamespace is the target project or namespace. Empty keeps the source path.
	// +optional
	DestNamespace string `json:"destNamespace,omitempty"`

	// destNamespaceReplaceCount is how many leading path components destNamespace
	// replaces. -1 is Harbor's legacy mode.
	// +optional
	// +kubebuilder:default=-1
	// +kubebuilder:validation:Minimum=-1
	DestNamespaceReplaceCount *int32 `json:"destNamespaceReplaceCount,omitempty"`

	// filters are managed in full.
	// +optional
	Filters []ReplicationFilter `json:"filters,omitempty"`

	// trigger says when the policy runs.
	// +optional
	Trigger ReplicationTrigger `json:"trigger,omitempty"`

	// override replaces artifacts that already exist at the destination.
	// +optional
	// +kubebuilder:default=true
	Override *bool `json:"override,omitempty"`

	// enabled enables the policy.
	// +optional
	// +kubebuilder:default=true
	Enabled *bool `json:"enabled,omitempty"`

	// replicateDeletion propagates deletions.
	// +optional
	ReplicateDeletion bool `json:"replicateDeletion,omitempty"`

	// copyByChunk transfers blobs in chunks.
	// +optional
	CopyByChunk bool `json:"copyByChunk,omitempty"`

	// singleActiveReplication skips a run while the previous one is still active.
	// +optional
	SingleActiveReplication bool `json:"singleActiveReplication,omitempty"`

	// speedKBps limits bandwidth. 0 means unlimited.
	// +optional
	// +kubebuilder:validation:Minimum=0
	SpeedKBps int32 `json:"speedKBps,omitempty"`

	// executeOnCreate starts one execution right after the policy is created.
	// +optional
	ExecuteOnCreate bool `json:"executeOnCreate,omitempty"`

	// adoptExisting takes over a policy of the same name.
	// +optional
	AdoptExisting bool `json:"adoptExisting,omitempty"`

	// deletionPolicy controls whether the Harbor policy is deleted with this object.
	// +optional
	// +kubebuilder:default=Delete
	DeletionPolicy DeletionPolicy `json:"deletionPolicy,omitempty"`

	// syncPeriod overrides the connection's drift-correction interval.
	// +optional
	SyncPeriod *metav1.Duration `json:"syncPeriod,omitempty"`
}

// ReplicationExecutionStatus summarises the latest execution.
type ReplicationExecutionStatus struct {
	// id of the execution.
	ID int64 `json:"id"`

	// status such as Succeed, Failed, InProgress, Stopped.
	// +optional
	Status string `json:"status,omitempty"`

	// startTime of the execution.
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// endTime of the execution.
	// +optional
	EndTime *metav1.Time `json:"endTime,omitempty"`
}

// HarborReplicationPolicyStatus is the observed state of a HarborReplicationPolicy.
type HarborReplicationPolicyStatus struct {
	// policyID in Harbor.
	// +optional
	PolicyID *int64 `json:"policyID,omitempty"`

	// executedOnCreate records that the executeOnCreate run was started.
	// +optional
	ExecutedOnCreate bool `json:"executedOnCreate,omitempty"`

	// lastExecution mirrors the most recent execution in Harbor.
	// +optional
	LastExecution *ReplicationExecutionStatus `json:"lastExecution,omitempty"`

	// observedGeneration is the spec generation the status reflects.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions: Ready=True when the policy exists and matches the spec.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hrepl
// +kubebuilder:printcolumn:name="Direction",type=string,JSONPath=".spec.direction"
// +kubebuilder:printcolumn:name="Trigger",type=string,JSONPath=".spec.trigger.type"
// +kubebuilder:printcolumn:name="Last run",type=string,JSONPath=".status.lastExecution.status"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// HarborReplicationPolicy is the Schema for the harborreplicationpolicies API
type HarborReplicationPolicy struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of HarborReplicationPolicy
	// +required
	Spec HarborReplicationPolicySpec `json:"spec"`

	// status defines the observed state of HarborReplicationPolicy
	// +optional
	Status HarborReplicationPolicyStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// HarborReplicationPolicyList contains a list of HarborReplicationPolicy
type HarborReplicationPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []HarborReplicationPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &HarborReplicationPolicy{}, &HarborReplicationPolicyList{})
		return nil
	})
}
