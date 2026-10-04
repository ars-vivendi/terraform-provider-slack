// Package clients resolves credentials and configures the actual upstream SDK.
package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	ujresource "github.com/crossplane/upjet/v2/pkg/resource"
	"github.com/crossplane/upjet/v2/pkg/terraform"
	tfsdk "github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ars-vivendi/terraform-provider-slack/apis/namespaced/v1beta1"
	"github.com/ars-vivendi/terraform-provider-slack/internal/upstream"
)

// ParseCredentials rejects malformed JSON and non-user credentials without
// including credential contents in errors.
func ParseCredentials(data []byte) (string, error) {
	var creds struct {
		Token string `json:"token"`
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&creds); err != nil {
		return "", fmt.Errorf("credentials must be a JSON object with a token string")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return "", fmt.Errorf("credentials must contain exactly one JSON object")
	}
	if !strings.HasPrefix(creds.Token, "xoxp-") || len(creds.Token) <= len("xoxp-") || strings.TrimSpace(creds.Token) != creds.Token {
		return "", fmt.Errorf("credentials require a nonempty xoxp user OAuth token")
	}
	return creds.Token, nil
}

// TerraformSetupBuilder re-reads the Secret and constructs fresh upstream Meta
// on every connect, so token rotation does not reuse a stale Slack client.
// managementPoliciesEnabled must match the native connector's feature flag.
func TerraformSetupBuilder(managementPoliciesEnabled bool) terraform.SetupFn {
	return func(ctx context.Context, c client.Client, mg resource.Managed) (terraform.Setup, error) {
		m, ok := mg.(resource.ModernManaged)
		if !ok {
			return terraform.Setup{}, fmt.Errorf("Slack requires a namespaced managed resource")
		}
		ref := m.GetProviderConfigReference()
		if ref == nil {
			ref = &xpv2.ProviderConfigReference{Kind: "ClusterProviderConfig", Name: "default"}
			m.SetProviderConfigReference(ref)
		}
		var spec v1beta1.ProviderConfigSpec
		switch ref.Kind {
		case "ProviderConfig":
			pc := &v1beta1.ProviderConfig{}
			if err := c.Get(ctx, types.NamespacedName{Namespace: mg.GetNamespace(), Name: ref.Name}, pc); err != nil {
				return terraform.Setup{}, fmt.Errorf("get ProviderConfig: %w", err)
			}
			spec = pc.Spec
			if spec.Credentials.SecretRef != nil {
				s := *spec.Credentials.SecretRef
				s.Namespace = mg.GetNamespace()
				spec.Credentials.SecretRef = &s
			}
		case "ClusterProviderConfig":
			pc := &v1beta1.ClusterProviderConfig{}
			if err := c.Get(ctx, types.NamespacedName{Name: ref.Name}, pc); err != nil {
				return terraform.Setup{}, fmt.Errorf("get ClusterProviderConfig: %w", err)
			}
			spec = pc.Spec
		default:
			return terraform.Setup{}, fmt.Errorf("unsupported providerConfigRef kind %q", ref.Kind)
		}
		if spec.Credentials.Source != xpv2.CredentialsSourceSecret || spec.Credentials.SecretRef == nil {
			return terraform.Setup{}, fmt.Errorf("credentials source must be Secret with secretRef")
		}
		if spec.Credentials.SecretRef.Namespace == "" {
			return terraform.Setup{}, fmt.Errorf("credential Secret namespace is required")
		}
		if err := resource.NewProviderConfigUsageTracker(c, &v1beta1.ProviderConfigUsage{}).Track(ctx, m); err != nil {
			return terraform.Setup{}, fmt.Errorf("track ProviderConfig usage: %w", err)
		}
		secretRef := spec.Credentials.SecretRef
		data, err := resource.CommonCredentialExtractor(ctx, spec.Credentials.Source, c, xpv2.CommonCredentialSelectors{SecretRef: &xpv2.SecretKeySelector{SecretReference: xpv2.SecretReference{Name: secretRef.Name, Namespace: secretRef.Namespace}, Key: secretRef.Key}})
		if err != nil {
			return terraform.Setup{}, fmt.Errorf("extract credential Secret: %w", err)
		}
		token, err := ParseCredentials(data)
		if err != nil {
			return terraform.Setup{}, err
		}
		configuration := map[string]any{"token": token}
		p := upstream.Provider()
		policy := managed.NewManagementPoliciesResolver(managementPoliciesEnabled, m.GetManagementPolicies())
		if policy.ShouldCreate() || policy.ShouldUpdate() {
			tr, ok := mg.(ujresource.Terraformed)
			if !ok {
				return terraform.Setup{}, fmt.Errorf("Slack requires a native Terraform managed resource")
			}
			// Use the generated merge implementation and the same feature flag
			// as Upjet Connect: initProvider is ignored when policies are disabled.
			parameters, err := tr.GetMergedParameters(managementPoliciesEnabled)
			if err != nil {
				return terraform.Setup{}, fmt.Errorf("get Slack resource parameters for validation: %w", err)
			}
			if diags := p.ResourcesMap["slack_conversation"].Validate(tfsdk.NewResourceConfigRaw(parameters)); diags.HasError() {
				return terraform.Setup{}, fmt.Errorf("upstream slack_conversation validation failed: %v", diags)
			}
		}
		if diags := p.Configure(ctx, tfsdk.NewResourceConfigRaw(configuration)); diags.HasError() {
			return terraform.Setup{}, fmt.Errorf("upstream Slack provider configuration failed")
		}
		return terraform.Setup{Configuration: configuration, Meta: p.Meta(), Requirement: terraform.ProviderRequirement{Source: "lfventura/slack", Version: "2.1.1"}}, nil
	}
}
