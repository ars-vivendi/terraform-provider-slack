// Package slack tracks reproducible provider generation entrypoints.
package slack

//go:generate go run ./cmd/schema
//go:generate go run ./cmd/provenance
//go:generate go run ./cmd/generator
//go:generate go run sigs.k8s.io/controller-tools/cmd/controller-gen object:headerFile=hack/boilerplate.go.txt paths=./apis/namespaced/... crd:crdVersions=v1 output:artifacts:config=package/crds
//go:generate go run github.com/crossplane/crossplane-tools/cmd/angryjet generate-methodsets --header-file=hack/boilerplate.go.txt ./apis/namespaced/...
