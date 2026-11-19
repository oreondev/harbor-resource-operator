# Harbor Resource Operator

A Kubernetes operator that manages objects inside a running [Harbor](https://goharbor.io) registry through its REST API: projects, robot accounts, replication endpoints and policies, project members, labels, immutable tag rules, webhooks, tag retention and the garbage collection schedule.

It does not install, upgrade or back up Harbor. Use the [official Harbor chart](https://github.com/goharbor/harbor-helm) for that, and this operator for everything you would otherwise click together in the Harbor UI.

- Documentation: <https://www.oreonit.com/harbor-operator/>
- Source: <https://github.com/oreondev/harbor-resource-operator>
- Supported Harbor versions: 2.12 to 2.15

## Install

```bash
helm install harbor-resource-operator oci://ghcr.io/oreondev/charts/harbor-resource-operator \
  --namespace harbor-resource-operator-system --create-namespace
```

Add `--version X.Y.Z` to pin a release. The chart installs the CRDs, the controller and its RBAC.

## Point it at a Harbor

```bash
kubectl create namespace harbor-system
kubectl -n harbor-system create secret generic prod-harbor-admin \
  --from-literal=username='<harbor user>' --from-literal=password='<password>'
```

```yaml
apiVersion: harbor.oreonit.com/v1alpha1
kind: HarborConnection
metadata:
  name: prod-harbor
  namespace: harbor-system
spec:
  url: https://harbor.example.com
  credentialsSecretRef:
    name: prod-harbor-admin
  allowedNamespaces: ["*"]
```

Then create projects, robot accounts and the other kinds as custom resources. The [documentation](https://www.oreonit.com/harbor-operator/) has a reference page and examples for every kind.

## What it manages

| Kind | Manages |
|---|---|
| `HarborConnection` | The Harbor endpoint and credentials the other kinds use |
| `HarborProject` | Projects, with quota and settings |
| `HarborRobotAccount` | System and project robot accounts; the secret is written to a Kubernetes Secret |
| `HarborRegistry`, `HarborReplicationPolicy` | Replication endpoints and policies |
| `HarborProjectMember` | Users and groups in a project |
| `HarborLabel` | Global and project labels |
| `HarborImmutableRule` | Immutable tag rules |
| `HarborWebhook` | Project webhook policies |
| `HarborRetentionPolicy` | Tag retention rules and schedule |
| `HarborGarbageCollection` | The garbage collection schedule |

Every kind supports adopting an object that already exists in Harbor, a `Retain` or `Delete` deletion policy, drift correction, `Ready` conditions and Kubernetes events.

## Values

The defaults suit most clusters. See [`values.yaml`](https://github.com/oreondev/harbor-resource-operator/blob/main/charts/chart/values.yaml) for the image, resources, metrics and other settings.

## Verify the chart

Images and charts are signed with cosign (keyless, GitHub Actions OIDC). The project README shows the verify command.

## Licence

Apache-2.0. Maintained by [Oreon IT](https://www.oreonit.com).
