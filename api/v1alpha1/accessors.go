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

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// The accessors below let controllers and metrics treat every managed kind
// alike. GetConnectionRef returns the reference with the namespace defaulted.

// GetConditions returns the status conditions.
func (o *HarborConnection) GetConditions() []metav1.Condition { return o.Status.Conditions }

// GetConnectionRef returns a reference to the connection itself.
func (o *HarborConnection) GetConnectionRef() ConnectionRef {
	return ConnectionRef{Name: o.Name, Namespace: o.Namespace}
}

// GetConditions returns the status conditions.
func (o *HarborProject) GetConditions() []metav1.Condition { return o.Status.Conditions }

// GetConnectionRef returns spec.connectionRef with the namespace defaulted.
func (o *HarborProject) GetConnectionRef() ConnectionRef {
	return o.Spec.ConnectionRef.withDefault(o.Namespace)
}

// GetConditions returns the status conditions.
func (o *HarborRobotAccount) GetConditions() []metav1.Condition { return o.Status.Conditions }

// GetConnectionRef returns spec.connectionRef with the namespace defaulted.
func (o *HarborRobotAccount) GetConnectionRef() ConnectionRef {
	return o.Spec.ConnectionRef.withDefault(o.Namespace)
}

// GetConditions returns the status conditions.
func (o *HarborRegistry) GetConditions() []metav1.Condition { return o.Status.Conditions }

// GetConnectionRef returns spec.connectionRef with the namespace defaulted.
func (o *HarborRegistry) GetConnectionRef() ConnectionRef {
	return o.Spec.ConnectionRef.withDefault(o.Namespace)
}

// GetConditions returns the status conditions.
func (o *HarborReplicationPolicy) GetConditions() []metav1.Condition { return o.Status.Conditions }

// GetConnectionRef returns spec.connectionRef with the namespace defaulted.
func (o *HarborReplicationPolicy) GetConnectionRef() ConnectionRef {
	return o.Spec.ConnectionRef.withDefault(o.Namespace)
}

// GetConditions returns the status conditions.
func (o *HarborProjectMember) GetConditions() []metav1.Condition { return o.Status.Conditions }

// GetConnectionRef returns spec.connectionRef with the namespace defaulted.
func (o *HarborProjectMember) GetConnectionRef() ConnectionRef {
	return o.Spec.ConnectionRef.withDefault(o.Namespace)
}

// StatusConditions returns a pointer to the status conditions for updates.
func (o *HarborProjectMember) StatusConditions() *[]metav1.Condition { return &o.Status.Conditions }

// SetObservedGeneration records the generation the status reflects.
func (o *HarborProjectMember) SetObservedGeneration(g int64) { o.Status.ObservedGeneration = g }

// GetSyncPeriod returns spec.syncPeriod.
func (o *HarborProjectMember) GetSyncPeriod() *metav1.Duration { return o.Spec.SyncPeriod }

// GetDeletionPolicy returns spec.deletionPolicy.
func (o *HarborProjectMember) GetDeletionPolicy() DeletionPolicy { return o.Spec.DeletionPolicy }

// GetProjectReference returns the project the resource belongs to.
func (o *HarborProjectMember) GetProjectReference() ProjectReference { return o.Spec.ProjectReference }

// GetConditions returns the status conditions.
func (o *HarborLabel) GetConditions() []metav1.Condition { return o.Status.Conditions }

// GetConnectionRef returns spec.connectionRef with the namespace defaulted.
func (o *HarborLabel) GetConnectionRef() ConnectionRef {
	return o.Spec.ConnectionRef.withDefault(o.Namespace)
}

// StatusConditions returns a pointer to the status conditions for updates.
func (o *HarborLabel) StatusConditions() *[]metav1.Condition { return &o.Status.Conditions }

// SetObservedGeneration records the generation the status reflects.
func (o *HarborLabel) SetObservedGeneration(g int64) { o.Status.ObservedGeneration = g }

// GetSyncPeriod returns spec.syncPeriod.
func (o *HarborLabel) GetSyncPeriod() *metav1.Duration { return o.Spec.SyncPeriod }

// GetDeletionPolicy returns spec.deletionPolicy.
func (o *HarborLabel) GetDeletionPolicy() DeletionPolicy { return o.Spec.DeletionPolicy }

// GetProjectReference returns the project the resource belongs to.
func (o *HarborLabel) GetProjectReference() ProjectReference { return o.Spec.ProjectReference }

// GetConditions returns the status conditions.
func (o *HarborImmutableRule) GetConditions() []metav1.Condition { return o.Status.Conditions }

// GetConnectionRef returns spec.connectionRef with the namespace defaulted.
func (o *HarborImmutableRule) GetConnectionRef() ConnectionRef {
	return o.Spec.ConnectionRef.withDefault(o.Namespace)
}

