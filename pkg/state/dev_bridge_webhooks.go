package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/devbridge"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type DevBridgeWebhookStore interface {
	ReserveDevBridgeWebhookReplay(context.Context, devbridge.WebhookReplay) (devbridge.WebhookReplay, bool, error)
	FinishDevBridgeWebhookReplay(context.Context, string, string, string, int) error
	DevBridgeWebhookReplayByID(context.Context, string, string, string) (devbridge.WebhookReplay, error)
}

func (s *PgStore) DevBridgeWebhookReplayByID(ctx context.Context, accountID, sessionID, id string) (devbridge.WebhookReplay, error) {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return devbridge.WebhookReplay{}, ErrNotFound
	}
	replay, err := parsePgUUID(id)
	if err != nil {
		return devbridge.WebhookReplay{}, ErrNotFound
	}
	row, err := sqlc.New().DevBridgeWebhookReplayByID(ctx, s.pool, sqlc.DevBridgeWebhookReplayByIDParams{ID: replay, AccountID: account, SessionID: sessionID})
	if errors.Is(err, pgx.ErrNoRows) {
		return devbridge.WebhookReplay{}, ErrNotFound
	}
	if err != nil {
		return devbridge.WebhookReplay{}, err
	}
	out := bridgeReplayFromRow(row)
	return out, nil
}

func bridgeReplayFromRow(row sqlc.DevBridgeWebhookReplay) devbridge.WebhookReplay {
	out := devbridge.WebhookReplay{ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID), SessionID: row.SessionID, InvocationID: pgUUIDString(row.InvocationID), IdempotencyKey: row.IdempotencyKey, State: row.State, HTTPStatus: int(row.HttpStatus), CreatedAt: row.CreatedAt.Time}
	if row.CompletedAt.Valid {
		value := row.CompletedAt.Time
		out.CompletedAt = &value
	}
	return out
}

func (s *PgStore) ReserveDevBridgeWebhookReplay(ctx context.Context, in devbridge.WebhookReplay) (devbridge.WebhookReplay, bool, error) {
	account, err := parsePgUUID(in.AccountID)
	if err != nil {
		return in, false, ErrInvalidArgument
	}
	id, err := parsePgUUID(in.ID)
	if err != nil {
		return in, false, ErrInvalidArgument
	}
	invocation, err := parsePgUUID(in.InvocationID)
	if err != nil {
		return in, false, ErrInvalidArgument
	}
	if len(in.IdempotencyKey) < 1 || len(in.IdempotencyKey) > api.DevBridgeReplayKeyBytes {
		return in, false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return in, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.LockDevBridgeReplaySession(ctx, tx, sqlc.LockDevBridgeReplaySessionParams{ID: in.SessionID, AccountID: account}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrNotFound
		}
		return in, false, err
	}
	n, err := q.CreateDevBridgeWebhookReplay(ctx, tx, sqlc.CreateDevBridgeWebhookReplayParams{ID: id, SessionID: in.SessionID, AccountID: account, InvocationID: invocation, IdempotencyKey: in.IdempotencyKey, MaxReplays: api.DevBridgeMaxWebhookReplays})
	if err != nil {
		return in, false, err
	}
	row, err := q.DevBridgeWebhookReplayByKey(ctx, tx, sqlc.DevBridgeWebhookReplayByKeyParams{SessionID: in.SessionID, AccountID: account, IdempotencyKey: in.IdempotencyKey})
	if errors.Is(err, pgx.ErrNoRows) {
		return in, false, ErrConflict
	}
	if err != nil {
		return in, false, err
	}
	if row.InvocationID != invocation {
		return in, false, ErrConflict
	}
	out := bridgeReplayFromRow(row)
	return out, n == 1, tx.Commit(ctx)
}

func (s *PgStore) FinishDevBridgeWebhookReplay(ctx context.Context, accountID, id, status string, code int) error {
	if !validBridgeReplayOutcome(status, code) {
		return ErrInvalidArgument
	}
	account, err := parsePgUUID(accountID)
	if err != nil {
		return ErrNotFound
	}
	replay, err := parsePgUUID(id)
	if err != nil {
		return ErrNotFound
	}
	n, err := sqlc.New().FinishDevBridgeWebhookReplay(ctx, s.pool, sqlc.FinishDevBridgeWebhookReplayParams{ID: replay, AccountID: account, State: status, HttpStatus: int32(code)})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotFound
	}
	return nil
}

func validBridgeReplayOutcome(status string, code int) bool {
	return (status == "completed" && code >= 100 && code <= 599) || (status == "uncertain" && code == 0)
}

func (m *MemStore) ReserveDevBridgeWebhookReplay(_ context.Context, in devbridge.WebhookReplay) (devbridge.WebhookReplay, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.devBridgeSessions[in.SessionID]
	if !ok || session.Scope.AccountID != in.AccountID || session.RevokedAt != nil || !time.Now().Before(session.ExpiresAt) {
		return in, false, ErrNotFound
	}
	if len(in.IdempotencyKey) < 1 || len(in.IdempotencyKey) > api.DevBridgeReplayKeyBytes {
		return in, false, ErrInvalidArgument
	}
	key := in.SessionID + "\x00" + in.IdempotencyKey
	if existing, ok := m.devBridgeWebhookReplays[key]; ok {
		if existing.InvocationID != in.InvocationID {
			return in, false, ErrConflict
		}
		return cloneBridgeReplay(existing), false, nil
	}
	count := 0
	for _, existing := range m.devBridgeWebhookReplays {
		if existing.SessionID == in.SessionID {
			count++
		}
		if existing.ID == in.ID {
			return in, false, ErrConflict
		}
	}
	if count >= api.DevBridgeMaxWebhookReplays {
		return in, false, ErrConflict
	}
	if m.devBridgeWebhookReplays == nil {
		m.devBridgeWebhookReplays = make(map[string]devbridge.WebhookReplay)
	}
	in.State, in.HTTPStatus, in.CreatedAt, in.CompletedAt = "dispatching", 0, time.Now().UTC(), nil
	m.devBridgeWebhookReplays[key] = in
	return in, true, nil
}

func (m *MemStore) FinishDevBridgeWebhookReplay(_ context.Context, account, id, status string, code int) error {
	if !validBridgeReplayOutcome(status, code) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, existing := range m.devBridgeWebhookReplays {
		if existing.ID == id && existing.AccountID == account && existing.State == "dispatching" {
			now := time.Now().UTC()
			existing.State, existing.HTTPStatus, existing.CompletedAt = status, code, &now
			m.devBridgeWebhookReplays[key] = existing
			return nil
		}
	}
	return ErrNotFound
}

func cloneBridgeReplay(in devbridge.WebhookReplay) devbridge.WebhookReplay {
	if in.CompletedAt != nil {
		value := *in.CompletedAt
		in.CompletedAt = &value
	}
	return in
}

func (m *MemStore) DevBridgeWebhookReplayByID(_ context.Context, account, session, id string) (devbridge.WebhookReplay, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, replay := range m.devBridgeWebhookReplays {
		if replay.AccountID == account && replay.SessionID == session && replay.ID == id {
			return cloneBridgeReplay(replay), nil
		}
	}
	return devbridge.WebhookReplay{}, ErrNotFound
}
