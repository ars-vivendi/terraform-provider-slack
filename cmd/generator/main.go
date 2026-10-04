// Command generator runs the actual Upjet namespaced generation pipeline.
package main

import (
	"path/filepath"

	"github.com/ars-vivendi/terraform-provider-slack/config"
	"github.com/crossplane/upjet/v2/pkg/pipeline"
	ujtypes "github.com/crossplane/upjet/v2/pkg/types"
)

func main() {
	root, err := filepath.Abs(".")
	if err != nil {
		panic(err)
	}
	r := pipeline.PipelineRunner{
		DirAPIs:               filepath.Join(root, "apis", "namespaced"),
		DirControllers:        filepath.Join(root, "internal", "controller", "namespaced"),
		DirExamples:           filepath.Join(root, "examples-generated", "namespaced"),
		DirHack:               filepath.Join(root, "hack"),
		ModulePathAPIs:        config.ModulePath + "/apis/namespaced",
		ModulePathControllers: config.ModulePath + "/internal/controller/namespaced",
		Scope:                 ujtypes.CRDScopeNamespaced,
	}
	r.Run(config.GetProvider())
}
