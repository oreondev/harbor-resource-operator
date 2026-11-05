# Changelog

All notable changes are listed here, newest first. Versions follow [semantic versioning](https://semver.org); while the API is `v1alpha1`, a minor version may change it. Each release also has a [GitHub release](https://github.com/oreondev/harbor-resource-operator/releases) with the image digest and install files.

## Unreleased

- Documentation site at <https://www.oreonit.com/harbor-operator/>, with a reference page for every kind generated from the CRDs and samples, and these release notes.
- `HarborProjectMember` for a user that does not exist in Harbor now reports `Ready=False` with reason `UserNotFound` instead of a generic `HarborError`, and adds the member at the next sync once the user exists.

## 0.1.5 - 2026-11-04

### Fixed

- Existing project robot accounts are found by name. Harbor's `GET /robots` lists only system robots unless the query names the project, so `adoptExisting` never worked for project robots, and a robot whose stored ID was lost failed on create instead of being found again ([#16](https://github.com/oreondev/harbor-resource-operator/pull/16)).

### Changed

- The Harbor client moved from `internal/harbor` to `pkg/harbor` so other modules can import it; it now also lists projects, repositories and artifacts and reads vulnerability reports ([#14](https://github.com/oreondev/harbor-resource-operator/pull/14), [#15](https://github.com/oreondev/harbor-resource-operator/pull/15)). The operator's behaviour does not change.

## 0.1.4 - 2026-11-03

### Changed

- The OLM bundle requires Kubernetes 1.30 or later (was 1.34). The end-to-end suite passes on Kubernetes 1.30 with Harbor 2.15, and the nightly run now covers 1.30 ([#12](https://github.com/oreondev/harbor-resource-operator/pull/12)).

## 0.1.3 - 2026-11-02

### Changed

- The OLM bundle links the maintainer's website ([#10](https://github.com/oreondev/harbor-resource-operator/pull/10)). No change to the operator.

## 0.1.2 - 2026-11-02

### Changed

- The OLM bundle names Oreon IT as provider ([#8](https://github.com/oreondev/harbor-resource-operator/pull/8)). No change to the operator.

## 0.1.1 - 2026-11-02

### Added

- OLM bundle for OperatorHub, published as the signed image `ghcr.io/oreondev/harbor-resource-operator-bundle` and attached to the GitHub release ([#3](https://github.com/oreondev/harbor-resource-operator/pull/3)).
- Every kind and field has a description, shown by `kubectl explain` and on OperatorHub ([#4](https://github.com/oreondev/harbor-resource-operator/pull/4)).

## 0.1.0 - 2026-11-01

First release.

- Eleven kinds: `HarborConnection`, `HarborProject`, `HarborRobotAccount`, `HarborRegistry`, `HarborReplicationPolicy`, `HarborProjectMember`, `HarborLabel`, `HarborImmutableRule`, `HarborWebhook`, `HarborRetentionPolicy` and `HarborGarbageCollection`.
- Adoption of existing Harbor objects, `Retain` or `Delete` deletion policies, drift correction, robot secret rotation, per-connection rate limiting, namespace restrictions and Prometheus metrics.
- Multi-arch image signed with cosign, Helm chart and `install.yaml`.
- Tested end to end against Harbor 2.12, 2.13, 2.14 and 2.15.
