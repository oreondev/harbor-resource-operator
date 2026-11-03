# Harbor Resource Operator: requirements

Version 0.1, 2026-11-01. Status: milestones 1 and 2 are implemented and covered by unit, envtest and end-to-end tests against Harbor 2.12 to 2.15. Milestone 3 (distribution) is in progress.

## 1. Purpose and positioning

A Kubernetes operator that manages configuration **inside** a running Harbor registry (projects, robot accounts, replication endpoints and policies, later retention, immutability, webhooks, members, labels) as Kubernetes resources, so that registry configuration lives in Git next to the workloads that use it.

It deliberately does **not** deploy or upgrade Harbor itself. Harbor's own Helm chart does that and is maintained by the Harbor team. The archived official operator was hard to maintain because it re-implemented the chart in Go for every Harbor component and embedded three third-party operators; this project is the opposite shape: one bounded job against Harbor's versioned REST API v2.0, no third-party operators, no conversion webhooks in v1alpha1.


## 2. Goals

- G1. Declarative, idempotent management of Harbor resources from any namespace, against one or many Harbor instances.
- G2. Drift correction: if someone changes a managed field in the Harbor UI, the operator restores the declared value on the next sync.
- G3. Robot account credentials delivered to workloads as Kubernetes Secrets, including ready-to-use image pull secrets, with rotation.
- G4. Safe by default: nothing in Harbor is deleted unless the resource says so; projects are retained on deletion by default.
- G5. Small and bounded enough for a single maintainer to keep current with Kubernetes and Harbor releases.
- G6. Works on upstream Kubernetes and OpenShift, installable by Helm or OLM.

## 3. Non-goals

- Installing, upgrading, backing up or scaling Harbor (use the Harbor Helm chart).
- Managing Harbor users, LDAP/OIDC settings or system configuration (except garbage collection schedule in milestone 2).
- Mirroring image contents, scanning, or acting on vulnerability reports.
- Multi-cluster federation of resources.
- A web UI.

## 4. Users and use cases

| User | Use case |
|---|---|
| Platform engineer | Creates a `HarborProject` per team via GitOps (Argo CD or Flux) with quota, auto-scan and public flag; teams get a project without a ticket |
| Application team | Declares a `HarborRobotAccount` with pull permission on their project; the operator writes an image pull Secret the Deployment references |
| CI/CD owner | Declares a push robot for the pipeline namespace with a 90-day duration and automatic rotation; the Secret is updated in place |
| Multi-site operator | Declares `HarborRegistry` endpoints (a second Harbor, ECR, Docker Hub) and `HarborReplicationPolicy` objects to push releases to edge sites on a schedule |
| Compliance owner (milestone 2) | Declares retention and immutability rules per project, and a webhook to the SIEM, as code |
| MSP or consultancy | Points one operator at several customer Harbors through separate `HarborConnection` objects with namespace restrictions |

## 5. Scope by milestone

| Milestone | Kinds | Deliverables |
|---|---|---|
| M1 (core) | HarborConnection, HarborProject, HarborRobotAccount, HarborRegistry, HarborReplicationPolicy | Controllers with adoption, drift correction, deletion policies, conditions, events, metrics; unit tests with a fake Harbor; envtest suite; Helm chart; multi-arch image |
| M2 (compliance) | HarborRetentionPolicy, HarborImmutableRule, HarborWebhook, HarborProjectMember, HarborLabel, HarborGarbageCollection | Same quality bar; e2e on kind with the Harbor chart in nightly CI |
| M3 (distribution) | none | OLM bundle, OperatorHub and OpenShift community PRs, Artifact Hub, docs site, example repository for Argo CD and Flux, release automation, SBOM and signed images |

## 6. API design

API group `harbor.oreonit.com`, version `v1alpha1`. All kinds are namespaced. Field names follow Kubernetes conventions (camelCase), not Harbor's snake_case. Every kind has `spec.connectionRef`, `spec.deletionPolicy`, `spec.adoptExisting`, and a `status` with `conditions`, `observedGeneration`, and the Harbor-side identifier.

### 6.1 HarborConnection

Holds the URL and credentials of one Harbor instance.

```yaml
apiVersion: harbor.oreonit.com/v1alpha1
kind: HarborConnection
metadata:
  name: prod-harbor
  namespace: harbor-system
spec:
  url: https://harbor.example.com
  credentialsSecretRef:          # keys: username, password (admin or a system robot with enough permissions)
    name: prod-harbor-admin
  caBundleSecretRef:             # optional, key: ca.crt
    name: prod-harbor-ca
  insecureSkipTLSVerify: false
  allowedNamespaces: ["*"]       # namespaces whose resources may reference this connection; default: own namespace only
  syncPeriod: 10m                # default resync for resources using this connection
status:
  harborVersion: v2.14.6
  conditions:
  - type: Ready
    status: "True"
    reason: Connected
```

