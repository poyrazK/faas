package livekeys

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

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
	// Signature is the row's writer signature; empty when unsigned.
	Signature []byte
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
		out = append(out, Sealed{ID: r.Kid, DatabaseID: r.DatabaseID, Blob: r.SealedSecret, Recipient: r.Recipient, Signature: r.SealedSecretSig})
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
		out = append(out, Sealed{ID: r.DatabaseID, DatabaseID: r.DatabaseID, Blob: r.SealedLogin, Recipient: r.Recipient, Signature: r.SealedLoginSig})
	}
	return out, nil
}

// DropLegacyLogin removes login, only while the row still holds login.Blob
// and names login.Recipient; see ResealKey. apid issues the database a new
// login (IssueMissingLogin).
func (s *Store) DropLegacyLogin(ctx context.Context, login Sealed) (bool, error) {
	n, err := s.q.DropLegacyLiveLogin(ctx, s.pool, sqlc.DropLegacyLiveLoginParams{DatabaseID: login.DatabaseID, Previous: login.Blob, Recipient: login.Recipient})
	if err != nil {
		return false, fmt.Errorf("livekeys: drop legacy live login: %w", err)
	}
	return n == 1, nil
}

// SignKey stores sig on an unsigned key, only while the row still holds the
// ciphertext and recipient the caller read (the operator backfill, ADR
// 20261028161502).
func (s *Store) SignKey(ctx context.Context, key Sealed, sig []byte) (bool, error) {
	n, err := s.q.SignLiveKey(ctx, s.pool, sqlc.SignLiveKeyParams{SealedSecretSig: sig, Kid: key.ID, DatabaseID: key.DatabaseID,
		SealedSecret: key.Blob, Recipient: key.Recipient})
	if err != nil {
		return false, fmt.Errorf("livekeys: sign live key: %w", err)
	}
	return n == 1, nil
}

// SignLogin is SignKey for a login.
func (s *Store) SignLogin(ctx context.Context, login Sealed, sig []byte) (bool, error) {
	n, err := s.q.SignLiveLogin(ctx, s.pool, sqlc.SignLiveLoginParams{SealedLoginSig: sig, DatabaseID: login.DatabaseID,
		SealedLogin: login.Blob, Recipient: login.Recipient})
	if err != nil {
		return false, fmt.Errorf("livekeys: sign live login: %w", err)
	}
	return n == 1, nil
}

// LoginRef names a database and the account that owns it.
type LoginRef struct{ AccountID, DatabaseID string }

// DatabasesWithoutLogin lists up to limit databases that have an active live
// key and no login, in database-ID order.
func (s *Store) DatabasesWithoutLogin(ctx context.Context, limit int) ([]LoginRef, error) {
	rows, err := s.q.LiveDatabasesWithoutLogin(ctx, s.pool, int32(limit)) //nolint:gosec // a page size, never near int32's range
	if err != nil {
		return nil, fmt.Errorf("livekeys: list databases without a login: %w", err)
	}
	out := make([]LoginRef, 0, len(rows))
	for _, r := range rows {
		out = append(out, LoginRef{AccountID: r.AccountID, DatabaseID: r.DatabaseID})
	}
	return out, nil
}

// IssueMissingLogin stores the login issue returns for a database that, once
// locked, still has an active key and no login; it reports whether it stored
// one. issue runs under the lock, as in Enable, so the login stored is the
// one issued last.
func (s *Store) IssueMissingLogin(ctx context.Context, accountID, databaseID string, issue func(context.Context) (*Login, error)) (bool, error) {
	stored := false
	err := s.locked(ctx, accountID, databaseID, func(tx pgx.Tx) error {
		active, err := s.q.HasActiveLiveKey(ctx, tx, databaseID)
		if err != nil {
			return fmt.Errorf("livekeys: read active key: %w", err)
		}
		existing, err := s.q.LiveLoginOf(ctx, tx, sqlc.LiveLoginOfParams{DatabaseID: databaseID, AccountID: accountID})
		if err != nil {
			return fmt.Errorf("livekeys: read login: %w", err)
		}
		if !active || len(existing) > 0 {
			return nil
		}
		login, err := issue(ctx)
		if err != nil || login == nil {
			return err
		}
		err = s.q.UpsertLiveLogin(ctx, tx, sqlc.UpsertLiveLoginParams{DatabaseID: databaseID, AccountID: accountID,
			SealedLogin: login.Sealed, Recipient: login.Recipient, ProviderIdentityID: login.Identity, Slot: login.Slot, Awake: login.Awake,
			SealedLoginSig: login.Signature})
		if err != nil {
			return fmt.Errorf("livekeys: store login: %w", err)
		}
		stored = true
		return nil
	})
	return stored, err
}
