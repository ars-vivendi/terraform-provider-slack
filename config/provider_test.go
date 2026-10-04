package config

import (
	"maps"
	"reflect"
	"strings"
	"testing"

	tfsdk "github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
)

func TestCustomDiffValidationUsesOnlyExistingStateName(t *testing.T) {
	r := GetProvider().Resources["slack_conversation"]
	for _, tc := range []struct {
		name   string
		state  *tfsdk.InstanceState
		inputs map[string]any
		wantOK bool
	}{
		{name: "existing state", state: &tfsdk.InstanceState{ID: "C123", Attributes: map[string]string{"name": "observed-channel"}}, inputs: map[string]any{"id": "C123"}, wantOK: true},
		{name: "no state", inputs: map[string]any{}},
		{name: "state without identity", state: &tfsdk.InstanceState{Attributes: map[string]string{"name": "observed-channel"}}, inputs: map[string]any{}},
		{name: "state without name", state: &tfsdk.InstanceState{ID: "C123"}, inputs: map[string]any{}},
		{name: "supplied null name is not replaced", state: &tfsdk.InstanceState{ID: "C123", Attributes: map[string]string{"name": "observed-channel"}}, inputs: map[string]any{"name": nil}},
		{name: "invalid destroy action", state: &tfsdk.InstanceState{ID: "C123", Attributes: map[string]string{"name": "observed-channel"}}, inputs: map[string]any{"action_on_destroy": "delete"}},
		{name: "invalid membership action", state: &tfsdk.InstanceState{ID: "C123", Attributes: map[string]string{"name": "observed-channel"}}, inputs: map[string]any{"action_on_update_permanent_members": "ignore"}},
		{name: "complete creation", inputs: map[string]any{"name": "new-channel"}, wantOK: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configuration := tfsdk.NewResourceConfigRaw(tc.inputs)
			before := maps.Clone(configuration.Raw)
			var attributes map[string]string
			if tc.state != nil {
				attributes = maps.Clone(tc.state.Attributes)
			}
			diff := tfsdk.NewInstanceDiff()
			got, err := r.TerraformCustomDiff(diff, tc.state, configuration)
			if tc.wantOK {
				if err != nil || got != diff {
					t.Fatalf("valid configuration changed or rejected: diff=%v err=%v", got, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "validation failed") || got != nil {
				t.Fatalf("expected unmodified upstream validation failure: diff=%v err=%v", got, err)
			}
			if !reflect.DeepEqual(before, configuration.Raw) || (tc.state != nil && !reflect.DeepEqual(attributes, tc.state.Attributes)) {
				t.Fatal("validation changed actual SDK configuration or state")
			}
		})
	}
}
