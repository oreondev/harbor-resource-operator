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

// RobotLevel is the scope of a robot account.
// +kubebuilder:validation:Enum=project;system
type RobotLevel string

const (
	RobotLevelProject RobotLevel = "project"
	RobotLevelSystem  RobotLevel = "system"
)

// RobotAccess is one permission entry.
type RobotAccess struct {
	// resource such as repository, artifact, tag, scan, label, helm-chart.
	// +required
	// +kubebuilder:validation:MinLength=1
	Resource string `json:"resource"`

	// action such as pull, push, read, list, create, update, delete, stop.
	// +required
	// +kubebuilder:validation:MinLength=1
	Action string `json:"action"`

	// effect is allow or deny. Defaults to allow.
	// +optional
	// +kubebuilder:validation:Enum=allow;deny
	Effect string `json:"effect,omitempty"`
}

// RobotPermission grants access within one namespace (a project name, or "*"
// for system-wide permissions).
type RobotPermission struct {
	// namespace is the Harbor project name, or "*" for all projects (system level only).
	// For project level robots this defaults to the referenced project.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// access entries granted in the namespace.
	// +required
	// +kubebuilder:validation:MinItems=1
	Access []RobotAccess `json:"access"`
}

// RobotSecretSpec describes the Kubernetes Secret the credentials are written to.
type RobotSecretSpec struct {
	// name of the Secret. Defaults to "<metadata.name>-robot".
	// +optional
	Name string `json:"name,omitempty"`

	// type of the Secret: Opaque (keys username, password, host) or
	// kubernetes.io/dockerconfigjson (ready to use as an imagePullSecret).
	// +optional
	// +kubebuilder:default=Opaque
	// +kubebuilder:validation:Enum=Opaque;kubernetes.io/dockerconfigjson
	Type string `json:"type,omitempty"`

	// labels added to the Secret.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// annotations added to the Secret.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// RotationSpec controls automatic refresh of the robot secret.
type RotationSpec struct {
	// period after which the secret is refreshed regardless of expiry, for example 720h.
	// +optional
	Period *metav1.Duration `json:"period,omitempty"`

	// beforeExpiry refreshes the secret when the robot is this close to expiring.
	// Only useful when duration is set.
	// +optional
	BeforeExpiry *metav1.Duration `json:"beforeExpiry,omitempty"`
}

// HarborRobotAccountSpec defines the desired state of a robot account.
type HarborRobotAccountSpec struct {
	// connectionRef selects the Harbor instance.
	// +required
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Harbor Connection"
	ConnectionRef ConnectionRef `json:"connectionRef"`

	// name of the robot without Harbor's prefix. Defaults to metadata.name.
	// Harbor stores it as robot$<name> (system) or robot$<project>+<name> (project).
	// +optional
	// +kubebuilder:validation:Pattern=`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Name"
	Name string `json:"name,omitempty"`

	// level is project or system.
	// +optional
	// +kubebuilder:default=project
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Level"
	Level RobotLevel `json:"level,omitempty"`

	// projectRef names a HarborProject in this namespace. Required for project
	// level robots unless projectName is set.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Project"
	ProjectRef *LocalObjectRef `json:"projectRef,omitempty"`

	// projectName is the Harbor project name for project level robots when the
	// project is not managed by this operator.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Existing Project Name"
	ProjectName string `json:"projectName,omitempty"`

	// description shown in Harbor.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Description"
	Description string `json:"description,omitempty"`

	// duration in days before the robot expires. -1 means never.
	// +optional
	// +kubebuilder:default=-1
	// +kubebuilder:validation:Minimum=-1
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Duration"
	Duration int64 `json:"duration,omitempty"`

	// permissions granted to the robot. For project level robots a single
	// entry without namespace is usual.
	// +required
	// +kubebuilder:validation:MinItems=1
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Permissions"
	Permissions []RobotPermission `json:"permissions"`

	// secret describes the Kubernetes Secret that receives the credentials.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Credentials Secret"
	Secret RobotSecretSpec `json:"secret,omitempty"`

	// rotation controls automatic secret refresh.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Rotation"
	Rotation *RotationSpec `json:"rotation,omitempty"`

	// disabled disables the robot in Harbor without deleting it.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Disabled"
	Disabled bool `json:"disabled,omitempty"`

	// adoptExisting takes over a robot of the same name. Because Harbor never
	// reveals an existing secret, adoption refreshes the secret.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Adopt Existing"
	AdoptExisting bool `json:"adoptExisting,omitempty"`

	// deletionPolicy controls whether the Harbor robot is deleted with this object.
	// +optional
	// +kubebuilder:default=Delete
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Deletion Policy"
	DeletionPolicy DeletionPolicy `json:"deletionPolicy,omitempty"`

	// syncPeriod overrides the connection's drift-correction interval.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Sync Period"
	SyncPeriod *metav1.Duration `json:"syncPeriod,omitempty"`
}

// HarborRobotAccountStatus is the observed state of a HarborRobotAccount.
type HarborRobotAccountStatus struct {
	// robotID in Harbor.
	// +optional
	RobotID *int64 `json:"robotID,omitempty"`

	// robotName is the full name including Harbor's prefix.
	// +optional
	RobotName string `json:"robotName,omitempty"`

	// expiresAt is when the robot expires; empty when it never expires.
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// lastRotation is when the secret was last generated.
	// +optional
	LastRotation *metav1.Time `json:"lastRotation,omitempty"`

	// secretRef names the Secret holding the credentials.
	// +optional
	SecretRef *LocalObjectRef `json:"secretRef,omitempty"`

	// observedGeneration is the spec generation the status reflects.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions: Ready=True when the robot exists and the Secret is written.
	// +listType=map
	// +listMapKey=type
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=status,displayName="Conditions",xDescriptors="urn:alm:descriptor:io.kubernetes.conditions"
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hrobot
// +kubebuilder:printcolumn:name="Robot",type=string,JSONPath=".status.robotName"
// +kubebuilder:printcolumn:name="Secret",type=string,JSONPath=".status.secretRef.name"
// +kubebuilder:printcolumn:name="Expires",type=string,JSONPath=".status.expiresAt"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
// +operator-sdk:csv:customresourcedefinitions:displayName="Harbor Robot Account",resources={{HarborConnection,v1alpha1,""},{HarborProject,v1alpha1,""},{Secret,v1,""}}

// HarborRobotAccount manages a Harbor robot account and writes its credentials to a Secret.
type HarborRobotAccount struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of HarborRobotAccount
	// +required
	Spec HarborRobotAccountSpec `json:"spec"`

	// status defines the observed state of HarborRobotAccount
	// +optional
	Status HarborRobotAccountStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// HarborRobotAccountList contains a list of HarborRobotAccount
type HarborRobotAccountList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []HarborRobotAccount `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &HarborRobotAccount{}, &HarborRobotAccountList{})
		return nil
	})
}
