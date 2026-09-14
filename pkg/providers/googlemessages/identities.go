package googlemessages

import (
	"fmt"
	"log"
	"sort"
	"strings"

	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
)

var _ core.CommunicationIdentityProvider = (*Provider)(nil)

// Diagnostics deliberately contain no message text, addresses, IDs or raw protobuf.
type identityDiagnostic struct{ Incoming, Outgoing, IncomingWithSenderPayload, OutgoingResolved uint64 }

func canonicalIdentity(card *gmproto.SIMCard) core.CommunicationIdentity {
	data := card.GetSIMData()
	address := data.GetFormattedPhoneNumber()
	if address == "" {
		address = data.GetInternationalPhoneNumber()
	}
	label := data.GetCarrierName()
	if label == "" {
		label = "SIM"
	}
	return core.CommunicationIdentity{ID: card.GetSIMParticipant().GetID(), Label: label, Address: address}
}

func (p *Provider) updateSIMCards(settings *gmproto.Settings) {
	if settings == nil || settings.SIMCards == nil {
		return
	} // Absent means a partial update.
	p.mu.Lock()
	p.simCards = make(map[string]*gmproto.SIMCard)
	for _, card := range settings.SIMCards {
		if id := card.GetSIMParticipant().GetID(); id != "" {
			p.simCards[id] = proto.Clone(card).(*gmproto.SIMCard)
		}
	}
	p.simSettingsKnown = true
	log.Printf("[%s] line diagnostic: available=%d incoming=%d outgoing=%d incoming_sender_payload=%d outgoing_resolved=%d", p.instance, len(p.simCards), p.identityDiagnostic.Incoming, p.identityDiagnostic.Outgoing, p.identityDiagnostic.IncomingWithSenderPayload, p.identityDiagnostic.OutgoingResolved)
	identities := make([]core.CommunicationIdentity, 0, len(p.simCards))
	for _, card := range p.simCards {
		identities = append(identities, canonicalIdentity(card))
	}
	p.mu.Unlock()
	if err := p.reconcileMessageIdentities(identities); err != nil {
		log.Printf("[%s] line metadata reconciliation failed: %v", p.instance, err)
	}
}

func (p *Provider) rememberConversationSIM(conversation *gmproto.Conversation) {
	card := conversation.GetSimCard()
	if card == nil {
		return
	}
	copy := proto.Clone(card).(*gmproto.SIMCard)
	if copy.GetSIMParticipant().GetID() == "" {
		copy.SIMParticipant = &gmproto.SIMParticipant{ID: conversation.GetDefaultOutgoingID()}
	}
	if copy.GetSIMParticipant().GetID() == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	// Settings are authoritative: never revive an unavailable SIM from an old conversation.
	if p.simSettingsKnown {
		return
	}
	if p.simCards == nil {
		p.simCards = make(map[string]*gmproto.SIMCard)
	}
	p.simCards[copy.GetSIMParticipant().GetID()] = copy
}

func (p *Provider) selectedIdentity(conversationID string) (string, error) {
	if p.instance == "" || !strings.HasPrefix(conversationID, p.instance+"::") {
		return "", fmt.Errorf("invalid conversation ownership")
	}
	if db.DB == nil {
		return "", nil
	}
	var conversation models.Conversation
	err := db.ForProvider(db.DB, p.instance).Conversations().Where("protocol_conv_id = ?", conversationID).First(&conversation).Error
	if err != nil {
		return "", err
	}
	return conversation.OutgoingIdentityID, nil
}

func (p *Provider) GetConversationIdentities(conversationID string) (*core.ConversationIdentities, error) {
	selected, err := p.selectedIdentity(conversationID)
	if err != nil {
		return nil, err
	}
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if client == nil {
		return nil, fmt.Errorf("not connected")
	}
	conversation, err := client.GetConversation(core.StripConvID(conversationID))
	if err != nil {
		return nil, err
	}
	p.rememberConversationSIM(conversation)
	result := &core.ConversationIdentities{Identities: []core.CommunicationIdentity{}, DefaultIdentityID: conversation.GetDefaultOutgoingID(), SelectedIdentityID: selected}
	p.mu.RLock()
	for _, card := range p.simCards {
		result.Identities = append(result.Identities, canonicalIdentity(card))
	}
	p.mu.RUnlock()
	sort.Slice(result.Identities, func(i, j int) bool { return result.Identities[i].ID < result.Identities[j].ID })
	return result, nil
}

