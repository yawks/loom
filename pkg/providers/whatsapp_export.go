// Package providers contains provider implementations and helpers.
// This file re-exports WhatsAppProvider from the whatsapp subpackage for backward compatibility.
package providers

import (
	"Loom/pkg/core"
	"Loom/pkg/providers/whatsapp"
)

// NewWhatsAppProvider creates a new instance of the WhatsAppProvider.
// This is a re-export from the whatsapp subpackage.
func NewWhatsAppProvider() core.Provider {
	return whatsapp.NewWhatsAppProvider()
}

