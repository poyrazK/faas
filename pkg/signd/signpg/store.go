// Package signpg is signd's Postgres store (ADR-20261109170000 §3).
package signpg

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/signd"
	"github.com/onebox-faas/faas/pkg/signd/signpg/sqlc"
	"github.com/onebox-faas/faas/pkg/state"
)

// Store reads build claims and records signatures. db is a pool or a
// connection logged in as faas_signd.
type Store struct {
	db sqlc.DBTX
	q  *sqlc.Queries
}

// New returns a store over db.
func New(db sqlc.DBTX) *Store { return &Store{db: db, q: sqlc.New()} }

var _ signd.Store = (*Store)(nil)

// BuildClaim implements signd.Store.
func (s *Store) BuildClaim(ctx context.Context, deploymentID string) (signd.Claim, error) {
	id, err := pgUUID(deploymentID)
	if err != nil {
		return signd.Claim{}, err
	}
	row, err := s.q.BuildClaim(ctx, s.db, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return signd.Claim{}, signd.ErrNoClaim
	}
	if err != nil {
		return signd.Claim{}, fmt.Errorf("signpg: build claim: %w", err)
	}
	return signd.Claim{Status: state.DeploymentStatus(row.Status), Sidecars: row.Sidecars, AppSlug: row.Slug,
		NodeName: row.NodeName, Phase: state.ImagePreparationPhase(row.Phase)}, nil
}

// RecordSignature implements signd.Store. The insert and the read run on
// one statement each; the primary key makes the first insert for a key the
// only one, so a racing second request reads the winner's row.
func (s *Store) RecordSignature(ctx context.Context, r signd.Record) (signd.Record, bool, error) {
	id, err := pgUUID(r.DeploymentID)
	if err != nil {
		return signd.Record{}, false, err
	}
	n, err := s.q.InsertArtifactSignature(ctx, s.db, sqlc.InsertArtifactSignatureParams{StorageKey: r.StorageKey,
		Digest: r.Digest, Bytes: r.Bytes, DeploymentID: id, NodeName: r.NodeName, SignatureFormat: r.Format})
	if err != nil {
		return signd.Record{}, false, fmt.Errorf("signpg: insert signature: %w", err)
	}
	if n == 1 {
		return r, true, nil
	}
	row, err := s.q.ArtifactSignatureByKey(ctx, s.db, r.StorageKey)
	if err != nil {
		return signd.Record{}, false, fmt.Errorf("signpg: read signature: %w", err)
	}
	return signd.Record{StorageKey: row.StorageKey, Digest: row.Digest, Bytes: row.Bytes, DeploymentID: row.DeploymentID,
		NodeName: row.NodeName, Format: row.SignatureFormat}, false, nil
}

func pgUUID(s string) (pgtype.UUID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("signpg: deployment id: %w", err)
	}
	return pgtype.UUID{Bytes: u, Valid: true}, nil
}
