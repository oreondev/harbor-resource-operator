# Contributing

## Branches

| Branch | Holds | Changes arrive by |
|---|---|---|
| `main` | the latest release; every commit on it is tagged or about to be | merge from `develop` (release) or a `hotfix/...` branch |
| `develop` | the next release, tested nightly against Harbor 2.12 to 2.15 | squash-merged pull requests from work branches |
| `feat/...`, `fix/...`, `chore/...`, `docs/...` | one change each, cut from `develop` | your commits |
| `hotfix/...` | an urgent fix to a release, cut from `main` | your commits |

Nobody pushes to `main` or `develop` directly.

## Making a change

1. Branch from `develop`: `git switch -c feat/short-name develop`.
2. Run `make test` and `make lint` before you push (lint must report 0 issues). After editing `api/v1alpha1/*_types.go`, run `make manifests generate` and commit the generated files. For a change users notice (a feature, a fix, a changed default), add a line under `Unreleased` in `CHANGELOG.md`.
3. Open a pull request into `develop`. CI runs unit tests, lint, the chart test and the e2e suite against the newest supported Harbor; all must pass.
4. The pull request is squash-merged, so each change is one commit on `develop`.

## Releases

1. On a `chore/release-X.Y.Z` branch from `develop`, with the release notes under `Unreleased` in `CHANGELOG.md`, run `./hack/release-bump.sh X.Y.Z`. It sets `VERSION ?= X.Y.Z` in the `Makefile`, bumps the chart (`Chart.yaml`, image tag in `values.yaml`) and the README install commands, turns `Unreleased` into `X.Y.Z - YYYY-MM-DD`, then runs `make build-installer`, `make bundle` and `make bundle-validate`. It refuses an empty `Unreleased`. If the manifests changed since the last release, also regenerate the chart with `kubebuilder edit --plugins=helm/v2-alpha --force --output-dir=charts` and revert what it does to `.github/workflows/test-chart.yml`. Set `spec.replaces` in the CSV base only if `ci.yaml` uses `replaces-mode`; with `semver-mode` leave it out. Open the pull request into `develop` and squash-merge it.
2. When `develop` is stable (green nightly matrix, nothing half-done), merge it into `main` with a merge commit, not a squash, so the two branches keep a shared history.
3. That merge is the release. `.github/workflows/tag-release.yml` sees a `VERSION` without a tag, creates the annotated tag `vX.Y.Z` on the merge commit ([semantic versioning](https://semver.org); while the API is `v1alpha1`, minor versions may change it), and starts `release.yml` and `docs.yml` on it. `release.yml` checks that the tag is on `main` and matches `VERSION` and `CHANGELOG.md`, runs the tests, then publishes:
   - `ghcr.io/oreondev/harbor-resource-operator:X.Y.Z` and `:X.Y` for amd64, arm64, s390x and ppc64le, signed with cosign (keyless), with SBOM and provenance attestations;
   - the Helm chart at `oci://ghcr.io/oreondev/charts/harbor-resource-operator`, signed;
   - the OLM bundle image `ghcr.io/oreondev/harbor-resource-operator-bundle:vX.Y.Z`, signed, built from the committed `bundle/` (the job fails if `bundle/` is not for `X.Y.Z`);
   - a GitHub release with `install.yaml`, the chart archive, the bundle archive, the `CHANGELOG.md` section and generated notes.

   `docs.yml` publishes the docs as version `X.Y` and moves `latest` to it. A merge into `main` that keeps an already tagged `VERSION` releases nothing; to republish the docs of the current release after a docs fix, run the Docs workflow by hand with that version. Pushing a `vX.Y.Z` tag by hand also still releases.
4. Submit the bundle to OperatorHub by hand, as described in [`docs/OPERATORHUB.md`](docs/OPERATORHUB.md), and record it there.

Pushes to `develop` also publish an untested-for-release edge image tagged `develop` and `sha-<commit>`, and the `dev` docs.

## Hotfixes

1. Branch `hotfix/short-name` from `main`, fix, add the change under `Unreleased` in `CHANGELOG.md`, run `./hack/release-bump.sh X.Y.Z+1`, and open a pull request into `main`.
2. Merging tags and publishes the patch release as above; then merge `main` back into `develop`.
