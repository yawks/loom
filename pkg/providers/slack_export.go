package providers

import (
	"Loom/pkg/core"
	"Loom/pkg/providers/slack"
)

// NewSlackProvider creates a new instance of the SlackProvider.
// This is a re-export from the slack subpackage.
func NewSlackProvider() core.Provider {
	return slack.NewSlackProvider()
}
