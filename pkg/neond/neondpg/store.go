// Package neondpg is neond's Postgres store for project keys
// (ADR-20261112160000 decision 3). Only faas_neond can read or write
// neon_project_keys.
package neondpg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/neond"
	"github.com/onebox-faas/faas/pkg/neond/neondpg/sqlc"
)

// Store implements neond.KeyStore. db is a pool or connection logged in as
// faas_neond.
type Store struct {
	db sqlc.DBTX
	q  *sqlc.Queries
}

// New returns a store over db.
func New(db sqlc.DBTX) *Store { return &Store{db: db, q: sqlc.New()} }

var _ neond.KeyStore = (*Store)(nil)

// LiveProjectKey implements neond.KeyStore.
func (s *Store) LiveProjectKey(ctx context.Context, project string) (neond.StoredKey, bool, error) {
	row, err := s.q.LiveProjectKey(ctx, s.db, project)
	if errors.Is(err, pgx.ErrNoRows) {
		return neond.StoredKey{}, false, nil
	}
	if err != nil {
		return neond.StoredKey{}, false, fmt.Errorf("neondpg: read project key of %s: %w", project, err)
	}
	return neond.StoredKey{KeyID: row.KeyID, Sealed: row.SealedKey}, true, nil
}

// InsertProjectKey implements neond.KeyStore.
func (s *Store) InsertProjectKey(ctx context.Context, project string, keyID int64, sealed []byte) error {
	if err := s.q.InsertProjectKey(ctx, s.db, sqlc.InsertProjectKeyParams{KeyID: keyID, ProjectID: project, SealedKey: sealed}); err != nil {
		return fmt.Errorf("neondpg: store project key %d of %s: %w", keyID, project, err)
	}
	return nil
}

// LiveProjectKeyIDs implements neond.KeyStore.
func (s *Store) LiveProjectKeyIDs(ctx context.Context, project string) ([]int64, error) {
	ids, err := s.q.LiveProjectKeyIDs(ctx, s.db, project)
	if err != nil {
		return nil, fmt.Errorf("neondpg: list project keys of %s: %w", project, err)
	}
	return ids, nil
}

// MarkProjectKeyRevoked implements neond.KeyStore.
func (s *Store) MarkProjectKeyRevoked(ctx context.Context, keyID int64) error {
	if _, err := s.q.MarkProjectKeyRevoked(ctx, s.db, keyID); err != nil {
		return fmt.Errorf("neondpg: mark project key %d revoked: %w", keyID, err)
	}
	return nil
}

// LiveProjectKeys implements neond.KeyStore.
func (s *Store) LiveProjectKeys(ctx context.Context) ([]neond.LiveKey, error) {
	rows, err := s.q.LiveProjectKeys(ctx, s.db)
	if err != nil {
		return nil, fmt.Errorf("neondpg: list live project keys: %w", err)
	}
	out := make([]neond.LiveKey, 0, len(rows))
	for _, row := range rows {
		out = append(out, neond.LiveKey{KeyID: row.KeyID, Project: row.ProjectID, CreatedAt: row.CreatedAt.Time})
	}
	return out, nil
}

// ReplaceProjectKey implements neond.KeyStore.
func (s *Store) ReplaceProjectKey(ctx context.Context, project string, oldKeyID, newKeyID int64, sealed []byte) error {
	n, err := s.q.ReplaceProjectKey(ctx, s.db, sqlc.ReplaceProjectKeyParams{NewKeyID: newKeyID, SealedKey: sealed, OldKeyID: oldKeyID, ProjectID: project})
	if err != nil {
		return fmt.Errorf("neondpg: replace project key %d of %s: %w", oldKeyID, project, err)
	}
	if n != 1 {
		return fmt.Errorf("neondpg: replace project key %d of %s: %w", oldKeyID, project, neond.ErrKeyNotLive)
	}
	return nil
}
