package whatsapp

import (
	"fmt"
	"time"

	"Loom/pkg/core"
	"Loom/pkg/db"
)

func (w *WhatsAppProvider) storeHistoryReactionBatch(conversationID string, reactions []core.ReactionEvent) error {
	if len(reactions) == 0 {
		return nil
	}
	instanceID := w.getInstanceId()
	if !db.ForProvider(db.DB, instanceID).OwnsConversation(conversationID) {
		return fmt.Errorf("history reactions have no conversation owner")
	}
	w.mu.RLock()
	var cutoff *time.Time
	if w.hadSyncAtStartup && w.reactionCatchUpSince != nil {
		value := *w.reactionCatchUpSince
		cutoff = &value
	}
	w.mu.RUnlock()
	w.historyBackfillMu.Lock()
	legacyBackfill := w.historyBackfills[conversationID]
	w.historyBackfillMu.Unlock()
	changes := append([]core.ReactionEvent(nil), reactions...)
	for index := range changes {
		// WhatsApp reaction timestamps have second precision in the canonical
		// event. Include the boundary second and let persisted event tombstones
		// deduplicate it. Never invent a date for an undated imported reaction.
		changes[index].Historical = legacyBackfill || cutoff == nil || changes[index].Timestamp <= 0 || changes[index].Timestamp < cutoff.Unix()
	}
	batch := core.ReactionBatchEvent{InstanceID: instanceID, ConversationID: conversationID, Reactions: changes}
	if err := core.StoreReactionBatch(db.DB, batch, w.CurrentUserID()); err != nil {
		return err
	}
	select {
	case w.eventChan <- batch:
		return nil
	case <-w.ctx.Done():
		return w.ctx.Err()
	case <-time.After(time.Second):
		return fmt.Errorf("history reaction event channel is full")
	}
}
