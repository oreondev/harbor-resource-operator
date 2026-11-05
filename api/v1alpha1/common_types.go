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

// ConnectionRef points to the HarborConnection that owns the Harbor instance
// a resource is reconciled against.
type ConnectionRef struct {
	// name of the HarborConnection.
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// namespace of the HarborConnection. Defaults to the namespace of the
	// referencing resource. A cross-namespace reference is only honoured when
	// the connection's spec.allowedNamespaces permits it.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// LocalObjectRef points to an object in the same namespace.
type LocalObjectRef struct {
	// name of the referenced object.
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// ProjectReference selects the Harbor project a project-scoped resource
// belongs to: a HarborProject in the same namespace, or the name of a project
// the operator does not manage. Set exactly one.
type ProjectReference struct {
	// projectRef names a HarborProject in this namespace. The resource waits
	// until that project is Ready.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Project"
	ProjectRef *LocalObjectRef `json:"projectRef,omitempty"`

	// projectName is the name of an existing Harbor project not managed by
	// this operator.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Existing Project Name"
	ProjectName string `json:"projectName,omitempty"`
}

// SelectorDecoration includes or excludes what a pattern matches.
// +kubebuilder:validation:Enum=matches;excludes
type SelectorDecoration string

const (
	SelectorMatches  SelectorDecoration = "matches"
	SelectorExcludes SelectorDecoration = "excludes"
)

// PatternSelector selects repositories or tags with a doublestar pattern
// such as "**", "release-*" or "{v1,v2}*".
type PatternSelector struct {
	// decoration is matches (default) or excludes.
	// +optional
	// +kubebuilder:default=matches
	Decoration SelectorDecoration `json:"decoration,omitempty"`

	// pattern is a doublestar glob. Defaults to "**" (everything).
	// +optional
	// +kubebuilder:default="**"
	// +kubebuilder:validation:MinLength=1
	Pattern string `json:"pattern,omitempty"`
}

// DeletionPolicy controls what happens in Harbor when the Kubernetes object is deleted.
// +kubebuilder:validation:Enum=Retain;Delete
type DeletionPolicy string

const (
	// DeletionPolicyRetain leaves the Harbor object in place.
	DeletionPolicyRetain DeletionPolicy = "Retain"
	// DeletionPolicyDelete removes the Harbor object before the finalizer is cleared.
	DeletionPolicyDelete DeletionPolicy = "Delete"
)

// Condition types and reasons shared by all kinds.
const (
	// ConditionReady is True when the Harbor object matches the spec.
	ConditionReady = "Ready"

	ReasonSynced                  = "Synced"
	ReasonConnected               = "Connected"
	ReasonConnectionNotFound      = "ConnectionNotFound"
	ReasonConnectionNotAllowed    = "ConnectionNotAllowed"
	ReasonConnectionUnavailable   = "ConnectionUnavailable"
	ReasonAlreadyExists           = "AlreadyExists"
	ReasonDependencyNotReady      = "DependencyNotReady"
	ReasonImmutableField          = "ImmutableField"
	ReasonInvalidSpec             = "InvalidSpec"
	ReasonHarborError             = "HarborError"
	ReasonProjectNotEmpty         = "ProjectNotEmpty"
	ReasonSecretConflict          = "SecretConflict"
	ReasonPingFailed              = "PingFailed"
	ReasonRegistryNotReady        = "RegistryNotReady"
	ReasonDeleting                = "Deleting"
	ReasonSecretMissing           = "SecretMissing"
	ReasonAuthenticationFailed    = "AuthenticationFailed"
	ReasonUnsupportedHarbor       = "UnsupportedHarbor"
	ReasonReconcileInProgress     = "ReconcileInProgress"
	ReasonExternalResourceMissing = "ExternalResourceMissing"
	ReasonDuplicate               = "Duplicate"
	ReasonUserNotFound            = "UserNotFound"
)

// Finalizer added to every managed kind.
const Finalizer = "harbor.oreonit.com/finalizer"
