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

// HarborConnectionSpec defines how to reach one Harbor instance.
type HarborConnectionSpec struct {
	// url of the Harbor instance, for example https://harbor.example.com.
	// The API path /api/v2.0 is appended by the operator.
	// +required
	// +kubebuilder:validation:Pattern=`^https?://.+`
	URL string `json:"url"`

	// credentialsSecretRef names a Secret in this namespace with keys
	// "username" and "password". Use a system robot account with the
	// permissions the operator needs rather than the admin user.
	// +required
	CredentialsSecretRef LocalObjectRef `json:"credentialsSecretRef"`

	// caBundleSecretRef names a Secret in this namespace with key "ca.crt"
	// holding PEM certificates to trust for the Harbor URL.
	// +optional
	CABundleSecretRef *LocalObjectRef `json:"caBundleSecretRef,omitempty"`

	// insecureSkipTLSVerify disables TLS certificate verification. Not for production.
	// +optional
	InsecureSkipTLSVerify bool `json:"insecureSkipTLSVerify,omitempty"`

	// allowedNamespaces lists namespaces whose resources may reference this
	// connection. "*" allows every namespace. The connection's own namespace
	// is always allowed.
	// +optional
	// +listType=set
	AllowedNamespaces []string `json:"allowedNamespaces,omitempty"`

	// syncPeriod is the default interval at which resources using this
	// connection are re-read from Harbor and drift is corrected.
	// +optional
	// +kubebuilder:default="10m"
	SyncPeriod *metav1.Duration `json:"syncPeriod,omitempty"`

	// requestsPerSecond caps the request rate the operator sends to this Harbor.
	// +optional
	// +kubebuilder:default=10
	// +kubebuilder:validation:Minimum=1
	RequestsPerSecond int32 `json:"requestsPerSecond,omitempty"`
}

// HarborConnectionStatus is the observed state of a HarborConnection.
type HarborConnectionStatus struct {
	// harborVersion reported by GET /systemInfo.
	// +optional
	HarborVersion string `json:"harborVersion,omitempty"`

	// username the operator authenticated as.
	// +optional
	Username string `json:"username,omitempty"`

	// lastCheckTime is when the connection was last verified.
	// +optional
	LastCheckTime *metav1.Time `json:"lastCheckTime,omitempty"`

	// observedGeneration is the spec generation the status reflects.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions: Ready=True when Harbor answered with the expected credentials.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hconn
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=".spec.url"
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=".status.harborVersion"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// HarborConnection is the Schema for the harborconnections API
type HarborConnection struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of HarborConnection
	// +required
	Spec HarborConnectionSpec `json:"spec"`

	// status defines the observed state of HarborConnection
	// +optional
	Status HarborConnectionStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// HarborConnectionList contains a list of HarborConnection
type HarborConnectionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []HarborConnection `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &HarborConnection{}, &HarborConnectionList{})
		return nil
	})
}
