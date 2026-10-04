#!/usr/bin/env bash
# Run from the repository root. Uses the goimports version locked in go.mod.
set -euo pipefail

export GOFLAGS=-mod=readonly
version=${1:-v0.0.0-dev}

mkdir -p _output/bin
GOBIN="$PWD/_output/bin" go install golang.org/x/tools/cmd/goimports
export PATH="$PWD/_output/bin:$PATH"
unformatted=$(goimports -l .)
if [[ -n "$unformatted" ]]; then
  printf 'Run goimports on:\n%s\n' "$unformatted" >&2
  exit 1
fi

go generate .
go run ./cmd/check-generation
git diff --exit-code
untracked=$(git ls-files --others --exclude-standard -- apis internal/controller examples-generated config package)
if [[ -n "$untracked" ]]; then
  printf 'Generated files must be checked in:\n%s\n' "$untracked" >&2
  exit 1
fi

go vet ./...
go test ./...
mkdir -p _output
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -ldflags="-X main.Version=$version" -o _output/provider ./cmd/provider
