#!/usr/bin/env bash
# Copyright 2026 Oreon IT.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Bumps every versioned file to X.Y.Z for a release: Makefile VERSION,
# dist/install.yaml, the chart, the OLM bundle, the README install commands
# and CHANGELOG.md (Unreleased becomes "X.Y.Z - today"). Run it on a
# chore/release-X.Y.Z branch from develop; it does not commit.
set -euo pipefail

new="${1:?usage: release-bump.sh X.Y.Z}"
cd "$(dirname "$0")/.."

if ! echo "$new" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
  echo "version $new is not MAJOR.MINOR.PATCH" >&2; exit 1
fi
old=$(sed -n 's/^VERSION ?= //p' Makefile)
if [ "$old" = "$new" ]; then
  echo "VERSION is already $new" >&2; exit 1
fi
if git rev-parse -q --verify "refs/tags/v$new" > /dev/null; then
  echo "tag v$new already exists" >&2; exit 1
fi
if ! ./hack/release-notes.sh Unreleased > /dev/null 2>&1; then
  echo "CHANGELOG.md has nothing under Unreleased; add the release notes first" >&2; exit 1
fi

sed -i "s/^VERSION ?= .*/VERSION ?= $new/" Makefile
sed -i -e "s/^version: .*/version: $new/" -e "s/^appVersion: .*/appVersion: \"$new\"/" charts/chart/Chart.yaml
sed -i "s/^    tag: .*/    tag: \"$new\"/" charts/chart/values.yaml
# Only the install and verify commands carry the version.
old_re=${old//./\\.}
sed -i -E "/(helm install|releases\/download|cosign verify|replace [0-9.]+ with)/ s/(^|[^0-9.])$old_re([^0-9]|$)/\1$new\2/g" README.md
sed -i "s/^## Unreleased$/## Unreleased\n\n## $new - $(date -u +%F)/" CHANGELOG.md

make build-installer VERSION="$new"
make bundle VERSION="$new"
make bundle-validate

echo "Bumped $old to $new. Check git diff, update docs/OPERATORHUB.md after the OperatorHub submission, then open the release PR."
