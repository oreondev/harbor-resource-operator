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

// WebhookTargetType is how Harbor delivers the event.
// +kubebuilder:validation:Enum=http;slack
type WebhookTargetType string

const (
	WebhookTargetHTTP  WebhookTargetType = "http"
	WebhookTargetSlack WebhookTargetType = "slack"
)

// SecretKeyRef names one key of a Secret in the same namespace.
type SecretKeyRef struct {
	// name of the Secret.
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// key in the Secret.
	// +required
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

// WebhookTarget is one delivery endpoint.
// +kubebuilder:validation:XValidation:rule="self.type == 'http' || !has(self.payloadFormat) || self.payloadFormat == 'Default'",message="payloadFormat CloudEvents needs type http"
type WebhookTarget struct {
	// type is http (default) or slack.
	// +optional
	// +kubebuilder:default=http
	Type WebhookTargetType `json:"type,omitempty"`

	// address is the URL Harbor posts to.
	// +required
	// +kubebuilder:validation:Pattern=`^https?://.+`
	Address string `json:"address"`

	// authHeaderSecretRef selects a Secret key whose value Harbor sends as the
	// Authorization header, for example "Bearer <token>".
	// +optional
	AuthHeaderSecretRef *SecretKeyRef `json:"authHeaderSecretRef,omitempty"`

	// skipCertVerify disables TLS verification of the address. Not for production.
	// +optional
	SkipCertVerify bool `json:"skipCertVerify,omitempty"`

	// payloadFormat is Default or CloudEvents (http targets only).
	// +optional
	// +kubebuilder:validation:Enum=Default;CloudEvents
	PayloadFormat string `json:"payloadFormat,omitempty"`
}

// HarborWebhookSpec defines a project webhook policy.
// +kubebuilder:validation:XValidation:rule="has(self.projectRef) != has(self.projectName)",message="set exactly one of projectRef or projectName"
// +kubebuilder:validation:XValidation:rule="has(self.projectRef) == has(oldSelf.projectRef) && (!has(self.projectRef) || self.projectRef.name == oldSelf.projectRef.name) && has(self.projectName) == has(oldSelf.projectName) && (!has(self.projectName) || self.projectName == oldSelf.projectName)",message="the project cannot change; create a new object instead"
type HarborWebhookSpec struct {
	// connectionRef selects the Harbor instance.
	// +required
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Harbor Connection"
	ConnectionRef ConnectionRef `json:"connectionRef"`

	ProjectReference `json:",inline"`

	// name of the webhook policy in Harbor. Defaults to metadata.name.
	// +optional
	// +kubebuilder:validation:MaxLength=256
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Name"
	Name string `json:"name,omitempty"`

	// description shown in Harbor.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Description"
	Description string `json:"description,omitempty"`

	// eventTypes Harbor notifies about, for example PUSH_ARTIFACT,
	// PULL_ARTIFACT, DELETE_ARTIFACT, SCANNING_COMPLETED, SCANNING_FAILED,
	// QUOTA_EXCEED, QUOTA_WARNING, REPLICATION, TAG_RETENTION.
	// +required
	// +kubebuilder:validation:MinItems=1
	// +listType=set
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Event Types"
	EventTypes []string `json:"eventTypes"`

	// targets receive the events.
	// +required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=8
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Targets"
	Targets []WebhookTarget `json:"targets"`

	// enabled turns the webhook on or off. Defaults to true.
	// +optional
	// +kubebuilder:default=true
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Enabled"
	Enabled *bool `json:"enabled,omitempty"`

	// adoptExisting takes over a webhook policy of the same name.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Adopt Existing"
	AdoptExisting bool `json:"adoptExisting,omitempty"`

	// deletionPolicy controls whether the webhook is deleted with this object.
	// +optional
	// +kubebuilder:default=Delete
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Deletion Policy"
	DeletionPolicy DeletionPolicy `json:"deletionPolicy,omitempty"`

	// syncPeriod overrides the connection's drift-correction interval.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Sync Period"
	SyncPeriod *metav1.Duration `json:"syncPeriod,omitempty"`
}

// HarborWebhookStatus is the observed state of a HarborWebhook.
type HarborWebhookStatus struct {
	// webhookID in Harbor.
	// +optional
	WebhookID *int64 `json:"webhookID,omitempty"`

	// projectID of the project the webhook belongs to.
	// +optional
	ProjectID *int64 `json:"projectID,omitempty"`

	// projectName of the project the webhook belongs to.
	// +optional
	ProjectName string `json:"projectName,omitempty"`

	// authSecretsVersion records the resourceVersions of the auth header
	// Secrets last sent to Harbor, so a Secret change triggers an update.
	// +optional
	AuthSecretsVersion string `json:"authSecretsVersion,omitempty"`

	// observedGeneration is the spec generation the status reflects.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions: Ready=True when the webhook matches the spec.
	// +listType=map
	// +listMapKey=type
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=status,displayName="Conditions",xDescriptors="urn:alm:descriptor:io.kubernetes.conditions"
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hwebhook
// +kubebuilder:printcolumn:name="Project",type=string,JSONPath=".status.projectName"
// +kubebuilder:printcolumn:name="ID",type=integer,JSONPath=".status.webhookID"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
// +operator-sdk:csv:customresourcedefinitions:displayName="Harbor Webhook",resources={{HarborConnection,v1alpha1,""},{HarborProject,v1alpha1,""},{Secret,v1,""}}

// HarborWebhook manages a webhook policy in a Harbor project.
type HarborWebhook struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of HarborWebhook
	// +required
	Spec HarborWebhookSpec `json:"spec"`

	// status defines the observed state of HarborWebhook
	// +optional
	Status HarborWebhookStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// HarborWebhookList contains a list of HarborWebhook
type HarborWebhookList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []HarborWebhook `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &HarborWebhook{}, &HarborWebhookList{})
		return nil
	})
}
