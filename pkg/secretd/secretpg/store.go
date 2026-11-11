// Package secretpg is secretd's Postgres store (ADR-20261110210000 §3).
package secretpg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/secretd"
	"github.com/onebox-faas/faas/pkg/secretd/secretpg/sqlc"
	"github.com/onebox-faas/faas/pkg/state"
)

// Store reads apps and nodes through the state store and writes secretd's
// grants and audit. db is a pool or connection logged in as faas_secretd.
type Store struct {
	db    sqlc.DBTX
	q     *sqlc.Queries
	state *state.PgStore
}

// New returns a store over db; st reads apps, accounts and nodes.
func New(db sqlc.DBTX, st *state.PgStore) *Store {
	return &Store{db: db, q: sqlc.New(), state: st}
}

var _ secretd.Store = (*Store)(nil)

// NodeIDByName implements secretd.Store: only an active node answers.
func (s *Store) NodeIDByName(ctx context.Context, name string) (string, error) {
	node, err := s.state.ComputeNodeByName(ctx, name)
	if errors.Is(err, state.ErrNotFound) {
		return "", secretd.ErrUnknownNode
	}
	if err != nil {
		return "", fmt.Errorf("secretpg: node by name: %w", err)
	}
	if !node.Active {
		return "", secretd.ErrUnknownNode
	}
	return node.ID, nil
}

// AppTarget implements secretd.Store.
func (s *Store) AppTarget(ctx context.Context, appID string) (secretd.AppTarget, error) {
	app, err := s.state.AppByID(ctx, appID)
	if err != nil {
		return secretd.AppTarget{}, fmt.Errorf("secretpg: app: %w", err)
	}
	account, err := state.AppResourceAccount(ctx, s.state, app)
	if err != nil {
		return secretd.AppTarget{}, fmt.Errorf("secretpg: app resource account: %w", err)
	}
	return secretd.AppTarget{OwnerNodeID: app.NodeID, AccountID: account.ID}, nil
}

func pgUUID(id string) (pgtype.UUID, error) {
	u, err := uuid.Parse(id)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("secretpg: bad id %q: %w", id, err)
	}
	return pgtype.UUID{Bytes: u, Valid: true}, nil
}

func pgTime(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

// GrantValid implements secretd.Store.
func (s *Store) GrantValid(ctx context.Context, appID, nodeID string, at time.Time) (bool, error) {
	app, err := pgUUID(appID)
	if err != nil {
		return false, err
	}
	node, err := pgUUID(nodeID)
	if err != nil {
		return false, err
	}
	return s.q.SecretGrantValid(ctx, s.db, sqlc.SecretGrantValidParams{AppID: app, NodeID: node, At: pgTime(at)})
}

// UpsertGrant implements secretd.Store.
func (s *Store) UpsertGrant(ctx context.Context, appID, nodeID, grantedByNodeID string, expiresAt time.Time) (time.Time, error) {
	app, err := pgUUID(appID)
	if err != nil {
		return time.Time{}, err
	}
	node, err := pgUUID(nodeID)
	if err != nil {
		return time.Time{}, err
	}
	by, err := pgUUID(grantedByNodeID)
	if err != nil {
		return time.Time{}, err
	}
	got, err := s.q.UpsertSecretGrant(ctx, s.db, sqlc.UpsertSecretGrantParams{
		AppID: app, NodeID: node, GrantedByNodeID: by, ExpiresAt: pgTime(expiresAt),
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("secretpg: upsert grant: %w", err)
	}
	return got.Time, nil
}

// RecordOpen implements secretd.Store.
func (s *Store) RecordOpen(ctx context.Context, nodeID, appID, via string, entries int) error {
	app, err := pgUUID(appID)
	if err != nil {
		return err
	}
	node, err := pgUUID(nodeID)
	if err != nil {
		return err
	}
	if err := s.q.InsertSecretOpenAudit(ctx, s.db, sqlc.InsertSecretOpenAuditParams{
		NodeID: node, AppID: app, Via: via, Entries: int32(min(entries, 1<<30)),
	}); err != nil {
		return fmt.Errorf("secretpg: record open: %w", err)
	}
	return nil
}

// CaptureKey implements secretd.Store: a live sealed capture's app and key.
func (s *Store) CaptureKey(ctx context.Context, captureID string) (string, []byte, error) {
	id, err := pgUUID(captureID)
	if err != nil {
		return "", nil, secretd.ErrUnknownCapture
	}
	row, err := s.q.CrashCaptureSealedKey(ctx, s.db, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, secretd.ErrUnknownCapture
	}
	if err != nil {
		return "", nil, fmt.Errorf("secretpg: capture key: %w", err)
	}
	return uuid.UUID(row.AppID.Bytes).String(), row.SealedKey, nil
}

// DeleteExpiredGrants removes grants that expired before before.
func (s *Store) DeleteExpiredGrants(ctx context.Context, before time.Time) (int64, error) {
	return s.q.DeleteExpiredSecretGrants(ctx, s.db, pgTime(before))
}
