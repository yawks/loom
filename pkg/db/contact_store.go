package db

import (
	"Loom/pkg/models"
	"fmt"
	"sync"
)

// ContactStore is the global in-memory store for MetaContact and LinkedAccount records.
// It is a write-through cache: SQLite remains the source of truth, but all reads come
// from memory. This eliminates the full-table scans on linked_accounts / meta_contacts
// that caused SQLite lock contention at startup.
var ContactStore = &contactStore{}

type contactStore struct {
	mu sync.RWMutex

	metaContacts   map[uint]models.MetaContact
	linkedAccounts map[uint]models.LinkedAccount

	// byProviderUser: "instanceID\x00userID" → LinkedAccount.ID
	byProviderUser map[string]uint
	// byMetaContact: MetaContactID → ordered slice of LinkedAccount.IDs
	byMetaContact map[uint][]uint
	// convByLinkedAccount: LinkedAccount.ID → ProtocolConvID (first/canonical conversation)
	convByLinkedAccount map[uint]string
}

func providerUserKey(instanceID, userID string) string {
	return instanceID + "\x00" + userID
}

// Load reads all MetaContacts and LinkedAccounts from the DB into memory.
// Must be called once after InitDatabase, before any provider starts.
func (s *contactStore) Load() error {
	if DB == nil {
		return fmt.Errorf("DB not initialized")
	}

	var mcs []models.MetaContact
	if err := DB.Find(&mcs).Error; err != nil {
		return fmt.Errorf("contact store: failed to load meta_contacts: %w", err)
	}

	var las []models.LinkedAccount
	if err := DB.Find(&las).Error; err != nil {
		return fmt.Errorf("contact store: failed to load linked_accounts: %w", err)
	}

	// Load canonical conversation IDs (ORDER BY id ASC so the first row per
	// linked_account_id wins, matching the GetMetaContacts query behaviour).
	type convRow struct {
		LinkedAccountID uint
		ProtocolConvID  string
	}
	var convRows []convRow
	if err := DB.Table("conversations").
		Select("linked_account_id, protocol_conv_id").
		Order("id ASC").
		Scan(&convRows).Error; err != nil {
		return fmt.Errorf("contact store: failed to load conversations: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.metaContacts = make(map[uint]models.MetaContact, len(mcs))
	for _, mc := range mcs {
		s.metaContacts[mc.ID] = mc
	}

	s.linkedAccounts = make(map[uint]models.LinkedAccount, len(las))
	s.byProviderUser = make(map[string]uint, len(las))
	s.byMetaContact = make(map[uint][]uint)

	for _, la := range las {
		s.linkedAccounts[la.ID] = la
		if la.ProviderInstanceID != "" && la.UserID != "" {
			s.byProviderUser[providerUserKey(la.ProviderInstanceID, la.UserID)] = la.ID
		}
		if la.MetaContactID > 0 {
			s.appendToMetaIndex(la.ID, la.MetaContactID)
		}
	}

	// Build the conversations index — first row per LinkedAccountID wins.
	s.convByLinkedAccount = make(map[uint]string, len(convRows))
	for _, row := range convRows {
		if _, exists := s.convByLinkedAccount[row.LinkedAccountID]; !exists {
			s.convByLinkedAccount[row.LinkedAccountID] = row.ProtocolConvID
		}
	}

	fmt.Printf("ContactStore: loaded %d meta_contacts, %d linked_accounts, %d conversations\n",
		len(mcs), len(las), len(s.convByLinkedAccount))
	return nil
}

// GetAll returns all MetaContacts that have at least one LinkedAccount, with
// LinkedAccounts assembled — no DB query. MetaContacts without any LinkedAccount
// are skipped; they are orphan data and cannot be displayed in the UI.
// LinkedAccounts is always a non-nil slice so the frontend receives [] not null.
func (s *contactStore) GetAll() []models.MetaContact {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]models.MetaContact, 0, len(s.metaContacts))
	for _, mc := range s.metaContacts {
		laIDs, hasLinkedAccounts := s.byMetaContact[mc.ID]
		if !hasLinkedAccounts || len(laIDs) == 0 {
			continue // Skip orphan MetaContacts — no provider account, nothing to show
		}

		mcCopy := mc
		mcCopy.LinkedAccounts = make([]models.LinkedAccount, 0, len(laIDs))
		for _, laID := range laIDs {
			if la, ok := s.linkedAccounts[laID]; ok {
				mcCopy.LinkedAccounts = append(mcCopy.LinkedAccounts, la)
			}
		}
		// Only include if we actually found LinkedAccounts in the store
		if len(mcCopy.LinkedAccounts) > 0 {
			result = append(result, mcCopy)
		}
	}
	return result
}

