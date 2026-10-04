#!/usr/bin/env bash
# Installs only the verified Linux/amd64 CLI used by GitHub's Linux runner.
set -euo pipefail

if [[ "$(uname -s)/$(uname -m)" != Linux/x86_64 ]]; then
  printf 'This installer requires Linux/amd64.\n' >&2
  exit 1
fi

mkdir -p _output/bin
curl --fail --silent --show-error --location \
  https://cli.crossplane.io/stable/v2.5.0/bin/linux_amd64/crossplane \
  --output _output/bin/crossplane
printf '%s  %s\n' \
  065dd6b40c9ea5fc71e49900d5c924cc0bec7f58375922ddbde6b402f292347e \
  _output/bin/crossplane | sha256sum --check --strict
chmod +x _output/bin/crossplane
