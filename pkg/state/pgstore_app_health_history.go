package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ AppHealthHistoryStore = (*PgStore)(nil)

func (s *PgStore) PruneExpiredAppHealthHistory(ctx context.Context, now time.Time) (int64, error) {
	n, err := sqlc.New().PruneExpiredAppHealthHistory(ctx, s.pool, sqlc.PruneExpiredAppHealthHistoryParams{OldestAt: NewPgtypeTime(now.Add(-api.AppHealthHistoryMaxAge)), BatchLimit: api.AppHealthHistoryPruneBatch})
	if err != nil {
		return 0, fmt.Errorf("prune expired application health history: %w", err)
	}
	return n, nil
}

func (s *PgStore) ClaimAppHealth(ctx context.Context, token string, now time.Time) (AppHealthClaim, error) {
	if token == "" || now.IsZero() {
		return AppHealthClaim{}, ErrInvalidArgument
	}
	row, err := sqlc.New().ClaimAppHealth(ctx, s.pool, sqlc.ClaimAppHealthParams{Token: token, CheckedNow: NewPgtypeTime(now), ExpiresAt: NewPgtypeTime(now.Add(api.AppHealthCollectorLease))})
	if errors.Is(err, pgx.ErrNoRows) {
		return AppHealthClaim{}, ErrNotFound
	}
	if err != nil {
		return AppHealthClaim{}, fmt.Errorf("claim application health: %w", err)
	}
	return AppHealthClaim{AppID: row.AppID, AccountID: row.AccountID, Token: row.LeaseToken.String, StartedAt: row.LeaseStartedAt.Time, LeaseUntil: row.LeaseUntil.Time}, nil
}

func (s *PgStore) FinishAppHealth(ctx context.Context, claim AppHealthClaim, a api.AppHealthResponse, now time.Time) error {
	key, body, err := prepareAppHealth(claim, a, now)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin app health observation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.LockAppHealthCollection(ctx, tx, sqlc.LockAppHealthCollectionParams{AppID: claim.AppID, AccountID: claim.AccountID, Token: claim.Token, StartedAt: NewPgtypeTime(claim.StartedAt), CheckedNow: NewPgtypeTime(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("lock app health observation: %w", err)
	}
	at, _ := time.Parse(time.RFC3339Nano, a.EvaluatedAt)
	if row.CheckedAt.Valid && !at.After(row.CheckedAt.Time) {
		return ErrConflict
	}
	previous, err := decodeAppHealth(row.Assessment)
	if err != nil {
		return err
	}
	entries := appHealthEntries(previous, row.AssessmentKey.String, key, a)
	notification, err := pgPrepareAppHealthNotification(ctx, tx, claim, row.NotificationState, previous, a, entries, now)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := insertAppHealthEntry(ctx, tx, claim, e); err != nil {
			return err
		}
	}
	if err := q.FinishAppHealthCollection(ctx, tx, sqlc.FinishAppHealthCollectionParams{AppID: claim.AppID, Assessment: body, AssessmentKey: key, NotificationState: notification.StateJSON, CheckedAt: NewPgtypeTime(at), NextCheckAt: NewPgtypeTime(now.Add(api.AppHealthCollectorInterval))}); err != nil {
		return fmt.Errorf("finish app health observation: %w", err)
	}
	if err := pgEnqueueAppHealthNotification(ctx, tx, claim, notification); err != nil {
		return err
	}
	if err := q.PruneAppHealthHistory(ctx, tx, sqlc.PruneAppHealthHistoryParams{AppID: claim.AppID, MaxEntries: api.AppHealthHistoryMaxEntries, MaxBytes: api.AppHealthHistoryMaxBytes, OldestAt: NewPgtypeTime(now.Add(-api.AppHealthHistoryMaxAge))}); err != nil {
		return fmt.Errorf("prune app health history: %w", err)
	}
	return tx.Commit(ctx)
}

func insertAppHealthEntry(ctx context.Context, tx pgx.Tx, claim AppHealthClaim, e api.AppHealthHistoryEntry) error {
	body, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encode app health history: %w", err)
	}
	if len(body) > api.AppHealthHistoryEntryMaxBytes {
		return ErrInvalidArgument
	}
	at, _ := time.Parse(time.RFC3339Nano, e.ObservedAt)
	if err := sqlc.New().InsertAppHealthHistory(ctx, tx, sqlc.InsertAppHealthHistoryParams{ID: e.ID, AppID: claim.AppID, AccountID: claim.AccountID, Kind: e.Kind, ObservedAt: NewPgtypeTime(at), EncodedBytes: int32(len(body)), Entry: body}); err != nil {
		return fmt.Errorf("insert app health history: %w", err)
	}
	return nil
}

func decodeAppHealth(body []byte) (*api.AppHealthResponse, error) {
	if len(body) == 0 {
		return nil, nil
	}
	var a api.AppHealthResponse
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, fmt.Errorf("decode saved app health: %w", err)
	}
	return &a, nil
}

func (s *PgStore) ListAppHealthHistory(ctx context.Context, accountID, appID string, limit int, before string, now time.Time) (api.AppHealthHistoryPage, error) {
	if err := validateAppHealthPage(limit, before); err != nil {
		return api.AppHealthHistoryPage{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.AppHealthHistoryPage{}, fmt.Errorf("begin app health history read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.AppHealthHistoryTarget(ctx, tx, sqlc.AppHealthHistoryTargetParams{AppID: appID, AccountID: accountID}); err != nil {
		return api.AppHealthHistoryPage{}, routePolicyReadError(err)
	}
	body, err := q.ReadAppHealthCollection(ctx, tx, sqlc.ReadAppHealthCollectionParams{AppID: appID, AccountID: accountID})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return api.AppHealthHistoryPage{}, fmt.Errorf("read latest collected app health: %w", err)
	}
	latest, err := decodeAppHealth(body)
	if err != nil {
		return api.AppHealthHistoryPage{}, err
	}
	page := appHealthPage(appID, latest, now)
	params := sqlc.ListAppHealthHistoryParams{AppID: appID, AccountID: accountID, OldestAt: NewPgtypeTime(now.Add(-api.AppHealthHistoryMaxAge)), BeforeID: before, BeforeAt: NewPgtypeTime(now), PageLimit: int32(limit + 1)}
	if before != "" {
		cursor, err := q.ReadAppHealthHistoryCursor(ctx, tx, sqlc.ReadAppHealthHistoryCursorParams{AppID: appID, AccountID: accountID, ID: before, OldestAt: params.OldestAt})
		if err != nil {
			return page, routePolicyReadError(err)
		}
		params.BeforeAt = cursor.ObservedAt
	}
	rows, err := q.ListAppHealthHistory(ctx, tx, params)
	if err != nil {
		return page, fmt.Errorf("list app health history: %w", err)
	}
	for i, body := range rows {
		if i == limit {
			page.NextCursor = page.Entries[i-1].ID
			break
		}
		var e api.AppHealthHistoryEntry
		if err := json.Unmarshal(body, &e); err != nil {
			return page, fmt.Errorf("decode app health history entry: %w", err)
		}
		page.Entries = append(page.Entries, e)
	}
	return page, tx.Commit(ctx)
}