// FindByProviderUser looks up a LinkedAccount by (providerInstanceID, userID).
func (s *contactStore) FindByProviderUser(instanceID, userID string) (models.LinkedAccount, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byProviderUser[providerUserKey(instanceID, userID)]
	if !ok {
		return models.LinkedAccount{}, false
	}
	la, ok := s.linkedAccounts[id]
	return la, ok
}

// FindByProvider returns all LinkedAccounts for a provider instance.
func (s *contactStore) FindByProvider(instanceID string) []models.LinkedAccount {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []models.LinkedAccount
	for _, la := range s.linkedAccounts {
		if la.ProviderInstanceID == instanceID {
			result = append(result, la)
		}
	}
	return result
}

// FindMetaContact returns a MetaContact by ID.
func (s *contactStore) FindMetaContact(id uint) (models.MetaContact, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	mc, ok := s.metaContacts[id]
	return mc, ok
}

// UpsertLinkedAccount reflects a DB write into the in-memory store.
func (s *contactStore) UpsertLinkedAccount(la models.LinkedAccount) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if old, exists := s.linkedAccounts[la.ID]; exists {
		// Remove stale indices.
		delete(s.byProviderUser, providerUserKey(old.ProviderInstanceID, old.UserID))
		if old.MetaContactID != la.MetaContactID {
			s.removeFromMetaIndex(la.ID, old.MetaContactID)
		}
	}

	s.linkedAccounts[la.ID] = la
	if la.ProviderInstanceID != "" && la.UserID != "" {
		s.byProviderUser[providerUserKey(la.ProviderInstanceID, la.UserID)] = la.ID
	}
	if la.MetaContactID > 0 {
		s.appendToMetaIndex(la.ID, la.MetaContactID)
	}
}

// UpsertMetaContact reflects a DB write into the in-memory store.
func (s *contactStore) UpsertMetaContact(mc models.MetaContact) {
	s.mu.Lock()
	s.metaContacts[mc.ID] = mc
	s.mu.Unlock()
}

// DeleteLinkedAccount removes a LinkedAccount from the in-memory store.
func (s *contactStore) DeleteLinkedAccount(id uint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, exists := s.linkedAccounts[id]; exists {
		delete(s.byProviderUser, providerUserKey(old.ProviderInstanceID, old.UserID))
		s.removeFromMetaIndex(id, old.MetaContactID)
		delete(s.linkedAccounts, id)
	}
}

// GetConversation returns the canonical ProtocolConvID for a LinkedAccount, or "".
func (s *contactStore) GetConversation(linkedAccountID uint) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.convByLinkedAccount[linkedAccountID]
}

// UpsertConversation registers (or keeps) the canonical conversation for a LinkedAccount.
// Only the first call per LinkedAccountID sets the value; subsequent calls are no-ops
// (matching the ORDER BY id ASC / first-wins behaviour of the DB query it replaces).
func (s *contactStore) UpsertConversation(linkedAccountID uint, protocolConvID string) {
	if linkedAccountID == 0 || protocolConvID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.convByLinkedAccount[linkedAccountID]; !exists {
		s.convByLinkedAccount[linkedAccountID] = protocolConvID
	}
}

// SetConversation forces the canonical conversation for a LinkedAccount (use when
// the old canonical conversation was deleted and a new one should replace it).
func (s *contactStore) SetConversation(linkedAccountID uint, protocolConvID string) {
	if linkedAccountID == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if protocolConvID == "" {
		delete(s.convByLinkedAccount, linkedAccountID)
	} else {
		s.convByLinkedAccount[linkedAccountID] = protocolConvID
	}
}

func (s *contactStore) appendToMetaIndex(laID, metaID uint) {
	for _, id := range s.byMetaContact[metaID] {
		if id == laID {
			return
		}
	}
	s.byMetaContact[metaID] = append(s.byMetaContact[metaID], laID)
}

func (s *contactStore) removeFromMetaIndex(laID, metaID uint) {
	ids := s.byMetaContact[metaID]
	for i, id := range ids {
		if id == laID {
			s.byMetaContact[metaID] = append(ids[:i], ids[i+1:]...)
			return
		}
	}
}
