package teams

import (
	"Loom/pkg/core"
	"Loom/pkg/models"
	"time"

	"go.mau.fi/mautrix-teams/pkg/msteams"
)

// Called with historySyncMu held. New messages already use the current
// converter; only compatibility data needs these full-history scans. Reconnect
// recovery can run every minute and must not repeat them on every pass. Keep a
// periodic retry for old messages which could not be fetched remotely yet.
func (p *Provider) runLegacyRepairs(now time.Time, repair func() error) error {
	if now.Before(p.legacyRepairNext) {
		return nil
	}
	p.legacyRepairNext = now.Add(time.Minute)
	if err := repair(); err != nil {
		return err
	}
	p.legacyRepairNext = now.Add(30 * time.Minute)
	return nil
}

func (p *Provider) repairLegacyMessages(client *msteams.Client) error {
	if err := p.normalizeStoredMeetingRecordings(); err != nil {
		return err
	}
	if err := p.removeStoredPendingRecordings(client); err != nil {
		return err
	}
	formatting, err := p.repairStoredHTMLFormatting(client)
	if err != nil {
		return err
	}
	cards, err := p.repairStoredCards(client)
	if err != nil {
		return err
	}
	for _, batches := range []map[string][]models.Message{formatting, cards} {
		for conversationID, messages := range batches {
			p.emit(core.MessageBatchEvent{InstanceID: p.instance, ConversationID: core.BuildConvID(p.instance, conversationID), Messages: messages, IsHistorical: true})
		}
	}
	return nil
}
