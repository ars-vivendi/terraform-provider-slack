#!/usr/bin/env bash
# Select the exact tool version from the provider's existing module graph.
set -euo pipefail

version=$(go list -mod=readonly -m -f '{{.Version}}' github.com/google/go-containerregistry)
mkdir -p _output/bin
# An explicit version installs the tool without changing the provider's pruned sums.
GOBIN="$PWD/_output/bin" go install "github.com/google/go-containerregistry/cmd/crane@$version"
