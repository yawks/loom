package providers

import (
	"Loom/pkg/core"
	signalprovider "Loom/pkg/providers/signal"
)

func NewSignalProvider() core.Provider { return signalprovider.NewProvider() }
