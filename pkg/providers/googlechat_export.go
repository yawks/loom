package providers

import (
	"Loom/pkg/core"
	"Loom/pkg/providers/googlechat"
)

// NewGoogleChatProvider creates a new instance of the GoogleChatProvider.
func NewGoogleChatProvider() core.Provider {
	return googlechat.NewGoogleChatProvider()
}
