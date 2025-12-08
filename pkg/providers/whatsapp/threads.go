package whatsapp

import (
	"Loom/pkg/models"
)

func (w *WhatsAppProvider) GetThreads(parentMessageID string) ([]models.Message, error) {
	// TODO: Implement thread retrieval
	markUnused(parentMessageID)
	return []models.Message{}, nil
}
