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

// ProjectMetadata holds the optional Harbor project settings. Only fields that
// are set are managed; unset fields are left as Harbor has them.
type ProjectMetadata struct {
	// autoScan scans every pushed artifact automatically.
	// +optional
	AutoScan *bool `json:"autoScan,omitempty"`

	// autoSbomGeneration generates an SBOM for every pushed artifact.
	// +optional
	AutoSBOMGeneration *bool `json:"autoSbomGeneration,omitempty"`

	// preventVulnerable blocks pulls of artifacts with vulnerabilities at or above severity.
	// +optional
	PreventVulnerable *bool `json:"preventVulnerable,omitempty"`

	// severity threshold used by preventVulnerable.
	// +optional
	// +kubebuilder:validation:Enum=none;low;medium;high;critical
	Severity *string `json:"severity,omitempty"`

	// enableContentTrustCosign requires cosign signatures for pulls.
	// +optional
	EnableContentTrustCosign *bool `json:"enableContentTrustCosign,omitempty"`

	// reuseSystemCVEAllowlist uses the system CVE allowlist instead of the project one.
	// +optional
	ReuseSystemCVEAllowlist *bool `json:"reuseSystemCVEAllowlist,omitempty"`
}

// CVEAllowlist is the project level list of CVEs to ignore.
type CVEAllowlist struct {
	// expiresAt is when the allowlist stops applying. Empty means never.
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// items are CVE identifiers such as CVE-2025-1234.
	// +optional
	// +listType=set
	Items []string `json:"items,omitempty"`
}

// ProxyCacheSpec turns the project into a proxy cache of an upstream registry.
type ProxyCacheSpec struct {
	// registryRef names a HarborRegistry in this namespace used as the upstream.
	// Immutable after creation, as in Harbor.
	// +required
	RegistryRef LocalObjectRef `json:"registryRef"`
}

// HarborProjectSpec defines the desired state of a Harbor project.
type HarborProjectSpec struct {
	// connectionRef selects the Harbor instance.
	// +required
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Harbor Connection"
	ConnectionRef ConnectionRef `json:"connectionRef"`

	// name of the project in Harbor. Defaults to metadata.name.
	// +optional
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:Pattern=`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Name"
	Name string `json:"name,omitempty"`

	// public makes the project readable without authentication.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Public"
	Public *bool `json:"public,omitempty"`

	// storageQuotaBytes caps the project storage. -1 means unlimited.
	// +optional
	// +kubebuilder:validation:Minimum=-1
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Storage Quota (bytes)"
	StorageQuotaBytes *int64 `json:"storageQuotaBytes,omitempty"`

	// metadata holds scanning, content trust and allowlist settings.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Project Settings"
	Metadata *ProjectMetadata `json:"metadata,omitempty"`

	// cveAllowlist is the project level CVE allowlist. When set, it is managed in full.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="CVE Allowlist"
	CVEAllowlist *CVEAllowlist `json:"cveAllowlist,omitempty"`

	// proxyCache makes the project a proxy cache for an upstream registry.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Proxy Cache"
	ProxyCache *ProxyCacheSpec `json:"proxyCache,omitempty"`

	// adoptExisting allows taking over a project of the same name that already
	// exists in Harbor. Without it, an existing project is reported as a conflict.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Adopt Existing"
	AdoptExisting bool `json:"adoptExisting,omitempty"`

	// deletionPolicy controls whether the Harbor project is deleted with this object.
	// Harbor refuses to delete projects that still hold repositories; the
	// operator never deletes repositories.
	// +optional
	// +kubebuilder:default=Retain
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Deletion Policy"
	DeletionPolicy DeletionPolicy `json:"deletionPolicy,omitempty"`

	// syncPeriod overrides the connection's drift-correction interval.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Sync Period"
	SyncPeriod *metav1.Duration `json:"syncPeriod,omitempty"`
}

// HarborProjectStatus is the observed state of a HarborProject.
type HarborProjectStatus struct {
	// projectID in Harbor once created or adopted.
	// +optional
	ProjectID *int64 `json:"projectID,omitempty"`

	// projectName as created in Harbor.
	// +optional
	ProjectName string `json:"projectName,omitempty"`

	// observedGeneration is the spec generation the status reflects.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions: Ready=True when the Harbor project matches the spec.
	// +listType=map
	// +listMapKey=type
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=status,displayName="Conditions",xDescriptors="urn:alm:descriptor:io.kubernetes.conditions"
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hproj
// +kubebuilder:printcolumn:name="Project",type=string,JSONPath=".status.projectName"
// +kubebuilder:printcolumn:name="ID",type=integer,JSONPath=".status.projectID"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
// +operator-sdk:csv:customresourcedefinitions:displayName="Harbor Project",resources={{HarborConnection,v1alpha1,""},{HarborRegistry,v1alpha1,""}}

// HarborProject manages a Harbor project: visibility, storage quota, scanning, CVE allowlist and proxy cache.
type HarborProject struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of HarborProject
	// +required
	Spec HarborProjectSpec `json:"spec"`

	// status defines the observed state of HarborProject
	// +optional
	Status HarborProjectStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// HarborProjectList contains a list of HarborProject
type HarborProjectList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []HarborProject `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &HarborProject{}, &HarborProjectList{})
		return nil
	})
}
