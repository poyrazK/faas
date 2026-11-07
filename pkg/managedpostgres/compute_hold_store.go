package managedpostgres

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
)

// ComputeHoldStore fences a cap provider operation with the same durable
// lease lifecycle operations check. Claiming preserves state and diagnostics;
// a stale snapshot or another live lease is ErrConflict.
type ComputeHoldStore interface {
	ClaimComputeHold(context.Context, Database, string, time.Time, time.Time) (Database, error)
	ReleaseComputeHold(context.Context, string, string, time.Time) error
}

func (s *MemoryStore) ClaimComputeHold(ctx context.Context, expected Database, token string, now, until time.Time) (Database, error) {
	if token == "" || now.IsZero() || !until.After(now) || !capHoldable(expected) {
		return Database{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := db.LockMemoryIntent(ctx)
	if err != nil {
		return Database{}, err
	}
	defer unlock()
	current, ok := s.databases[expected.ID]
	if !ok || current.AccountID != expected.AccountID {
		return Database{}, ErrNotFound
	}
	if current.State != expected.State || current.DesiredGeneration != expected.DesiredGeneration ||
		current.BackendID != expected.BackendID || current.BackendFingerprint != expected.BackendFingerprint ||
		current.ProviderResourceID != expected.ProviderResourceID || databaseDataResource(current) != databaseDataResource(expected) ||
		current.LeaseUntil.After(now) {
		return Database{}, ErrConflict
	}
	current.LeaseToken, current.LeaseUntil = token, until
	s.databases[current.ID] = current
	return cloneDatabase(current), nil
}

func (s *MemoryStore) ReleaseComputeHold(ctx context.Context, id, token string, now time.Time) error {
	if token == "" || now.IsZero() {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := db.LockMemoryIntent(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	current, ok := s.databases[id]
	if !ok {
		return ErrNotFound
	}
	if !capHoldable(current) || current.LeaseToken != token || !current.LeaseUntil.After(now) {
		return ErrConflict
	}
	current.LeaseToken, current.LeaseUntil = "", time.Time{}
	s.databases[id] = current
	return nil
}