Requirements:
- C1. The operator verifies the connection by calling `GET /systeminfo` and `GET /users/current` and reports the Harbor version and the authenticated account in status.
- C2. Resources in other namespaces may reference the connection only if their namespace matches `allowedNamespaces` (exact names or `*`). A disallowed reference sets condition `Ready=False, reason=ConnectionNotAllowed` and does nothing in Harbor.
- C3. Credentials are read at reconcile time, never cached across reconciles, never logged.
- C4. A broken connection marks dependent resources `Ready=False, reason=ConnectionUnavailable` and retries with exponential backoff capped at the sync period.

### 6.2 HarborProject

```yaml
apiVersion: harbor.oreonit.com/v1alpha1
kind: HarborProject
metadata:
  name: team-payments
  namespace: team-payments
spec:
  connectionRef: { name: prod-harbor, namespace: harbor-system }
  name: payments                  # Harbor project name; defaults to metadata.name
  public: false
  storageQuotaBytes: 53687091200  # optional; -1 for unlimited
  metadata:
    autoScan: true
    autoSbomGeneration: false
    preventVulnerable: true
    severity: critical            # none, low, medium, high, critical
    enableContentTrustCosign: false
    reuseSystemCVEAllowlist: true
  cveAllowlist:                   # optional
    expiresAt: "2027-01-31T00:00:00Z"
    items: ["CVE-2025-1234"]
  proxyCache:                     # optional; makes this a proxy cache project
    registryRef: { name: dockerhub-upstream }
  adoptExisting: false
  deletionPolicy: Retain          # Retain | Delete; default Retain
status:
  projectID: 42
  conditions: [...]
```

Requirements:
- P1. Create via `POST /projects`; look up by name with `HEAD /projects?project_name=`; update via `PUT /projects/{id}` and quota via `PUT /quotas/{id}`.
- P2. `registryRef` (proxy cache) is immutable after creation, as in Harbor; a change sets `Ready=False, reason=ImmutableField`.
- P3. On `deletionPolicy: Delete`, call `GET /projects/{id}/_deletable` first; if Harbor says the project is not deletable (it has repositories), keep the finalizer, set condition `Deleting=False, reason=ProjectNotEmpty`, emit a warning event, and retry on the sync period. Never delete repositories.
- P4. Only fields set in spec are managed. Unset optional fields are left as Harbor has them.

### 6.3 HarborRobotAccount

```yaml
apiVersion: harbor.oreonit.com/v1alpha1
kind: HarborRobotAccount
metadata:
  name: payments-ci
  namespace: team-payments
spec:
  connectionRef: { name: prod-harbor, namespace: harbor-system }
  name: payments-ci               # Harbor adds the robot$ prefix (and project+ for project-level)
  level: project                  # project | system
  projectRef: { name: team-payments }   # required for level: project (a HarborProject in this namespace) or use projectName
  description: CI pipeline push
  duration: 90                    # days; -1 never expires
  permissions:                    # for level: system, one entry per project namespace, or namespace "*"
  - namespace: payments
    access:
    - { resource: repository, action: push }
    - { resource: repository, action: pull }
    - { resource: artifact, action: read }
  secret:
    name: payments-ci-robot       # default <metadata.name>-robot
    type: kubernetes.io/dockerconfigjson   # or Opaque (keys: username, password, host)
    labels: {}
    annotations: {}
  rotation:
    period: 30d                   # optional; refreshes the secret via PATCH /robots/{id}
    beforeExpiry: 7d              # optional; rotate when expires_at is near
  disabled: false
  adoptExisting: false
  deletionPolicy: Delete
status:
  robotID: 17
  robotName: robot$payments+payments-ci
  expiresAt: "2027-01-29T10:00:00Z"
  lastRotation: "2026-10-31T10:00:00Z"
  secretRef: { name: payments-ci-robot }
  conditions: [...]
```

