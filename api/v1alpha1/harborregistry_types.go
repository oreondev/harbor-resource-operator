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

// HarborRegistrySpec defines a replication endpoint (a remote registry) in Harbor.
type HarborRegistrySpec struct {
	// connectionRef selects the Harbor instance.
	// +required
	ConnectionRef ConnectionRef `json:"connectionRef"`

	// name of the endpoint in Harbor. Defaults to metadata.name.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	Name string `json:"name,omitempty"`

	// type is Harbor's adapter name, for example harbor, docker-hub,
	// docker-registry, aws-ecr, azure-acr, google-gcr, github-ghcr, quay,
	// gitlab, jfrog-artifactory, huawei-SWR, ali-acr, tencent-tcr, volcengine-cr.
	// +required
	// +kubebuilder:validation:MinLength=1
	Type string `json:"type"`

	// url of the remote registry.
	// +required
	// +kubebuilder:validation:Pattern=`^https?://.+`
	URL string `json:"url"`

	// insecure skips TLS verification when Harbor talks to the remote registry.
	// +optional
	Insecure bool `json:"insecure,omitempty"`

	// credentialsSecretRef names a Secret in this namespace with keys
	// "accessKey" and "accessSecret". Omit for anonymous access.
	// +optional
	CredentialsSecretRef *LocalObjectRef `json:"credentialsSecretRef,omitempty"`

	// credentialType is basic or oauth.
	// +optional
	// +kubebuilder:default=basic
	// +kubebuilder:validation:Enum=basic;oauth
	CredentialType string `json:"credentialType,omitempty"`

	// description shown in Harbor.
	// +optional
	Description string `json:"description,omitempty"`

	// createEvenIfUnhealthy creates the endpoint even when the ping fails.
	// +optional
	CreateEvenIfUnhealthy bool `json:"createEvenIfUnhealthy,omitempty"`

	// adoptExisting takes over an endpoint of the same name.
	// +optional
	AdoptExisting bool `json:"adoptExisting,omitempty"`

	// deletionPolicy controls whether the Harbor endpoint is deleted with this object.
	// +optional
	// +kubebuilder:default=Delete
	DeletionPolicy DeletionPolicy `json:"deletionPolicy,omitempty"`

	// syncPeriod overrides the connection's drift-correction interval.
	// +optional
	SyncPeriod *metav1.Duration `json:"syncPeriod,omitempty"`
}

// HarborRegistryStatus is the observed state of a HarborRegistry.
type HarborRegistryStatus struct {
	// registryID in Harbor.
	// +optional
	RegistryID *int64 `json:"registryID,omitempty"`

	// health is Harbor's status for the endpoint (healthy or unhealthy).
	// +optional
	Health string `json:"health,omitempty"`

	// credentialsSecretVersion is the resourceVersion of the credentials Secret
	// last pushed to Harbor. Harbor never returns secrets, so this is how
	// credential changes are detected.
	// +optional
	CredentialsSecretVersion string `json:"credentialsSecretVersion,omitempty"`

	// observedGeneration is the spec generation the status reflects.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions: Ready=True when the endpoint exists and matches the spec.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hreg
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=".spec.url"
// +kubebuilder:printcolumn:name="Health",type=string,JSONPath=".status.health"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// HarborRegistry is the Schema for the harborregistries API
type HarborRegistry struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of HarborRegistry
	// +required
	Spec HarborRegistrySpec `json:"spec"`

	// status defines the observed state of HarborRegistry
	// +optional
	Status HarborRegistryStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// HarborRegistryList contains a list of HarborRegistry
type HarborRegistryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []HarborRegistry `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &HarborRegistry{}, &HarborRegistryList{})
		return nil
	})
}
