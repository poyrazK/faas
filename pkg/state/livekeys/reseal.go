package livekeys

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state/livekeys/sqlc"
)

// Sealed is one stored live key or login as the operator re-seal reads it
// (ADR-20261016151947, at-rest binding, release 2). ID is the key ID of a key
// and the database ID of a login.
type Sealed struct {
	ID         string
	DatabaseID string
	Blob       []byte
	Recipient  string
}

// SealedKeysAfter returns up to limit live keys whose key ID sorts after
// after, in key-ID order: every key, retiring ones too, on every database.
func (s *Store) SealedKeysAfter(ctx context.Context, after string, limit int) ([]Sealed, error) {
	rows, err := s.q.SealedLiveKeysAfter(ctx, s.pool, sqlc.SealedLiveKeysAfterParams{After: after, PageSize: int32(limit)}) //nolint:gosec // a page size, never near int32's range
	if err != nil {
		return nil, fmt.Errorf("livekeys: page live keys: %w", err)
	}
	out := make([]Sealed, 0, len(rows))
	for _, r := range rows {
		out = append(out, Sealed{ID: r.Kid, DatabaseID: r.DatabaseID, Blob: r.SealedSecret, Recipient: r.Recipient})
	}
	return out, nil
}

// ResealKey replaces key's seal with blob, only while the row still holds
// key.Blob. It reports false when the key was rotated, revoked or re-sealed
// meanwhile, and leaves the row as it is.
func (s *Store) ResealKey(ctx context.Context, key Sealed, blob []byte) (bool, error) {
	n, err := s.q.ResealLiveKey(ctx, s.pool, sqlc.ResealLiveKeyParams{SealedSecret: blob, Kid: key.ID, DatabaseID: key.DatabaseID, Previous: key.Blob, Recipient: key.Recipient})
	if err != nil {
		return false, fmt.Errorf("livekeys: re-seal live key: %w", err)
	}
	return n == 1, nil
}

// SealedLoginsAfter returns up to limit live logins whose database ID sorts
// after after, in database-ID order.
func (s *Store) SealedLoginsAfter(ctx context.Context, after string, limit int) ([]Sealed, error) {
	rows, err := s.q.SealedLiveLoginsAfter(ctx, s.pool, sqlc.SealedLiveLoginsAfterParams{After: after, PageSize: int32(limit)}) //nolint:gosec // a page size, never near int32's range
	if err != nil {
		return nil, fmt.Errorf("livekeys: page live logins: %w", err)
	}
	out := make([]Sealed, 0, len(rows))
	for _, r := range rows {
		out = append(out, Sealed{ID: r.DatabaseID, DatabaseID: r.DatabaseID, Blob: r.SealedLogin, Recipient: r.Recipient})
	}
	return out, nil
}

// ResealLogin replaces login's seal with blob, only while the row still holds
// login.Blob; see ResealKey.
func (s *Store) ResealLogin(ctx context.Context, login Sealed, blob []byte) (bool, error) {
	n, err := s.q.ResealLiveLogin(ctx, s.pool, sqlc.ResealLiveLoginParams{SealedLogin: blob, DatabaseID: login.DatabaseID, Previous: login.Blob, Recipient: login.Recipient})
	if err != nil {
		return false, fmt.Errorf("livekeys: re-seal live login: %w", err)
	}
	return n == 1, nil
}
