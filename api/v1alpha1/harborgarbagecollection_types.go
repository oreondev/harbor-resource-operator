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

// HarborGarbageCollectionSpec sets the system garbage collection schedule of
// one Harbor. Only one object per connection takes effect: the oldest; later
// ones report Ready=False with reason Duplicate.
type HarborGarbageCollectionSpec struct {
	// connectionRef selects the Harbor instance. Needs a system administrator connection.
	// +required
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Harbor Connection"
	ConnectionRef ConnectionRef `json:"connectionRef"`

	// schedule is Hourly, Daily, Weekly, None, or a six-field cron expression
	// (seconds first) such as "0 0 3 * * 6".
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^(Hourly|Daily|Weekly|None|\S+( \S+){5})$`
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Schedule"
	Schedule string `json:"schedule"`

	// deleteUntagged also deletes untagged artifacts.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Delete Untagged"
	DeleteUntagged bool `json:"deleteUntagged,omitempty"`

	// workers run garbage collection in parallel, 1 to 5.
	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=5
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Workers"
	Workers int32 `json:"workers,omitempty"`

	// deletionPolicy Retain (default) leaves the schedule in Harbor when this
	// object is deleted; Delete sets the schedule to None.
	// +optional
	// +kubebuilder:default=Retain
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Deletion Policy"
	DeletionPolicy DeletionPolicy `json:"deletionPolicy,omitempty"`

	// syncPeriod overrides the connection's drift-correction interval.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Sync Period"
	SyncPeriod *metav1.Duration `json:"syncPeriod,omitempty"`
}

// GCRunStatus describes one garbage collection run.
type GCRunStatus struct {
	// id of the run in Harbor.
	ID int64 `json:"id"`

	// status as Harbor reports it, for example Success, Running or Error.
	// +optional
	Status string `json:"status,omitempty"`

	// startTime of the run.
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// updateTime is when Harbor last updated the run.
	// +optional
	UpdateTime *metav1.Time `json:"updateTime,omitempty"`
}

// HarborGarbageCollectionStatus is the observed state of a HarborGarbageCollection.
type HarborGarbageCollectionStatus struct {
	// nextScheduledTime as Harbor reports it.
	// +optional
	NextScheduledTime string `json:"nextScheduledTime,omitempty"`

	// lastRun is the most recent garbage collection run.
	// +optional
	LastRun *GCRunStatus `json:"lastRun,omitempty"`

	// observedGeneration is the spec generation the status reflects.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions: Ready=True when Harbor's schedule matches the spec.
	// +listType=map
	// +listMapKey=type
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=status,displayName="Conditions",xDescriptors="urn:alm:descriptor:io.kubernetes.conditions"
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hgc
// +kubebuilder:printcolumn:name="Schedule",type=string,JSONPath=".spec.schedule"
// +kubebuilder:printcolumn:name="Last run",type=string,JSONPath=".status.lastRun.status"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
// +operator-sdk:csv:customresourcedefinitions:displayName="Harbor Garbage Collection",resources={{HarborConnection,v1alpha1,""}}

// HarborGarbageCollection manages Harbor's garbage collection schedule.
type HarborGarbageCollection struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of HarborGarbageCollection
	// +required
	Spec HarborGarbageCollectionSpec `json:"spec"`

	// status defines the observed state of HarborGarbageCollection
	// +optional
	Status HarborGarbageCollectionStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// HarborGarbageCollectionList contains a list of HarborGarbageCollection
type HarborGarbageCollectionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []HarborGarbageCollection `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &HarborGarbageCollection{}, &HarborGarbageCollectionList{})
		return nil
	})
}
