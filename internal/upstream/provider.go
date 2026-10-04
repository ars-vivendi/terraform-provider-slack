// Package upstream is the pinned lfventura/slack SDK v2 provider boundary,
// including the upstream patch for an absent imported destroy action.
package upstream

import (
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	slack "github.com/pablovarela/terraform-provider-slack/slack"
)

// Provider constructs the actual upstream provider, including its validation,
// configuration and resource lifecycle callbacks.
func Provider() *schema.Provider {
	return slack.Provider()
}
