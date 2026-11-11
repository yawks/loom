package models

// ContactChanges either replaces the complete snapshot (Reset), or upserts
// Contacts and removes RemovedIDs from the snapshot identified by the caller.
// Revision is opaque; clients must not infer ordering or provider identity.
type ContactChanges struct {
	Revision   string        `json:"revision"`
	Reset      bool          `json:"reset"`
	Contacts   []MetaContact `json:"contacts,omitempty"`
	RemovedIDs []uint        `json:"removedIds,omitempty"`
}
