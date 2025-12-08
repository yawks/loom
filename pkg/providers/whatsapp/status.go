package whatsapp

import (
	"Loom/pkg/core"
	"Loom/pkg/models"
	"fmt"
)

func (w *WhatsAppProvider) SendStatusMessage(text string, file *core.Attachment) (*models.Message, error) {
	// TODO: Implement status messages
	markUnused(text, file)
	return nil, fmt.Errorf("status messages not yet implemented")
}