Requirements:
- R1. Harbor returns the robot secret only at creation (`POST /robots`) or refresh (`PATCH /robots/{id}`). The operator stores it in the Kubernetes Secret immediately and never in status, logs or events.
- R2. If the Secret is missing or lacks the `password` key on a later reconcile, the operator refreshes the robot secret and rewrites the Secret, and emits an event `SecretRecreated`.
- R3. Permissions are reconciled via `PUT /robots/{id}`; name, level and project are immutable (Harbor behaviour).
- R4. Adoption of an existing robot requires `adoptExisting: true` and triggers a secret refresh, because the existing secret is unknown; this is documented as rotating the credential.
- R5. For `kubernetes.io/dockerconfigjson`, the registry host is derived from the connection URL and the Secret carries an annotation with the robot name and expiry for humans.
- R6. The Secret is owned by the HarborRobotAccount (owner reference) and removed with it; `deletionPolicy: Retain` keeps the robot in Harbor but still removes the Secret.

### 6.4 HarborRegistry (replication endpoint)

```yaml
apiVersion: harbor.oreonit.com/v1alpha1
kind: HarborRegistry
metadata:
  name: edge-eu-west
  namespace: platform
spec:
  connectionRef: { name: prod-harbor, namespace: harbor-system }
  name: edge-eu-west
  type: harbor                    # harbor, docker-hub, docker-registry, aws-ecr, azure-acr, google-gcr, github-ghcr, quay, ... (Harbor's list)
  url: https://harbor.eu-west.example.com
  insecure: false
  credentialsSecretRef:           # optional; keys: accessKey, accessSecret
    name: edge-eu-west-creds
  credentialType: basic           # basic | oauth
  description: Edge site EU West
  adoptExisting: false
  deletionPolicy: Delete
status:
  registryID: 3
  health: healthy
  conditions: [...]
```

Requirements:
- E1. Before create or update, call `POST /registries/ping`; a failed ping sets `Ready=False, reason=PingFailed` with the Harbor message and still creates the registry only if `spec.createEvenIfUnhealthy: true`.
- E2. Status reports Harbor's registry `status` field on every sync.

### 6.5 HarborReplicationPolicy

```yaml
apiVersion: harbor.oreonit.com/v1alpha1
kind: HarborReplicationPolicy
metadata:
  name: releases-to-edge
  namespace: platform
spec:
  connectionRef: { name: prod-harbor, namespace: harbor-system }
  name: releases-to-edge
  direction: push                 # push: local -> destRegistryRef; pull: srcRegistryRef -> local
  destRegistryRef: { name: edge-eu-west }
  destNamespace: payments
  destNamespaceReplaceCount: -1
  filters:
  - { type: name, value: "payments/**" }
  - { type: tag, value: "v*" }
  - { type: resource, value: image }
  trigger:
    type: scheduled               # manual | scheduled | event_based
    cron: "0 0 2 * * *"
  override: true
  enabled: true
  replicateDeletion: false
  copyByChunk: false
  singleActiveReplication: true
  speedKBps: 0
  executeOnCreate: false          # POST /replication/executions once after creation
  adoptExisting: false
  deletionPolicy: Delete
status:
  policyID: 9
  lastExecution: { id: 120, status: Succeeded, startTime: ..., endTime: ... }
  conditions: [...]
```

Requirements:
- S1. The referenced HarborRegistry must be `Ready` and belong to the same connection; otherwise `Ready=False, reason=RegistryNotReady`.
- S2. Status mirrors the latest execution from `GET /replication/executions?policy_id=`.
- S3. Filters and trigger are reconciled in full (the whole list is declared).

### 6.6 Milestone 2 kinds

All project-scoped kinds take exactly one of `projectRef` (a HarborProject in the same namespace, on the same connection) or `projectName` (an existing Harbor project). Both the project and, for members, the identity are immutable (CEL transition rules). They default to `deletionPolicy: Delete`.

