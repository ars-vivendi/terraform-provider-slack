// Command provenance verifies the module replacement and emits reproducible provenance.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
)

const (
	original      = "github.com/pablovarela/terraform-provider-slack"
	baseFork      = "github.com/lfventura/terraform-provider-slack"
	baseVersion   = "v1.2.14-0.20260808215558-b4b4cabd248a"
	baseCommit    = "b4b4cabd248a9d94c9d809fad8ce7bef55d5dae0"
	fork          = "github.com/ars-vivendi/terraform-provider-slack-sdk"
	version       = "v1.2.14-0.20261004145015-6758d581211e"
	commit        = "6758d581211eb41d87725bab47ecb452617a658e"
	moduleSum     = "h1:qCOtYfDKY4SaiacuAbWKLxwX/UjS5eR1OM2a6NoHO84="
	goModSum      = "h1:LmGnMOs/Gzyem2rmF1JFCZO6RM0zkE1/cQ58NmpzkX0="
	baseSchemaSum = "bef33d926d255ab83c12163a33b0f1850f178dd7ed85739952adc72b3fcba3a0"

	upjetOriginal    = "github.com/crossplane/upjet/v2"
	upjetBaseVersion = "v2.5.1"
	upjetFork        = "github.com/ars-vivendi/upjet/v2"
	upjetVersion     = "v2.5.2-0.20261004140655-c3f424997d79"
	upjetCommit      = "c3f424997d7914bfd163cad0956d20f61242d0a4"
	upjetModuleSum   = "h1:c4RcBHfrtleaW/JIMrXHO1b/PzSznt2hxiWTXoAamOU="
	upjetGoModSum    = "h1:mk+Me18gPUhsQMWgg225vOsGeFocD9/GYI6opMSgHzk="
)

func main() {
	var module struct {
		Path, Version string
		Replace       *struct{ Path, Version string }
	}
	decodeGo(&module, "list", "-m", "-json", original)
	if module.Path != original || module.Version != baseVersion || module.Replace == nil || module.Replace.Path != fork || module.Replace.Version != version {
		panic("go.mod does not use the approved exact Slack fork replacement")
	}
	var downloaded struct {
		Path, Version, Sum, GoModSum, Error string
		Origin                              struct{ Hash string }
	}
	decodeGo(&downloaded, "mod", "download", "-json", fork+"@"+version)
	if downloaded.Error != "" || downloaded.Path != fork || downloaded.Version != version || downloaded.Sum != moduleSum || downloaded.GoModSum != goModSum || downloaded.Origin.Hash != commit {
		panic(fmt.Sprintf("unexpected upstream module provenance: %+v", downloaded))
	}
	var upjetModule struct {
		Path, Version string
		Replace       *struct{ Path, Version string }
	}
	decodeGo(&upjetModule, "list", "-m", "-json", upjetOriginal)
	if upjetModule.Path != upjetOriginal || upjetModule.Version != upjetBaseVersion || upjetModule.Replace == nil || upjetModule.Replace.Path != upjetFork || upjetModule.Replace.Version != upjetVersion {
		panic("go.mod does not use the approved exact Upjet delete-only fix replacement")
	}
	var upjetDownloaded struct {
		Path, Version, Sum, GoModSum, Error string
		Origin                              struct{ Hash string }
	}
	decodeGo(&upjetDownloaded, "mod", "download", "-json", upjetFork+"@"+upjetVersion)
	if upjetDownloaded.Error != "" || upjetDownloaded.Path != upjetFork || upjetDownloaded.Version != upjetVersion || upjetDownloaded.Sum != upjetModuleSum || upjetDownloaded.GoModSum != upjetGoModSum || upjetDownloaded.Origin.Hash != upjetCommit {
		panic(fmt.Sprintf("unexpected Upjet module provenance: %+v", upjetDownloaded))
	}
	schema, err := os.ReadFile("config/schema.json")
	if err != nil {
		panic(err)
	}
	hash := sha256.Sum256(schema)
	if hex.EncodeToString(hash[:]) != baseSchemaSum {
		panic("Slack lifecycle backport changed the approved base provider schema")
	}
	provenance := map[string]any{
		"terraformProvider": "lfventura/slack", "terraformVersion": "2.1.1", "upstreamCommit": baseCommit,
		"baseReplacementModule": baseFork, "baseReplacementVersion": baseVersion,
		"modulePath": original, "replacementModule": fork, "replacementVersion": version, "moduleSum": moduleSum, "goModSum": goModSum,
		"replacementCommit": commit, "upstreamPullRequest": "https://github.com/lfventura/terraform-provider-slack/pull/31",
		"schemaSHA256": hex.EncodeToString(hash[:]), "schemaResourceAllowlist": []string{"slack_conversation"},
		"schemaSource": "upstream SDK v2 GRPCProvider.GetProviderSchema", "upjetVersion": upjetBaseVersion,
		"upjetReplacementModule": upjetFork, "upjetReplacementVersion": upjetVersion, "upjetReplacementCommit": upjetCommit,
		"upjetModuleSum": upjetModuleSum, "upjetGoModSum": upjetGoModSum, "upjetUpstreamPullRequest": "https://github.com/crossplane/upjet/pull/772",
	}
	b, err := json.MarshalIndent(provenance, "", "  ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile("config/upstream-provenance.json", append(b, '\n'), 0644); err != nil {
		panic(err)
	}
	fmt.Println("Verified pinned Slack and Upjet origins and checksums; wrote config/upstream-provenance.json")
}

func decodeGo(v any, args ...string) {
	cmd := exec.Command("go", args...)
	cmd.Stderr = os.Stderr
	b, err := cmd.Output()
	if err != nil {
		panic(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		panic(err)
	}
}