// StatusConditions returns a pointer to the status conditions for updates.
func (o *HarborImmutableRule) StatusConditions() *[]metav1.Condition { return &o.Status.Conditions }

// SetObservedGeneration records the generation the status reflects.
func (o *HarborImmutableRule) SetObservedGeneration(g int64) { o.Status.ObservedGeneration = g }

// GetSyncPeriod returns spec.syncPeriod.
func (o *HarborImmutableRule) GetSyncPeriod() *metav1.Duration { return o.Spec.SyncPeriod }

// GetDeletionPolicy returns spec.deletionPolicy.
func (o *HarborImmutableRule) GetDeletionPolicy() DeletionPolicy { return o.Spec.DeletionPolicy }

// GetProjectReference returns the project the resource belongs to.
func (o *HarborImmutableRule) GetProjectReference() ProjectReference { return o.Spec.ProjectReference }

// GetConditions returns the status conditions.
func (o *HarborWebhook) GetConditions() []metav1.Condition { return o.Status.Conditions }

// GetConnectionRef returns spec.connectionRef with the namespace defaulted.
func (o *HarborWebhook) GetConnectionRef() ConnectionRef {
	return o.Spec.ConnectionRef.withDefault(o.Namespace)
}

// StatusConditions returns a pointer to the status conditions for updates.
func (o *HarborWebhook) StatusConditions() *[]metav1.Condition { return &o.Status.Conditions }

// SetObservedGeneration records the generation the status reflects.
func (o *HarborWebhook) SetObservedGeneration(g int64) { o.Status.ObservedGeneration = g }

// GetSyncPeriod returns spec.syncPeriod.
func (o *HarborWebhook) GetSyncPeriod() *metav1.Duration { return o.Spec.SyncPeriod }

// GetDeletionPolicy returns spec.deletionPolicy.
func (o *HarborWebhook) GetDeletionPolicy() DeletionPolicy { return o.Spec.DeletionPolicy }

// GetProjectReference returns the project the resource belongs to.
func (o *HarborWebhook) GetProjectReference() ProjectReference { return o.Spec.ProjectReference }

// GetConditions returns the status conditions.
func (o *HarborRetentionPolicy) GetConditions() []metav1.Condition { return o.Status.Conditions }

// GetConnectionRef returns spec.connectionRef with the namespace defaulted.
func (o *HarborRetentionPolicy) GetConnectionRef() ConnectionRef {
	return o.Spec.ConnectionRef.withDefault(o.Namespace)
}

// StatusConditions returns a pointer to the status conditions for updates.
func (o *HarborRetentionPolicy) StatusConditions() *[]metav1.Condition { return &o.Status.Conditions }

// SetObservedGeneration records the generation the status reflects.
func (o *HarborRetentionPolicy) SetObservedGeneration(g int64) { o.Status.ObservedGeneration = g }

// GetSyncPeriod returns spec.syncPeriod.
func (o *HarborRetentionPolicy) GetSyncPeriod() *metav1.Duration { return o.Spec.SyncPeriod }

// GetDeletionPolicy returns spec.deletionPolicy.
func (o *HarborRetentionPolicy) GetDeletionPolicy() DeletionPolicy { return o.Spec.DeletionPolicy }

// GetProjectReference returns the project the resource belongs to.
func (o *HarborRetentionPolicy) GetProjectReference() ProjectReference {
	return o.Spec.ProjectReference
}

// GetConditions returns the status conditions.
func (o *HarborGarbageCollection) GetConditions() []metav1.Condition { return o.Status.Conditions }

// GetConnectionRef returns spec.connectionRef with the namespace defaulted.
func (o *HarborGarbageCollection) GetConnectionRef() ConnectionRef {
	return o.Spec.ConnectionRef.withDefault(o.Namespace)
}

// StatusConditions returns a pointer to the status conditions for updates.
func (o *HarborGarbageCollection) StatusConditions() *[]metav1.Condition { return &o.Status.Conditions }

// SetObservedGeneration records the generation the status reflects.
func (o *HarborGarbageCollection) SetObservedGeneration(g int64) { o.Status.ObservedGeneration = g }

// GetSyncPeriod returns spec.syncPeriod.
func (o *HarborGarbageCollection) GetSyncPeriod() *metav1.Duration { return o.Spec.SyncPeriod }

// GetDeletionPolicy returns spec.deletionPolicy.
func (o *HarborGarbageCollection) GetDeletionPolicy() DeletionPolicy { return o.Spec.DeletionPolicy }

// withDefault fills in the namespace of a reference from the referencing object.
func (r ConnectionRef) withDefault(ns string) ConnectionRef {
	if r.Namespace == "" {
		r.Namespace = ns
	}
	return r
}

// Key renders the reference as "<namespace>/<name>".
func (r ConnectionRef) Key() string { return r.Namespace + "/" + r.Name }