- HarborRetentionPolicy: `rules[]` (1 to 15) with `template` (`latestPushedK`, `latestPulledN`, `nDaysSinceLastPush`, `nDaysSinceLastPull`, `always`), `count` (required unless `always`), `repositories` and `tags` pattern selectors (`tags.untagged`, default true), `disabled`; `schedule` (six-field cron, empty for manual). Algorithm is always `or`. The policy is linked through the project's `retention_id` metadata; a stale pointer is cleared and a new policy created.
- HarborImmutableRule: `repositories` and `tags` pattern selectors (`decoration` matches or excludes, doublestar `pattern`), `disabled`. Harbor has no rule name, so a rule is found by stored ID and otherwise by identical selectors. Harbor's `priority` field is not exposed and is kept as Harbor has it on update.
- HarborWebhook: `name`, `description`, `eventTypes[]`, `targets[]` (1 to 8; `http` or `slack`, `address`, `authHeaderSecretRef`, `skipCertVerify`, `payloadFormat` Default or CloudEvents), `enabled`. Secret changes trigger a reconcile; status keeps only the resourceVersions of the referenced Secrets, never their values.
- HarborProjectMember: `user` (an existing Harbor user) or `group` (`name`, `type` ldap, http or oidc, `ldapGroupDN`), `role` (projectAdmin, maintainer, developer, guest, limitedGuest). Only the role is updated in place.
- HarborLabel: `name`, `color` (#RRGGBB), `description`, `scope: global | project` (project needs a project reference).
- HarborGarbageCollection: one effective object per connection (oldest wins, others report `Duplicate`); `schedule` (Hourly, Daily, Weekly, None or six-field cron), `deleteUntagged`, `workers` (1 to 5). Defaults to `deletionPolicy: Retain`; `Delete` sets the schedule to None. Status mirrors the next scheduled time and the last run.

## 7. Reconciliation semantics

- X1. Idempotent: every reconcile computes desired state from spec, reads actual state from Harbor by stored ID (status) or by name (first time), and applies the minimal update. No create when an ID is known; if the ID no longer exists in Harbor, the resource is recreated and an event `RecreatedInHarbor` is emitted.
- X2. Adoption: if no ID is stored and an object with the same name exists in Harbor, adopt only when `adoptExisting: true`; otherwise set `Ready=False, reason=AlreadyExists` and do nothing.
- X3. Managed fields: only fields present in spec are compared and written. This lets humans keep settings the spec does not mention.
- X4. Drift correction runs on every sync period (default from the connection, overridable per resource with `spec.syncPeriod`) and on every spec change. Corrections emit a `DriftCorrected` event naming the field.
- X5. Deletion: finalizer `harbor.oreonit.com/finalizer`. `Retain` removes the Kubernetes object and leaves Harbor untouched; `Delete` removes the Harbor object first, then the finalizer. Harbor 404 on delete counts as success.
- X6. Ordering: a resource whose dependency (connection, project, registry) is not Ready requeues after 30 seconds without calling Harbor.
- X7. Rate limiting: per-connection client with a token bucket (default 10 requests per second, burst 20) and a concurrency cap, so a large GitOps apply cannot overload Harbor.
- X8. Status: `conditions` use `metav1.Condition` with types `Ready`, `Synced`, `Deleting`; `observedGeneration` is set on every status write. Status writes use the status subresource.
- X9. Backoff: Harbor 5xx and network errors use controller-runtime's default exponential backoff; 4xx other than 404 and 409 do not retry until the spec changes (they are user errors) and are reported in the condition message.

## 8. Secrets and credentials

- K1. Connection credentials, registry credentials and webhook auth headers are read from Secrets by reference; the operator never embeds them in CRs.
- K2. Robot secrets are written to Secrets the operator creates and owns; existing Secrets with the same name but no owner reference are not overwritten (condition `SecretConflict`).
- K3. All Harbor API calls use HTTPS; `insecureSkipTLSVerify` is allowed but logged as a warning at startup and in the connection status.
- K4. Logs redact `Authorization` headers and any field named `secret`, `password`, `access_secret`, `auth_header`.

## 9. Multi-tenancy and RBAC

- T1. Namespaced kinds; tenants manage resources in their namespaces; the connection owner controls which namespaces may use a connection (`allowedNamespaces`).
- T2. The operator's own RBAC: get/list/watch on its kinds, update on status and finalizers, get/list/watch/create/update/delete Secrets it owns, create events. It needs no cluster-wide Secret read beyond that; referenced Secrets are read by name in the resource's own namespace (or the connection's namespace for connection credentials).
- T3. Optional watch-namespace restriction via `--watch-namespaces` for single-tenant installs.

## 10. Observability

- O1. Prometheus metrics on the controller-runtime endpoint: reconcile counts and durations per kind (built in), plus `harbor_api_requests_total{connection,method,code}`, `harbor_api_request_duration_seconds`, `harbor_resources{kind,connection,ready}`, `harbor_drift_corrections_total{kind}`, `harbor_robot_expiration_timestamp_seconds{namespace,name,robot}` (Unix time of expiry, following the Prometheus `_timestamp_seconds` convention; alert with `harbor_robot_expiration_timestamp_seconds - time() < 7*86400`).
- O2. Kubernetes events for create, update, drift correction, deletion, rotation, conflicts and Harbor errors.
- O3. Structured JSON logs with resource keys; `--zap-log-level` as usual.
- O4. `kubectl get` columns: Ready, Harbor ID, Age; robots also show Expires.

## 11. Compatibility

- V1. Harbor 2.12, 2.13, 2.14 and 2.15 tested end to end (2.15 on every push, all four nightly); API v2.0 only. New Harbor minors are added to the matrix within one month of release.
- V2. Kubernetes 1.30 and later: end-to-end tested on 1.30 (nightly, the minimum), 1.34 and 1.37, envtest on 1.37. OpenShift: not yet tested; the pod meets the restricted Pod Security Standard (non-root, read-only filesystem, no capabilities, no fixed UID), which the `restricted-v2` SCC requires.
- V3. CRD schema changes within v1alpha1 are additive only; a v1beta1 with a conversion webhook is a milestone after M3 and only if the API has to change.

## 12. Non-functional

- N1. The operator runs as a single replica with leader election enabled for upgrades; a second replica is allowed.
- N2. Resource usage under 100 MB memory and 0.1 CPU steady state for 1,000 managed resources.
- N3. A full resync of 1,000 resources completes within 5 minutes under the default rate limit.
- N4. Image: distroless, non-root, multi-arch (amd64, arm64, s390x, ppc64le), under 60 MB, SBOM attached, cosign-signed.

## 13. Testing

- Q1. Unit tests for the Harbor client against an `httptest` fake implementing the used endpoints, including error and pagination paths.
- Q2. envtest suites per controller covering create, adopt, drift, delete with both policies, connection unavailable, and robot secret recreation.
- Q3. End-to-end tests on kind with the Harbor Helm chart: the newest Harbor on every push, four Harbor minors nightly. They cover every kind, HTTPS with a custom CA, 300 objects on one rate-limited connection, metrics content and bulk deletion.
- Q4. Lint (golangci-lint), `go vet`, `govulncheck` in CI; coverage target 70 percent for controllers and 90 percent for the client.

## 14. Packaging and distribution

- D1. Helm chart in-repo (`charts/chart`), published on each release to `oci://ghcr.io/oreondev/charts/harbor-resource-operator`, and listed on Artifact Hub.
- D2. OLM bundle with CSV capability level "Seamless Upgrades" (level 2), `spec.links` to docs and source, `spec.provider.url`, icon; PRs to `k8s-operatorhub/community-operators` and the OpenShift community repository.
- D3. Releases by semantic version from `vX.Y.Z` tags on `main`: GitHub Actions builds the multi-arch image, signs it with cosign (keyless), attaches SBOM and provenance, pushes image and chart to ghcr.io and creates a GitHub release with `install.yaml`. Renovate for dependency bumps.
- D4. Repository name `harbor-resource-operator` to avoid confusion with the archived `harbor-operator`.

## 15. Documentation

- A docs site, mirrored as README in the repository: install (Helm, OLM), connection setup with a least-privilege robot instead of admin, one page per kind with full field reference, GitOps examples for Argo CD and Flux, migration guide for adopting an existing Harbor, troubleshooting by condition reason.

## 16. Maintenance and ownership

- Routine work: controller-runtime and Kubernetes dependency bumps with each Kubernetes minor (about every four months), Harbor minor compatibility runs, weekly issue triage. Harbor API v2.0 has been additive since 2.0 (2020); breaking changes are not expected inside 2.x.

## 17. Risks

| Risk | Mitigation |
|---|---|
| Harbor changes robot or replication API semantics in a minor | CI matrix across four Harbor versions; version-gated code paths if needed |
| Admin credentials in a Secret are a high-value target | Document and default to a system robot with only project/robot/registry/replication permissions; never require admin |
| Users expect the operator to install Harbor | Name, README first paragraph and OperatorHub description state the scope; link to the Harbor chart |
| Accidental project deletion | `Retain` default, `_deletable` check, no repository deletion ever |
| Maintainer burnout | Bounded scope, no third-party operators, no conversion webhooks in v1alpha1 |

## Appendix: Harbor API v2.0 endpoints used (from the 2.14.6 OpenAPI spec)

| Kind | Endpoints |
|---|---|
| Connection | `GET /systeminfo`, `GET /users/current` |
| Project | `HEAD /projects?project_name=`, `POST /projects`, `GET/PUT/DELETE /projects/{id}`, `GET /projects/{id}/_deletable`, `GET/PUT /quotas/{id}` (quota found via `GET /quotas?reference=project&reference_id=`) |
| RobotAccount | `GET /robots?q=name=`, `POST /robots`, `GET/PUT/DELETE /robots/{id}`, `PATCH /robots/{id}` (secret refresh) |
| Registry | `POST /registries/ping`, `GET /registries?q=name=`, `POST /registries`, `GET/PUT/DELETE /registries/{id}` |
| ReplicationPolicy | `GET /replication/policies?q=name=`, `POST /replication/policies`, `GET/PUT/DELETE /replication/policies/{id}`, `POST /replication/executions`, `GET /replication/executions?policy_id=` |
| M2 |
