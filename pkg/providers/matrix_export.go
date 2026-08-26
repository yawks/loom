package providers

import (
	"Loom/pkg/core"
	matrixprovider "Loom/pkg/providers/matrix"
)

func NewMatrixProvider() core.Provider { return matrixprovider.NewProvider() }
