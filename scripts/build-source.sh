#!/usr/bin/env bash
# Archive the same checkout as the runtime, with locked dependency source/notices.
set -euo pipefail

export GOFLAGS=-mod=readonly
export COPYFILE_DISABLE=1
version=${1:-v0.0.0-dev}
name="terraform-provider-slack-$version"
archive="_output/$name-source.tar.gz"
mkdir -p _output
stage=$(mktemp -d _output/source.XXXXXX)
trap 'rm -rf "$stage"' EXIT
source="$stage/$name"
mkdir -p "$source"

git ls-files --cached --others --exclude-standard -z -- . ':(exclude)_output' |
  tar --null --files-from=- --create --file=- |
  tar --extract --file=- --directory="$source"
test -s "$source/LICENSE"
test -s "$source/THIRD_PARTY_LICENSES.md"
test -s "$source/go.mod"
test -s "$source/go.sum"
go mod vendor -o "$source/vendor"
git diff --exit-code -- go.mod go.sum
tar --create --gzip --file="$archive" --directory="$stage" "$name"
printf 'Built matching source archive: %s\n' "$archive"
