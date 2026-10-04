package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	crdvalidation "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	schemavalidation "k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	crdregistry "k8s.io/apiextensions-apiserver/pkg/registry/customresourcedefinition"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/yaml"
)

func TestGeneratedCRDContract(t *testing.T) {
	p := GetProvider()
	if len(p.Resources) != 1 || !p.Resources["slack_conversation"].ShouldUseTerraformPluginSDKClient() || len(p.IncludeList) != 0 {
		t.Fatal("resource allowlist must use only the native SDK conversation")
	}
	paths, err := filepath.Glob("../package/crds/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 4 {
		t.Fatalf("expected Conversation, ProviderConfig, ClusterProviderConfig, ProviderConfigUsage; got %d", len(paths))
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var crd apiextensionsv1.CustomResourceDefinition
			if err := yaml.Unmarshal(b, &crd); err != nil {
				t.Fatal(err)
			}
			var internal apiextensions.CustomResourceDefinition
			if err := apiextensionsv1.Convert_v1_CustomResourceDefinition_To_apiextensions_CustomResourceDefinition(&crd, &internal, nil); err != nil {
				t.Fatal(err)
			}
			// Mirror API-server creation, which initializes status.storedVersions
			// before validation. Generated manifests intentionally omit status.
			crdregistry.NewStrategy(runtime.NewScheme()).PrepareForCreate(context.Background(), &internal)
			// Kubernetes' actual validator also compiles the generated CEL rules.
			if errs := crdvalidation.ValidateCustomResourceDefinition(context.Background(), &internal); len(errs) != 0 {
				t.Fatalf("invalid CRD: %v", errs)
			}
			wantScope := apiextensionsv1.NamespaceScoped
			if crd.Spec.Names.Kind == "ClusterProviderConfig" {
				wantScope = apiextensionsv1.ClusterScoped
			}
			if crd.Spec.Scope != wantScope {
				t.Fatalf("scope=%s want=%s", crd.Spec.Scope, wantScope)
			}
			if crd.Spec.Names.Kind != "Conversation" {
				return
			}
			if crd.Spec.Group != "conversation.slack.m.arsvivendi.io" || crd.Spec.Versions[0].Name != "v1alpha1" {
				t.Fatal("incorrect Conversation GVK")
			}
			spec := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"]
			if spec.Properties["writeConnectionSecretToRef"].Properties["name"].Type != "string" {
				t.Fatal("missing namespaced connection Secret contract")
			}
			status := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["status"]
			if status.Properties["atProvider"].Properties["id"].Type != "string" {
				t.Fatal("missing typed status.atProvider.id")
			}
			if len(spec.XValidations) == 0 {
				t.Fatal("missing required-name CEL rule")
			}
			var schema apiextensions.JSONSchemaProps
			if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(crd.Spec.Versions[0].Schema.OpenAPIV3Schema, &schema, nil); err != nil {
				t.Fatal(err)
			}
			validator, _, err := schemavalidation.NewSchemaValidator(&schema)
			if err != nil {
				t.Fatal(err)
			}
			obj := map[string]any{"apiVersion": "conversation.slack.m.arsvivendi.io/v1alpha1", "kind": "Conversation", "metadata": map[string]any{"name": "channel", "namespace": "team"}, "spec": map[string]any{"forProvider": map[string]any{"name": "channel", "permanentMembers": []any{"Uone"}}, "providerConfigRef": map[string]any{"kind": "ProviderConfig", "name": "workspace"}}}
			if errs := schemavalidation.ValidateCustomResource(field.NewPath(""), obj, validator); len(errs) != 0 {
				t.Fatalf("valid typed resource rejected: %v", errs)
			}
			obj["spec"].(map[string]any)["forProvider"].(map[string]any)["permanentMembers"] = []any{float64(42)}
			if errs := schemavalidation.ValidateCustomResource(field.NewPath(""), obj, validator); len(errs) == 0 {
				t.Fatal("invalid member element type accepted")
			}
		})
	}
}

func TestPackageCoreTarget(t *testing.T) {
	b, err := os.ReadFile("../package/crossplane.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		Spec struct {
			Crossplane struct {
				Version string `json:"version"`
			} `json:"crossplane"`
			Capabilities []string `json:"capabilities"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal(b, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Spec.Crossplane.Version != ">=v2.4.2" {
		t.Fatalf("package must declare selected minimum core 2.4.2: %q", metadata.Spec.Crossplane.Version)
	}
	if len(metadata.Spec.Capabilities) != 1 || metadata.Spec.Capabilities[0] != "SafeStart" {
		t.Fatal("package must declare implemented SafeStart capability")
	}
	// This checks the declared target only. Real core/package compatibility is
	// proved by disposable-cluster acceptance, never by this metadata assertion.
}
