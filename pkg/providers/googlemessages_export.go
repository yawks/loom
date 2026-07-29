package providers

import (
	"Loom/pkg/core"
	googlemessages "Loom/pkg/providers/googlemessages"
)

func NewGoogleMessagesProvider() core.Provider { return googlemessages.NewProvider() }
