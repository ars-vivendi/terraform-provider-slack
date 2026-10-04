// Package config defines the native SDK resource allowlist and generation contract.
package config

import (
	_ "embed"
	"fmt"

	"github.com/ars-vivendi/terraform-provider-slack/internal/upstream"
	ujconfig "github.com/crossplane/upjet/v2/pkg/config"
	tfsdk "github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
)

//go:embed schema.json
var providerSchema []byte

//go:embed provider-metadata.yaml
var providerMetadata []byte

const ModulePath = "github.com/ars-vivendi/terraform-provider-slack"

// GetProvider configures only namespaced Conversation, using SDK-native callbacks.
func GetProvider() *ujconfig.Provider {
	p := ujconfig.NewProvider(providerSchema, "slack", ModulePath, providerMetadata,
		ujconfig.WithRootGroup("slack.m.arsvivendi.io"),
		ujconfig.WithShortName("slack"),
		ujconfig.WithFeaturesPackage("internal/features"),
		ujconfig.WithIncludeList([]string{}),
		ujconfig.WithTerraformPluginSDKIncludeList([]string{"^slack_conversation$"}),
		ujconfig.WithTerraformProvider(upstream.Provider()),
		ujconfig.WithBasePackages(ujconfig.BasePackages{APIVersion: []string{"v1beta1"}, ControllerMap: map[string]string{"providerconfig": "config"}}),
	)
	p.AddResourceConfigurator("slack_conversation", func(r *ujconfig.Resource) {
		r.ShortGroup = "conversation"
		r.Kind = "Conversation"
		r.Version = "v1alpha1"
		r.ExternalName = ujconfig.IdentifierFromProvider
		// Setup validates complete writable inputs. After Refresh, imports and
		// delete-only configurations may validate an omitted name against the
		// genuine existing state, without changing parameters or the SDK diff.
		r.TerraformCustomDiff = func(diff *tfsdk.InstanceDiff, state *tfsdk.InstanceState, configuration *tfsdk.ResourceConfig) (*tfsdk.InstanceDiff, error) {
			// Upjet adds the SDK state identity as a synthetic "id" parameter
			// after configuration injection. It is not an upstream configurable
			// argument. Validate every actual input without changing SDK state.
			inputs := make(map[string]any, len(configuration.Raw))
			for key, value := range configuration.Raw {
				if key != "id" {
					inputs[key] = value
				}
			}
			if _, supplied := inputs["name"]; !supplied && state != nil && state.ID != "" {
				if name, ok := state.Attributes["name"]; ok {
					inputs["name"] = name
				}
			}
			if diags := r.TerraformResource.Validate(tfsdk.NewResourceConfigRaw(inputs)); diags.HasError() {
				return nil, fmt.Errorf("upstream slack_conversation validation failed: %v", diags)
			}
			return diff, nil
		}
		r.Sensitive.AdditionalConnectionDetailsFn = func(attr map[string]any) (map[string][]byte, error) {
			id, ok := attr["id"].(string)
			if !ok || id == "" {
				return nil, fmt.Errorf("upstream conversation state has no channel ID")
			}
			return map[string][]byte{"channel_id": []byte(id)}, nil
		}
	})
	p.ConfigureResources()
	return p
}
