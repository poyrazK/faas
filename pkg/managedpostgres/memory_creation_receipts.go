package managedpostgres

import (
	"context"
	"time"
)

func creationReceiptKey(kind, backend, resource string) string {
	return kind + "\x00" + backend + "\x00" + resource
}

func (s *MemoryStore) RecordCreationReceipt(_ context.Context, r CreationReceipt, lease string) error {
	if r.Validate() != nil {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Kind == "restore" {
		d, ok := s.databases[r.DatabaseID]
		if !ok {
			return ErrNotFound
		}
		if lease == "" || d.LeaseToken != lease || !d.LeaseUntil.After(time.Now()) || d.State != StateProvisioning || !d.AccountingRequired ||
			!sameCreationReceipt(r, restoreCreationReceipt(d, r.Acknowledgement)) || d.RestoreSourceResourceID != r.Acknowledgement.SourceResourceID {
			return ErrConflict
		}
	}
	key := creationReceiptKey(r.Kind, r.BackendID, r.ResourceID)
	if old, ok := s.creationReceipts[key]; ok {
		if !sameCreationReceipt(old, r) {
			return ErrConflict
		}
		return nil // Replay must retain the monotonic cleanup checkpoint.
	}
	for _, old := range s.creationReceipts {
		if old.BackendID == r.BackendID && old.BackendFingerprint == r.BackendFingerprint && old.Acknowledgement.ProviderResourceID == r.Acknowledgement.ProviderResourceID && !sameCreationReceipt(old, r) {
			return ErrConflict
		}
	}
	s.creationReceipts[key] = r
	return nil
}

func (s *MemoryStore) GetCreationReceipt(_ context.Context, kind, backend, resource string) (CreationReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.creationReceipts[creationReceiptKey(kind, backend, resource)]
	if !ok {
		return CreationReceipt{}, ErrNotFound
	}
	return r, nil
}

func (s *MemoryStore) RecordCreationCleanup(_ context.Context, r CreationReceipt, lease string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := creationReceiptKey(r.Kind, r.BackendID, r.ResourceID)
	old, ok := s.creationReceipts[key]
	if !ok {
		return ErrNotFound
	}
	if !sameCreationReceipt(old, r) {
		return ErrConflict
	}
	if r.Kind == "restore" {
		d, ok := s.databases[r.DatabaseID]
		if !ok || lease == "" || d.LeaseToken != lease || !d.LeaseUntil.After(time.Now()) || d.State != StateDeleting ||
			!sameCreationReceipt(r, restoreCreationReceipt(d, r.Acknowledgement)) || d.RestoreSourceResourceID != r.Acknowledgement.SourceResourceID {
			return ErrConflict
		}
	}
	if old.CleanupStartedAt.IsZero() {
		old.CleanupStartedAt = time.Now().UTC()
	}
	s.creationReceipts[key] = old
	return nil
}
