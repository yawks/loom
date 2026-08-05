package teams

import (
	"Loom/pkg/core"
	"Loom/pkg/models"
	"Loom/pkg/providers/messageformat"
	"context"
	"fmt"
	"time"

	"go.mau.fi/mautrix-teams/pkg/msteams"
)

// ScheduleMessage queues a text message for future delivery via the Teams
// native draft scheduling API (POST /v1/users/ME/drafts).
func (p *Provider) ScheduleMessage(conversationID, text string, scheduledAt time.Time, _ string) (*models.ScheduledMessage, error) {
	client, instance, err := p.connectedClient()
	if err != nil {
		return nil, err
	}
	if text == "" {
		return nil, fmt.Errorf("%s: scheduled message text is required", providerID)
	}
	if !scheduledAt.After(time.Now()) {
		return nil, fmt.Errorf("%s: scheduled time must be in the future", providerID)
	}
	rawConvID := core.StripConvID(conversationID)
	nsConvID := core.BuildConvID(instance, rawConvID)
	content := msteams.MatrixToTeamsHTML(messageformat.TeamsHTML(text))
	draftID, err := client.ScheduleDraft(context.Background(), rawConvID, content, p.displayName(), scheduledAt)
	if err != nil {
		return nil, fmt.Errorf("%s: schedule message: %w", providerID, err)
	}
	return &models.ScheduledMessage{
		ID:                 draftID,
		ProviderInstanceID: instance,
		ProtocolConvID:     nsConvID,
		Body:               text,
		ScheduledAt:        scheduledAt,
		CreatedAt:          time.Now(),
	}, nil
}

// ListScheduledMessages returns all pending scheduled drafts for the given
// conversation from the Teams server.
func (p *Provider) ListScheduledMessages(conversationID string) ([]models.ScheduledMessage, error) {
	client, instance, err := p.connectedClient()
	if err != nil {
		return nil, err
	}
	rawConvID := core.StripConvID(conversationID)
	nsConvID := core.BuildConvID(instance, rawConvID)
	drafts, err := client.ListScheduledDrafts(context.Background(), rawConvID)
	if err != nil {
		return nil, fmt.Errorf("%s: list scheduled messages: %w", providerID, err)
	}
	out := make([]models.ScheduledMessage, 0, len(drafts))
	for _, d := range drafts {
		plain, _ := msteams.HTMLToMatrix(d.Content)
		if plain == "" {
			plain = d.Content
		}
		out = append(out, models.ScheduledMessage{
			ID:                 d.DraftID,
			ProviderInstanceID: instance,
			ProtocolConvID:     nsConvID,
			Body:               plain,
			ScheduledAt:        d.SendAt,
			CreatedAt:          d.CreatedAt,
		})
	}
	return out, nil
}

// CancelScheduledMessage deletes a scheduled draft from the Teams server.
func (p *Provider) CancelScheduledMessage(conversationID, scheduledMessageID string) error {
	client, _, err := p.connectedClient()
	if err != nil {
		return err
	}
	if err := client.DeleteScheduledDraft(context.Background(), scheduledMessageID); err != nil {
		return fmt.Errorf("%s: cancel scheduled message: %w", providerID, err)
	}
	return nil
}
