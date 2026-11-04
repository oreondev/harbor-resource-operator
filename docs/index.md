---
title: Harbor Resource Operator
description: Manage Harbor projects, robot accounts, replication, members, labels, webhooks, retention and garbage collection as Kubernetes resources.
---

# Harbor Resource Operator

The Harbor Resource Operator manages configuration **inside** a running [Harbor](https://goharbor.io) registry as Kubernetes resources. You declare projects, robot accounts, replication endpoints and policies, project members, labels, immutable tag rules, webhooks, tag retention and the garbage collection schedule in YAML, and the operator keeps Harbor in line with them.

It does not install or upgrade Harbor; use the [Harbor Helm chart](https://github.com/goharbor/harbor-helm) for that. The operator talks to Harbor's REST API v2.0 only and depends on no other operator.

<div class="grid cards" markdown>

- **[Install](install.md)**: Helm, a single manifest, or OperatorHub.
- **[Quick start](quickstart.md)**: connect a Harbor and create a project with a robot account.
- **[How it behaves](behaviour.md)**: managed fields, drift correction, adoption, deletion.
- **[Reference](reference/harborconnection.md)**: every field of the eleven kinds.

</div>

## Why

- **Harbor as code.** Projects, quotas, robot accounts and replication live in Git next to the workloads that use them, and Argo CD or Flux apply them like any other manifest.
- **Pull secrets that look after themselves.** A `HarborRobotAccount` writes its credentials to a Secret (`kubernetes.io/dockerconfigjson` if you like), refreshes it if it is lost and rotates it before the robot expires.
- **No drift.** The operator re-reads Harbor on a sync period and restores what you declared, with a `DriftCorrected` event.
- **Safe by default.** Projects are kept when their Kubernetes object is deleted, and never deleted while they still hold repositories.

## Kinds

| Kind | What it manages |
|---|---|
| [`HarborConnection`](reference/harborconnection.md) | URL and credentials of one Harbor; which namespaces may use it |
| [`HarborProject`](reference/harborproject.md) | Project, public flag, storage quota, scan metadata, CVE allowlist, proxy cache |
| [`HarborRobotAccount`](reference/harborrobotaccount.md) | Project or system robot with permissions; credentials in a Secret; rotation |
| [`HarborRegistry`](reference/harborregistry.md) | Replication endpoint (another Harbor, ECR, Docker Hub, ...) |
| [`HarborReplicationPolicy`](reference/harborreplicationpolicy.md) | Push or pull replication with filters and trigger; the latest execution |
| [`HarborProjectMember`](reference/harborprojectmember.md) | A user or an LDAP, HTTP or OIDC group with a project role |
| [`HarborLabel`](reference/harborlabel.md) | Global or project label |
| [`HarborImmutableRule`](reference/harborimmutablerule.md) | Immutable tag rule |
| [`HarborWebhook`](reference/harborwebhook.md) | Project webhook policy with HTTP or Slack targets |
| [`HarborRetentionPolicy`](reference/harborretentionpolicy.md) | Tag retention rules and schedule |
| [`HarborGarbageCollection`](reference/harborgarbagecollection.md) | The system garbage collection schedule; the last run |

## Supported versions

| | Tested |
|---|---|
| Harbor | 2.12, 2.13, 2.14, 2.15 (end-to-end with the official chart: 2.15 on every push, all four nightly) |
| Kubernetes | 1.30 and later: 1.30, 1.34 and 1.37 end-to-end on kind, 1.37 envtest |
| OpenShift | not tested yet; the pod meets the restricted Pod Security Standard |
| Architectures | linux/amd64, linux/arm64, linux/s390x, linux/ppc64le |

The API is `v1alpha1`: all eleven kinds are implemented and tested, and changes inside `v1alpha1` are additive where possible.

!!! note
    This is an independent community project, maintained by [Oreon IT](https://www.oreonit.com). It is not affiliated with or endorsed by the Harbor project or the CNCF; Harbor is a trademark of The Linux Foundation.