func (p *Provider) SetConversationIdentity(conversationID, identityID string) error {
	if _, err := p.selectedIdentity(conversationID); err != nil {
		return err
	}
	if db.DB == nil {
		return fmt.Errorf("database unavailable")
	}
	p.mu.RLock()
	card := p.simCards[identityID]
	p.mu.RUnlock()
	if identityID != "" && card == nil {
		return fmt.Errorf("selected line is unavailable; choose another line")
	}
	return db.Transaction(db.DB, func(tx *gorm.DB) error {
		result := db.ForProvider(tx, p.instance).Conversations().Where("protocol_conv_id = ?", conversationID).Update("outgoing_identity_id", identityID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("conversation not found")
		}
		return nil
	})
}

func (p *Provider) resolveSendingIdentity(conversationID string, conversation *gmproto.Conversation) (*gmproto.SIMCard, error) {
	selected, err := p.selectedIdentity(conversationID)
	if err != nil {
		return nil, err
	}
	p.rememberConversationSIM(conversation)
	p.mu.RLock()
	defer p.mu.RUnlock()
	if selected != "" {
		card := p.simCards[selected]
		if card == nil || card.GetSIMData().GetSIMPayload() == nil {
			return nil, fmt.Errorf("selected line is unavailable; choose another line")
		}
		return proto.Clone(card).(*gmproto.SIMCard), nil
	}
	// Preserve the remote default, including when the phone omits SIM metadata.
	card := &gmproto.SIMCard{SIMParticipant: &gmproto.SIMParticipant{ID: conversation.GetDefaultOutgoingID()}}
	if conversation.GetSimCard() != nil {
		card = proto.Clone(conversation.GetSimCard()).(*gmproto.SIMCard)
		card.SIMParticipant = &gmproto.SIMParticipant{ID: conversation.GetDefaultOutgoingID()}
	}
	return card, nil
}

func applyMessageIdentity(message *models.Message, identity core.CommunicationIdentity) {
	message.LocalIdentityApplicable = true
	message.LocalIdentityID, message.LocalIdentityLabel, message.LocalIdentityAddress = identity.ID, identity.Label, identity.Address
}

func (p *Provider) setMessageIdentity(message *models.Message, remote *gmproto.Message) {
	message.LocalIdentityApplicable = true
	p.mu.Lock()
	defer p.mu.Unlock()
	d := &p.identityDiagnostic
	if !message.IsFromMe {
		d.Incoming++
		if remote.GetSenderParticipant().GetSimPayload() != nil {
			d.IncomingWithSenderPayload++
		}
		// senderParticipant belongs to the remote sender. Its payload is not proof of
		// the receiving local line. Never infer historical routing from today's default.
	} else {
		d.Outgoing++
		id := remote.GetSenderParticipant().GetID().GetParticipantID()
		if id == "" {
			id = remote.GetParticipantID()
		}
		if card := p.simCards[id]; card != nil {
			applyMessageIdentity(message, canonicalIdentity(card))
			d.OutgoingResolved++
		}
	}
	if (d.Incoming+d.Outgoing)%100 == 0 {
		log.Printf("[%s] line diagnostic: incoming=%d outgoing=%d incoming_sender_payload=%d outgoing_resolved=%d", p.instance, d.Incoming, d.Outgoing, d.IncomingWithSenderPayload, d.OutgoingResolved)
	}
}

// Backfill only provable own-sender mappings. A single transaction handles all
// lines; no messages from other provider instances are enumerated or updated.
func (p *Provider) reconcileMessageIdentities(identities []core.CommunicationIdentity) error {
	if p.instance == "" {
		return fmt.Errorf("provider instance ID is required")
	}
	if db.DB == nil {
		return nil
	}
	return db.Transaction(db.DB, func(tx *gorm.DB) error {
		if err := db.ForProvider(tx, p.instance).Messages().Where("local_identity_applicable IS NULL OR local_identity_applicable = ?", false).Update("local_identity_applicable", true).Error; err != nil {
			return err
		}
		for _, identity := range identities {
			if identity.ID == "" {
				continue
			}
			if err := db.ForProvider(tx, p.instance).Messages().Where("is_from_me = ? AND sender_id = ? AND (local_identity_id IS NULL OR local_identity_id = '')", true, googleMessagesParticipantID(identity.ID)).Updates(map[string]any{
				"local_identity_id": identity.ID, "local_identity_label": identity.Label, "local_identity_address": identity.Address,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
