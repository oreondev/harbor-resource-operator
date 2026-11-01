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

package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	harborv1alpha1 "github.com/oreondev/harbor-resource-operator/api/v1alpha1"
)

// refersToConnection reports whether a resource in ns with the given
// connectionRef points at the connection object conn.
func refersToConnection(ref harborv1alpha1.ConnectionRef, ns string, conn client.Object) bool {
	return ref.Name == conn.GetName() && defaultName(ref.Namespace, ns) == conn.GetNamespace()
}

// enqueueProjectsForConnection maps a HarborConnection event to every HarborProject using it.
func enqueueProjectsForConnection(c client.Client) func(context.Context, client.Object) []reconcile.Request {
	return func(ctx context.Context, conn client.Object) []reconcile.Request {
		var list harborv1alpha1.HarborProjectList
		if err := c.List(ctx, &list); err != nil {
			return nil
		}
		var out []reconcile.Request
		for i := range list.Items {
			if refersToConnection(list.Items[i].Spec.ConnectionRef, list.Items[i].Namespace, conn) {
				out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
			}
		}
		return out
	}
}

// enqueueProjectsForRegistry maps a HarborRegistry event to proxy-cache projects using it.
func enqueueProjectsForRegistry(c client.Client) func(context.Context, client.Object) []reconcile.Request {
	return func(ctx context.Context, reg client.Object) []reconcile.Request {
		var list harborv1alpha1.HarborProjectList
		if err := c.List(ctx, &list, client.InNamespace(reg.GetNamespace())); err != nil {
			return nil
		}
		var out []reconcile.Request
		for i := range list.Items {
			if pc := list.Items[i].Spec.ProxyCache; pc != nil && pc.RegistryRef.Name == reg.GetName() {
				out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
			}
		}
		return out
	}
}

// enqueueRobotsForConnection maps a HarborConnection event to every HarborRobotAccount using it.
func enqueueRobotsForConnection(c client.Client) func(context.Context, client.Object) []reconcile.Request {
	return func(ctx context.Context, conn client.Object) []reconcile.Request {
		var list harborv1alpha1.HarborRobotAccountList
		if err := c.List(ctx, &list); err != nil {
			return nil
		}
		var out []reconcile.Request
		for i := range list.Items {
			if refersToConnection(list.Items[i].Spec.ConnectionRef, list.Items[i].Namespace, conn) {
				out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
			}
		}
		return out
	}
}

// enqueueRobotsForProject maps a HarborProject event to the robots that reference it.
func enqueueRobotsForProject(c client.Client) func(context.Context, client.Object) []reconcile.Request {
	return func(ctx context.Context, proj client.Object) []reconcile.Request {
		var list harborv1alpha1.HarborRobotAccountList
		if err := c.List(ctx, &list, client.InNamespace(proj.GetNamespace())); err != nil {
			return nil
		}
		var out []reconcile.Request
		for i := range list.Items {
			if ref := list.Items[i].Spec.ProjectRef; ref != nil && ref.Name == proj.GetName() {
				out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
			}
		}
		return out
	}
}

// enqueueRegistriesForConnection maps a HarborConnection event to every HarborRegistry using it.
func enqueueRegistriesForConnection(c client.Client) func(context.Context, client.Object) []reconcile.Request {
	return func(ctx context.Context, conn client.Object) []reconcile.Request {
		var list harborv1alpha1.HarborRegistryList
		if err := c.List(ctx, &list); err != nil {
			return nil
		}
		var out []reconcile.Request
		for i := range list.Items {
			if refersToConnection(list.Items[i].Spec.ConnectionRef, list.Items[i].Namespace, conn) {
				out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
			}
		}
		return out
	}
}

// enqueuePoliciesForConnection maps a HarborConnection event to every HarborReplicationPolicy using it.
func enqueuePoliciesForConnection(c client.Client) func(context.Context, client.Object) []reconcile.Request {
	return func(ctx context.Context, conn client.Object) []reconcile.Request {
		var list harborv1alpha1.HarborReplicationPolicyList
		if err := c.List(ctx, &list); err != nil {
			return nil
		}
		var out []reconcile.Request
		for i := range list.Items {
			if refersToConnection(list.Items[i].Spec.ConnectionRef, list.Items[i].Namespace, conn) {
				out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
			}
		}
		return out
	}
}

// enqueuePoliciesForRegistry maps a HarborRegistry event to the policies that reference it.
func enqueuePoliciesForRegistry(c client.Client) func(context.Context, client.Object) []reconcile.Request {
	return func(ctx context.Context, reg client.Object) []reconcile.Request {
		var list harborv1alpha1.HarborReplicationPolicyList
		if err := c.List(ctx, &list, client.InNamespace(reg.GetNamespace())); err != nil {
			return nil
		}
		var out []reconcile.Request
		for i := range list.Items {
			p := &list.Items[i]
			if (p.Spec.DestRegistryRef != nil && p.Spec.DestRegistryRef.Name == reg.GetName()) ||
				(p.Spec.SrcRegistryRef != nil && p.Spec.SrcRegistryRef.Name == reg.GetName()) {
				out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(p)})
			}
		}
		return out
	}
}

// requestFor builds a reconcile request.
func requestFor(ns, name string) reconcile.Request {
	return reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: name}}
}

var _ = requestFor
var _ ctrl.Manager
