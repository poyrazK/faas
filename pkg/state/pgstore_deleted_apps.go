package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ AccountDeletedAppsStore = (*PgStore)(nil)

func deletedAppTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	at := value.Time
	return &at
}

func (s *PgStore) ListAccountDeletedApps(ctx context.Context, accountID string, beforeAt time.Time, beforeID string, limit int) ([]DeletedAppRecord, error) {
	if !validDeletedAppPage(accountID, beforeAt, beforeID, limit) {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListAccountDeletedApps(ctx, s.pool, sqlc.ListAccountDeletedAppsParams{
		AccountID: accountID, BeforeID: beforeID, BeforeAt: pgtype.Timestamptz{Time: beforeAt, Valid: !beforeAt.IsZero()}, PageLimit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list account deleted apps: %w", err)
	}
	out := make([]DeletedAppRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, DeletedAppRecord{ID: r.ID, Slug: r.Slug, DeletedAt: deletedAppTime(r.DeletedAt), DeleteGraceUntil: deletedAppTime(r.DeleteGraceUntil), SortAt: r.SortAt.Time, PurgeStarted: r.PurgeStarted})
	}
	return out, nil
}

func (s *PgStore) AccountDeletedAppByID(ctx context.Context, accountID, id string) (DeletedAppRecord, error) {
	r, err := sqlc.New().AccountDeletedAppByID(ctx, s.pool, sqlc.AccountDeletedAppByIDParams{AccountID: accountID, AppID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return DeletedAppRecord{}, ErrNotFound
	}
	if err != nil {
		return DeletedAppRecord{}, fmt.Errorf("read account deleted app: %w", err)
	}
	return DeletedAppRecord{ID: r.ID, Slug: r.Slug, DeletedAt: deletedAppTime(r.DeletedAt), DeleteGraceUntil: deletedAppTime(r.DeleteGraceUntil), SortAt: r.SortAt.Time, PurgeStarted: r.PurgeStarted}, nil
}
