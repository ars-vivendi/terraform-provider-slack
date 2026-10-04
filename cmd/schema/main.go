// Command schema exports the allowlisted schema from the pinned upstream SDK.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/ars-vivendi/terraform-provider-slack/internal/upstream"
	"github.com/hashicorp/terraform-plugin-go/tfprotov5"
)

func export(s *tfprotov5.Schema) map[string]any {
	if len(s.Block.BlockTypes) != 0 {
		panic("schema exporter requires explicit support for nested blocks")
	}
	attrs := map[string]any{}
	for _, a := range s.Block.Attributes {
		typ, err := a.Type.MarshalJSON()
		if err != nil {
			panic(err)
		}
		attrs[a.Name] = map[string]any{"type": json.RawMessage(typ), "required": a.Required, "optional": a.Optional, "computed": a.Computed, "sensitive": a.Sensitive, "description": a.Description}
	}
	return map[string]any{"version": s.Version, "block": map[string]any{"attributes": attrs, "description": s.Block.Description}}
}

func main() {
	p := upstream.Provider()
	if err := p.InternalValidate(); err != nil {
		panic(err)
	}
	s, err := p.GRPCProvider().GetProviderSchema(context.Background(), &tfprotov5.GetProviderSchemaRequest{})
	if err != nil {
		panic(err)
	}
	for _, d := range s.Diagnostics {
		if d.Severity == tfprotov5.DiagnosticSeverityError {
			panic(d.Summary + ": " + d.Detail)
		}
	}
	v := map[string]any{"format_version": "1.0", "provider_schemas": map[string]any{"registry.terraform.io/lfventura/slack": map[string]any{"provider": export(s.Provider), "resource_schemas": map[string]any{"slack_conversation": export(s.ResourceSchemas["slack_conversation"])}}}}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile("config/schema.json", append(b, '\n'), 0644); err != nil {
		panic(err)
	}
	fmt.Println("Exported actual upstream provider and slack_conversation schema to config/schema.json")
}
