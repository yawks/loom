package providers

import (
	"Loom/pkg/core"
	teamsprovider "Loom/pkg/providers/teams"
)

func NewTeamsProvider() core.Provider { return teamsprovider.NewProvider() }
