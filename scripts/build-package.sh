#!/usr/bin/env bash
# Run check.sh with the same version first. No runtime image is pushed.
set -euo pipefail

version=${1:-v0.0.0-dev}
crossplane=${CROSSPLANE_CLI:-_output/bin/crossplane}
runtime="terraform-provider-slack-runtime:$version"
package="_output/terraform-provider-slack-$version-linux-amd64.xpkg"

test -s _output/provider
test -s package/crossplane.yaml
shopt -s nullglob
crds=(package/crds/*.yaml)
if [[ ${#crds[@]} -ne 4 ]]; then
  printf 'Expected the four generated provider CRDs in package/crds/.\n' >&2
  exit 1
fi

docker build --platform linux/amd64 --tag "$runtime" .
if [[ "$(docker run --rm --platform linux/amd64 "$runtime" --version)" != "$version" ]]; then
  printf 'Runtime version differs from package version; rerun check.sh with %s.\n' "$version" >&2
  exit 1
fi
mkdir -p _output/empty-examples
"$crossplane" xpkg build --package-root=package \
  --examples-root=_output/empty-examples \
  --embed-runtime-image="$runtime" --package-file="$package"
printf 'Built linux/amd64 package: %s\n' "$package"
