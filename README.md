# Harbor Resource Operator

A Kubernetes operator that manages configuration **inside** a running [Harbor](https://goharbor.io) registry as Kubernetes resources: projects, robot accounts, replication endpoints and policies, project members, labels, immutable tag rules, webhooks, tag retention and the garbage collection schedule.

It does **not** install or upgrade Harbor. Use the [Harbor Helm chart](https://github.com/goharbor/harbor-helm) for that. This operator talks to Harbor's REST API v2.0 only and has no dependency on other operators. See [docs/REQUIREMENTS.md](docs/REQUIREMENTS.md) for the full design.

Documentation: **https://www.oreonit.com/harbor-operator/**

This is an independent community project. It is not affiliated with or endorsed by the Harbor project or the CNCF; Harbor is a trademark of The Linux Foundation.

## Supported versions

| | Tested |
|---|---|
| Harbor | 2.12, 2.13, 2.14, 2.15 (end-to-end with the official chart: 2.15 on every push, all four nightly) |
| Kubernetes | 1.30 and later: 1.30, 1.34 and 1.37 end-to-end on kind (1.30 nightly), 1.37 envtest |
| OpenShift | not tested yet; the pod meets the restricted Pod Security Standard |
| Architectures | linux/amd64, linux/arm64, linux/s390x, linux/ppc64le |

## Kinds

| Kind | What it manages | Harbor API |
|---|---|---|
| `HarborConnection` | URL and credentials of one Harbor; which namespaces may use it | `/systeminfo`, `/users/current` |
| `HarborProject` | Project, public flag, storage quota, scan and content-trust metadata, CVE allowlist, proxy cache | `/projects`, `/quotas` |
| `HarborRobotAccount` | Project or system robot with permissions; credentials written to a Secret (Opaque or `kubernetes.io/dockerconfigjson`); rotation | `/robots` |
| `HarborRegistry` | Replication endpoint (another Harbor, ECR, Docker Hub, ...) with credentials from a Secret | `/registries` |
| `HarborReplicationPolicy` | Push or pull policy with filters, trigger, options; mirrors the latest execution | `/replication/policies`, `/replication/executions` |
| `HarborProjectMember` | A user or an LDAP, HTTP or OIDC group with a project role | `/projects/{id}/members` |
| `HarborLabel` | Global or project label with colour and description | `/labels` |
| `HarborImmutableRule` | Immutable tag rule: repository and tag patterns | `/projects/{id}/immutabletagrules` |
| `HarborWebhook` | Project webhook policy: event types, HTTP or Slack targets, auth header from a Secret | `/projects/{id}/webhook/policies` |
| `HarborRetentionPolicy` | The project's tag retention policy: up to 15 rules and a cron schedule | `/retentions` |
| `HarborGarbageCollection` | The system garbage collection schedule, one per connection; mirrors the last run | `/system/gc/schedule`, `/system/gc` |

Every kind supports `adoptExisting` (take over an object that already exists in Harbor), `deletionPolicy` (`Retain` or `Delete`; projects and garbage collection default to `Retain`; projects are never deleted while they hold repositories), drift correction on a sync period, `Ready` conditions with a reason, and Kubernetes events.

## Quick start

```bash
# 1. Install the operator (replace 0.1.6 with the latest release)
helm install harbor-resource-operator oci://ghcr.io/oreondev/charts/harbor-resource-operator --version 0.1.6 \
  --namespace harbor-resource-operator-system --create-namespace
# or: kubectl apply -f https://github.com/oreondev/harbor-resource-operator/releases/download/v0.1.6/install.yaml

# 2. Point it at a Harbor
kubectl create namespace harbor-system
kubectl -n harbor-system create secret generic prod-harbor-admin \
  --from-literal=username='<harbor user>' --from-literal=password='<password>'
kubectl apply -f - <<'EOF'
apiVersion: harbor.oreonit.com/v1alpha1
kind: HarborConnection
metadata: { name: prod-harbor, namespace: harbor-system }
spec:
  url: https://harbor.example.com
  credentialsSecretRef: { name: prod-harbor-admin }
  allowedNamespaces: ["*"]
EOF
kubectl -n harbor-system get hconn

# 3. Declare a project and a robot whose pull secret your Deployments can use
kubectl create namespace team-payments
kubectl apply -f https://raw.githubusercontent.com/oreondev/harbor-resource-operator/main/config/samples/harbor_v1alpha1_harborproject.yaml
kubectl apply -f https://raw.githubusercontent.com/oreondev/harbor-resource-operator/main/config/samples/harbor_v1alpha1_harborrobotaccount.yaml
kubectl -n team-payments get hproj,hrobot
```

The connection's account needs enough Harbor permissions for the kinds you use: system-level kinds (projects, registries, replication, global labels, garbage collection, system robots) need a Harbor administrator or a system robot account with those permissions. The end-to-end suite runs with an administrator; a least-privilege robot setup is not verified yet. Keep the credentials Secret in a namespace only the platform team can read, and use `allowedNamespaces` to choose which namespaces may use the connection.

Images are signed with cosign (keyless). To verify:

```bash
cosign verify ghcr.io/oreondev/harbor-resource-operator:0.1.6 \
  --certificate-identity-regexp '^https://github.com/oreondev/harbor-resource-operator/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

More examples live in [config/samples](config/samples).

## How it behaves

- **Managed fields only.** Fields you leave unset in `spec` are left as Harbor has them.
- **Drift correction.** Every `syncPeriod` (default 10 minutes, set on the connection or per resource) the operator re-reads Harbor and restores declared values, emitting a `DriftCorrected` event.
- **Safe deletion.** `HarborProject` defaults to `deletionPolicy: Retain`. With `Delete`, Harbor's `_deletable` check runs first and the object stays with reason `ProjectNotEmpty` until the repositories are gone. Repositories are never deleted by the operator.
- **Robot secrets.** Harbor only returns a robot secret at creation. The operator stores it in the Secret immediately; if the Secret is deleted or loses its password, the robot secret is refreshed and the Secret rewritten. `rotation.period` refreshes on a schedule; `rotation.beforeExpiry` recreates the robot before it expires.
- **Multi-tenancy.** Resources reference a connection in another namespace only if that connection lists the namespace (or `*`) in `allowedNamespaces`.
- **Rate limiting.** One token bucket per connection (`requestsPerSecond`, default 10) shared by all controllers.
- **Project-scoped kinds** reference a `HarborProject` with `projectRef` (it must use the same connection) or name an existing Harbor project with `projectName`. The project and the member identity are immutable; recreate the object to change them.
- **One garbage collection schedule per Harbor.** If several `HarborGarbageCollection` objects point at one connection, the oldest wins and the others report `Duplicate`.
- **Single-namespace installs.** `--watch-namespaces=a,b` restricts the manager cache, and so the operator, to those namespaces.

## Metrics

Besides the controller-runtime reconcile metrics, the operator exports:

| Metric | Labels |
|---|---|
| `harbor_api_requests_total` | `connection`, `method`, `code` |
| `harbor_api_request_duration_seconds` | `connection`, `method` |
| `harbor_drift_corrections_total` | `kind` |
| `harbor_resources` | `kind`, `connection`, `ready` |
| `harbor_robot_expiration_timestamp_seconds` | `namespace`, `name`, `robot` |

Alert on robots that expire within a week with `harbor_robot_expiration_timestamp_seconds - time() < 7*86400`.

## Development

```bash
make test        # unit tests against an in-memory fake Harbor + envtest controller suite
make lint        # golangci-lint
make run         # run the operator against the current kubeconfig
make manifests generate   # after editing api/v1alpha1/*_types.go
make test-e2e    # kind cluster + real Harbor chart (HARBOR_CHART_VERSION=1.19.4 by default)
```

Branches, pull requests and releases are described in [CONTRIBUTING.md](CONTRIBUTING.md). To run your own build: `make docker-build docker-push IMG=<your image>`, then `make deploy IMG=<your image>`.

Tests use `pkg/harbor/fake`, an in-memory Harbor API v2.0 that mimics the endpoints, status codes, `Location` headers and error bodies the operator depends on. End-to-end tests install the official Harbor chart on kind and run against Harbor 2.15 on every push, and against 2.12 to 2.15 nightly.

## Status

`v1alpha1`: all eleven kinds are implemented and tested. The API may still change between minor versions; changes inside `v1alpha1` are additive where possible. Listed on [OperatorHub.io](https://operatorhub.io/operator/harbor-resource-operator). Planned next: an Artifact Hub listing for the chart, and GitOps examples for Argo CD and Flux.

## Security

Report vulnerabilities privately; see [SECURITY.md](SECURITY.md).

## Licence

[Apache-2.0](LICENSE).
