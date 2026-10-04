//go:build generate

// Package apis tracks the pinned generation toolchain.
package apis

// Generation is invoked from the project root with go generate .

import (
	_ "github.com/crossplane/crossplane-tools/cmd/angryjet"
	_ "golang.org/x/tools/cmd/goimports"
	_ "sigs.k8s.io/controller-tools/cmd/controller-gen"
)
