package main

import (
	"Loom/pkg/models"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"sort"
	"sync"
)

type contactSnapshot struct {
	revision string
	hashes   map[uint][32]byte
}

// Retain fingerprints only, not multiple copies of the contact graph. A client
// older than this bounded history receives a full authoritative reset.
type contactChangesCache struct {
	mu      sync.Mutex
	history []contactSnapshot
}

func (cache *contactChangesCache) changes(contacts []models.MetaContact, previous string) (models.ContactChanges, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	ordered := append([]models.MetaContact(nil), contacts...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	current := contactSnapshot{hashes: make(map[uint][32]byte, len(ordered))}
	digest := sha256.New()
	for _, contact := range ordered {
		encoded, err := json.Marshal(contact)
		if err != nil {
			return models.ContactChanges{}, err
		}
		hash := sha256.Sum256(encoded)
		current.hashes[contact.ID] = hash
		var id [8]byte
		binary.LittleEndian.PutUint64(id[:], uint64(contact.ID))
		_, _ = digest.Write(id[:])
		_, _ = digest.Write(hash[:])
	}
	current.revision = hex.EncodeToString(digest.Sum(nil))
	result := models.ContactChanges{Revision: current.revision}
	var known *contactSnapshot
	for i := range cache.history {
		if cache.history[i].revision == previous {
			known = &cache.history[i]
			break
		}
	}
	if previous == "" || known == nil {
		result.Reset = true
		result.Contacts = ordered
		if result.Contacts == nil {
			result.Contacts = []models.MetaContact{}
		}
	} else if previous != current.revision {
		for _, contact := range ordered {
			if old, exists := known.hashes[contact.ID]; !exists || old != current.hashes[contact.ID] {
				result.Contacts = append(result.Contacts, contact)
			}
		}
		for id := range known.hashes {
			if _, exists := current.hashes[id]; !exists {
				result.RemovedIDs = append(result.RemovedIDs, id)
			}
		}
		sort.Slice(result.RemovedIDs, func(i, j int) bool { return result.RemovedIDs[i] < result.RemovedIDs[j] })
	}
	if len(cache.history) == 0 || cache.history[len(cache.history)-1].revision != current.revision {
		cache.history = append(cache.history, current)
		if len(cache.history) > 4 {
			cache.history = cache.history[len(cache.history)-4:]
		}
	}
	return result, nil
}

// GetMetaContactChanges is a provider-neutral, revisioned snapshot contract.
// Old clients can keep using GetMetaContacts unchanged.
func (a *App) GetMetaContactChanges(previousRevision string) (models.ContactChanges, error) {
	contacts, err := a.GetMetaContacts()
	if err != nil {
		return models.ContactChanges{}, err
	}
	return a.contactChanges.changes(contacts, previousRevision)
}
