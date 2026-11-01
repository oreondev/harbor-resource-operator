#!/usr/bin/env bash
# Sign an image or chart digest with keyless cosign, then verify the signature
# is in the registry. Rekor answers 409 when cosign retries an upload that
# already went through; a fresh cosign run uses a new ephemeral key, so retry.
set -euo pipefail

ref="$1"
identity="^https://github.com/${GITHUB_REPOSITORY}/\.github/workflows/release\.yml@"
issuer="https://token.actions.githubusercontent.com"

for attempt in 1 2 3; do
  if cosign sign --yes "$ref"; then
    break
  fi
  if [ "$attempt" = 3 ]; then
    echo "::error::cosign sign failed three times for $ref"
    exit 1
  fi
  echo "cosign sign failed (attempt $attempt); retrying in 15s"
  sleep 15
done

cosign verify "$ref" \
  --certificate-identity-regexp "$identity" \
  --certificate-oidc-issuer "$issuer" > /dev/null
echo "signed and verified $ref"
