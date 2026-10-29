package clients

import (
	"context"
	"strings"
	"testing"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	conversation "github.com/ars-vivendi/terraform-provider-slack/apis/namespaced/conversation/v1alpha1"
	"github.com/ars-vivendi/terraform-provider-slack/apis/namespaced/v1beta1"
)

func TestSetupTracksUnresolvedProviderConfig(t *testing.T) {
	for _, kind := range []string{"ProviderConfig", "ClusterProviderConfig"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			if err := v1beta1.SchemeBuilder.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			// A exists but is deliberately invalid: tracking must precede
			// credential-selector validation as well as configuration lookup.
			var a client.Object = &v1beta1.ProviderConfig{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "team"}}
			if kind == "ClusterProviderConfig" {
				a = &v1beta1.ClusterProviderConfig{ObjectMeta: metav1.ObjectMeta{Name: "a"}}
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(a).Build()
			m := &conversation.Conversation{ObjectMeta: metav1.ObjectMeta{Name: "channel", Namespace: "team", UID: "channel-uid"}}
			m.SetGroupVersionKind(conversation.Conversation_GroupVersionKind)
			setup := TerraformSetupBuilder(true)
			for _, name := range []string{"a", "missing-b"} {
				ref := xpv2.ProviderConfigReference{Kind: kind, Name: name}
				m.SetProviderConfigReference(&ref)
				_, err := setup(ctx, c, m)
				wantErr := "credentials source must be Secret with secretRef"
				if name == "missing-b" {
					wantErr = "get " + kind + ":"
				}
				if err == nil || !strings.Contains(err.Error(), wantErr) {
					t.Fatalf("setup %s: got %v, want %q", name, err, wantErr)
				}
				usages := &v1beta1.ProviderConfigUsageList{}
				if err := c.List(ctx, usages); err != nil {
					t.Fatal(err)
				}
				if len(usages.Items) != 1 {
					t.Fatalf("got %d usages, want one created then updated", len(usages.Items))
				}
				u := usages.Items[0]
				if u.GetProviderConfigReference() != ref || u.Labels[xpv2.LabelKeyProviderName] != name || u.Labels[xpv2.LabelKeyProviderKind] != kind {
					t.Fatalf("usage still references previous configuration: %+v", u)
				}
				if u.Name != string(m.UID) || u.Namespace != m.Namespace || len(u.OwnerReferences) != 1 || u.OwnerReferences[0].UID != m.UID {
					t.Fatalf("incorrect usage identity/owner: %+v", u.ObjectMeta)
				}
			}
		})
	}
}

func TestSetupDefaultAndUnsupportedReferences(t *testing.T) {
	for _, kind := range []string{"default", "UnsupportedConfig", ""} {
		t.Run(kind, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := v1beta1.SchemeBuilder.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			c := fake.NewClientBuilder().WithScheme(scheme).Build()
			m := &conversation.Conversation{ObjectMeta: metav1.ObjectMeta{Name: "channel", Namespace: "team", UID: "channel-uid"}}
			m.SetGroupVersionKind(conversation.Conversation_GroupVersionKind)
			if kind != "default" {
				m.SetProviderConfigReference(&xpv2.ProviderConfigReference{Kind: kind, Name: "config"})
			}
			_, err := TerraformSetupBuilder(true)(context.Background(), c, m)
			if err == nil {
				t.Fatal("expected configuration error")
			}
			usages := &v1beta1.ProviderConfigUsageList{}
			if err := c.List(context.Background(), usages); err != nil {
				t.Fatal(err)
			}
			if kind == "default" {
				want := xpv2.ProviderConfigReference{Kind: "ClusterProviderConfig", Name: "default"}
				if *m.GetProviderConfigReference() != want || len(usages.Items) != 1 || usages.Items[0].GetProviderConfigReference() != want {
					t.Fatalf("default reference not tracked: %+v", usages.Items)
				}
			} else if !strings.Contains(err.Error(), "unsupported providerConfigRef kind") || len(usages.Items) != 0 {
				t.Fatalf("unsupported kind tracked: usages=%v err=%v", usages.Items, err)
			}
		})
	}
}

func TestParseCredentials(t *testing.T) {
	for _, input := range []string{`{}`, `null`, `[]`, `{"token":1}`, `{"token":"xoxb-bot"}`, `{"token":"xoxp-"}`, `{"token":" xoxp-user"}`, `{"token":"xoxp-user"} {}`, `{"token":"xoxp-user","extra":"unexpected"}`, `not json`} {
		t.Run(input, func(t *testing.T) {
			if _, err := ParseCredentials([]byte(input)); err == nil {
				t.Fatal("invalid credentials accepted")
			}
		})
	}
	if token, err := ParseCredentials([]byte(`{"token":"xoxp-user"}`)); err != nil || token != "xoxp-user" {
		t.Fatalf("valid credentials: token=%q err=%v", token, err)
	}
}
