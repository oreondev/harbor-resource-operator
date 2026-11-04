---
title: Install
description: Install the Harbor Resource Operator with Helm, a single manifest, or OperatorHub, and verify the signed images.
---

# Install

The operator runs as one Deployment and needs Kubernetes 1.30 or later. Pick one of the methods below; all of them install the same signed image from `ghcr.io/oreondev/harbor-resource-operator`.

=== "Helm"

    ```bash
    helm install harbor-resource-operator oci://ghcr.io/oreondev/charts/harbor-resource-operator \
      --version {{ version }} \
      --namespace harbor-resource-operator-system --create-namespace
    ```

    Useful values:

    | Value | Default | Meaning |
    |---|---|---|
    | `manager.args` | `[--leader-elect]` | Manager flags; add `--watch-namespaces=a,b` to restrict the operator to some namespaces |
    | `manager.replicas` | `1` | More replicas wait as hot standbys (leader election) |
    | `crd.keep` | `true` | Keep the CRDs, and so your objects, on `helm uninstall` |
    | `metrics.enabled` | `true` | Serve `/metrics` on port 8443 (HTTPS, authenticated) |
    | `prometheus.enabled` | `false` | Create a ServiceMonitor (needs the Prometheus operator) |
    | `networkPolicy.enabled` | `false` | Restrict ingress to the manager |

=== "Manifest"

    ```bash
    kubectl apply -f https://github.com/oreondev/harbor-resource-operator/releases/download/v{{ version }}/install.yaml
    ```

    This installs the CRDs, RBAC and the manager into `harbor-resource-operator-system`.

=== "OperatorHub"

    The operator is listed on [OperatorHub.io](https://operatorhub.io/operator/harbor-resource-operator). With [OLM](https://olm.operatorframework.io) installed:

    ```bash
    kubectl create -f https://operatorhub.io/install/harbor-resource-operator.yaml
    ```

    OLM then upgrades the operator when a new version is published.

## Verify the image

Images are signed with cosign (keyless) by the release workflow, and carry an SBOM and provenance:

```bash
cosign verify ghcr.io/oreondev/harbor-resource-operator:{{ version }} \
  --certificate-identity-regexp '^https://github.com/oreondev/harbor-resource-operator/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Upgrade

Upgrade with `helm upgrade`, by applying the newer `install.yaml`, or let OLM do it. Objects keep their Harbor IDs across upgrades; the operator finds what it created by the stored ID first.

## Uninstall

Delete your Harbor resources first if you want their `deletionPolicy` to run; otherwise the finalizers keep them until the operator is back. Then:

```bash
helm uninstall harbor-resource-operator --namespace harbor-resource-operator-system
```

With `crd.keep: true` the CRDs and your objects stay in the cluster; nothing is deleted in Harbor.

## Next

[Connect a Harbor and create your first project](quickstart.md).
