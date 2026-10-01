package state

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/devbridge"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// DevBridgeStore is optional during rolling upgrades. Session credentials are
// never stored in plaintext; reads retain digests only for trusted verifiers.
type DevBridgeStore interface {
	CreateDevBridge(context.Context, devbridge.Session) error
	DevBridgeByID(context.Context, string, string) (devbridge.Session, error)
	ListDevBridges(context.Context, string, int) ([]devbridge.Session, error)
	RevokeDevBridge(context.Context, string, string, time.Time) error
}

func (s *PgStore) CreateDevBridge(ctx context.Context, session devbridge.Session) error {
	account, err := parsePgUUID(session.Scope.AccountID)
	if err != nil {
		return err
	}
	app, err := parsePgUUID(session.Scope.TargetAppID)
	if err != nil {
		return err
	}
	env, err := parsePgUUID(session.Scope.EnvironmentID)
	if err != nil {
		return err
	}
	scope, err := json.Marshal(session.Scope)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	plan, err := q.LockDevBridgeAccount(ctx, tx, account)
	if err != nil {
		return err
	}
	limit := api.MustLimitsFor(api.Plan(plan)).DeveloperApps
	if err := q.PruneDevBridgeSessions(ctx, tx, sqlc.PruneDevBridgeSessionsParams{AccountID: account, ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(-api.DevBridgeMetadataRetention), Valid: true}}); err != nil {
		return err
	}
	affected, err := q.CreateDevBridge(ctx, tx, sqlc.CreateDevBridgeParams{
		ID: session.ID, AccountID: account, TargetAppID: app, EnvironmentID: env, Scope: scope,
		AttachmentDigest: session.AttachmentDigest[:], RequestDigest: session.RequestDigest[:],
		ExpiresAt: pgtype.Timestamptz{Time: session.ExpiresAt, Valid: true}, MaxSessions: int32(limit),
	})
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}

func (s *PgStore) DevBridgeByID(ctx context.Context, accountID, id string) (devbridge.Session, error) {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return devbridge.Session{}, ErrNotFound
	}
	row, err := sqlc.New().DevBridgeByID(ctx, s.pool, sqlc.DevBridgeByIDParams{ID: id, AccountID: account})
	if errors.Is(err, pgx.ErrNoRows) {
		return devbridge.Session{}, ErrNotFound
	}
	if err != nil {
		return devbridge.Session{}, err
	}
	out, err := decodeDevBridge(row)
	if err != nil || out.Scope.AccountID != accountID {
		return devbridge.Session{}, ErrNotFound
	}
	return out, nil
}

func decodeDevBridge(row sqlc.DevBridgeByIDRow) (devbridge.Session, error) {
	out := devbridge.Session{ID: row.ID, ExpiresAt: row.ExpiresAt.Time}
	if row.RevokedAt.Valid {
		v := row.RevokedAt.Time
		out.RevokedAt = &v
	}
	if len(row.AttachmentDigest) != 32 || len(row.RequestDigest) != 32 {
		return out, ErrInvalidArgument
	}
	if err := json.Unmarshal(row.Scope, &out.Scope); err != nil {
		return out, err
	}
	copy(out.AttachmentDigest[:], row.AttachmentDigest)
	copy(out.RequestDigest[:], row.RequestDigest)
	return out, nil
}

func (s *PgStore) ListDevBridges(ctx context.Context, accountID string, limit int) ([]devbridge.Session, error) {
	account, err := parsePgUUID(accountID)
	if err != nil || limit < 1 || limit > api.DevBridgeInventoryLimit {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListDevBridges(ctx, s.pool, sqlc.ListDevBridgesParams{AccountID: account, RowLimit: int32(limit)})
	if err != nil {
		return nil, err
	}
	result := make([]devbridge.Session, 0, len(rows))
	for _, row := range rows {
		session, err := decodeDevBridge(sqlc.DevBridgeByIDRow(row))
		if err != nil || session.Scope.AccountID != accountID {
			return nil, ErrInvalidArgument
		}
		result = append(result, session)
	}
	return result, nil
}

func (s *PgStore) RevokeDevBridge(ctx context.Context, accountID, id string, now time.Time) error {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return ErrNotFound
	}
	affected, err := sqlc.New().RevokeDevBridge(ctx, s.pool, sqlc.RevokeDevBridgeParams{
		ID: id, AccountID: account, RevokedAt: pgtype.Timestamptz{Time: now.UTC(), Valid: true},
	})
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrNotFound
	}
	return nil
}

func cloneDevBridge(s devbridge.Session) devbridge.Session {
	s.Scope.DependencyAppIDs = append([]string(nil), s.Scope.DependencyAppIDs...)
	if s.RevokedAt != nil {
		v := *s.RevokedAt
		s.RevokedAt = &v
	}
	return s
}

func (m *MemStore) CreateDevBridge(_ context.Context, s devbridge.Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[s.Scope.TargetAppID]
	if !ok || app.AccountID != s.Scope.AccountID || app.Status != AppActive {
		return ErrNotFound
	}
	env, exists := m.projectEnvironments[s.Scope.EnvironmentID]
	if !exists || env.AccountID != app.AccountID || env.ProjectID != app.ProjectID || s.Scope.ProjectID != env.ProjectID || env.Protected || env.Slug == "production" || env.Slug == "default" {
		return ErrNotFound
	}
	active := 0
	cutoff := time.Now().Add(-api.DevBridgeMetadataRetention)
	for _, existing := range m.devBridgeSessions {
		if existing.Scope.AccountID == s.Scope.AccountID && existing.ExpiresAt.Before(cutoff) {
			delete(m.devBridgeSessions, existing.ID)
			for id, receipt := range m.devBridgeWebhookReplays {
				if receipt.SessionID == existing.ID {
					delete(m.devBridgeWebhookReplays, id)
				}
			}
			continue
		}
		if existing.Scope.AccountID == s.Scope.AccountID && existing.RevokedAt == nil && time.Now().Before(existing.ExpiresAt) {
			active++
		}
	}
	if active >= api.MustLimitsFor(m.accounts[app.AccountID].Plan).DeveloperApps {
		return ErrConflict
	}
	if m.devBridgeSessions == nil {
		m.devBridgeSessions = make(map[string]devbridge.Session)
	}
	if _, exists := m.devBridgeSessions[s.ID]; exists {
		return ErrConflict
	}
	m.devBridgeSessions[s.ID] = cloneDevBridge(s)
	return nil
}

func (m *MemStore) DevBridgeByID(_ context.Context, accountID, id string) (devbridge.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.devBridgeSessions[id]
	if !ok || s.Scope.AccountID != accountID {
		return devbridge.Session{}, ErrNotFound
	}
	return cloneDevBridge(s), nil
}

func (m *MemStore) ListDevBridges(_ context.Context, accountID string, limit int) ([]devbridge.Session, error) {
	if limit < 1 || limit > api.DevBridgeInventoryLimit {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]devbridge.Session, 0)
	for _, session := range m.devBridgeSessions {
		if session.Scope.AccountID == accountID && session.RevokedAt == nil && time.Now().Before(session.ExpiresAt) {
			result = append(result, cloneDevBridge(session))
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ExpiresAt.Equal(result[j].ExpiresAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].ExpiresAt.After(result[j].ExpiresAt)
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (m *MemStore) RevokeDevBridge(_ context.Context, accountID, id string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.devBridgeSessions[id]
	if !ok || s.Scope.AccountID != accountID {
		return ErrNotFound
	}
	if s.RevokedAt == nil {
		v := now.UTC()
		s.RevokedAt = &v
	}
	m.devBridgeSessions[id] = s
	return nil
}
