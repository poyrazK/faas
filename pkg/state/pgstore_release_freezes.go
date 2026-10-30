package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ReleaseFreezeStore = (*PgStore)(nil)

func optionalPgInt2(v int, set bool) pgtype.Int2 {
	return pgtype.Int2{Int16: int16(v), Valid: set}
}

func optionalPgInt4(v int, set bool) pgtype.Int4 {
	return pgtype.Int4{Int32: int32(v), Valid: set}
}

func optionalPgText(v string) pgtype.Text {
	return pgtype.Text{String: v, Valid: v != ""}
}

func releaseFreezeFromRow(r sqlc.ReleaseFreeze) ReleaseFreeze {
	return ReleaseFreeze{
		ID: pgUUIDString(r.ID), AccountID: pgUUIDStringNullable(r.AccountID), OrgID: pgUUIDStringNullable(r.OrgID),
		AppID: pgUUIDStringNullable(r.AppID), Kind: r.Kind, StartsAt: optionalTimestamptz(r.StartsAt), EndsAt: optionalTimestamptz(r.EndsAt),
		WeeklyDays: int(r.WeeklyDays.Int16), WeeklyStartMinute: int(r.WeeklyStartMinute.Int16),
		WeeklyDurationMinutes: int(r.WeeklyDurationMinutes.Int32), TimeZone: r.TimeZone.String,
		AfterRollbackMinutes: int(r.AfterRollbackMinutes.Int32), Reason: r.Reason, Source: r.Source,
		SourceDeploymentID: pgUUIDStringNullable(r.SourceDeploymentID), CreatedBy: r.CreatedBy,
		CreatedAt: r.CreatedAt.Time.UTC(), LiftedAt: optionalTimestamptz(r.LiftedAt), LiftedBy: r.LiftedBy.String,
	}
}

func releaseFreezesFromRows(rows []sqlc.ReleaseFreeze) []ReleaseFreeze {
	out := make([]ReleaseFreeze, 0, len(rows))
	for _, r := range rows {
		out = append(out, releaseFreezeFromRow(r))
	}
	return out
}

func (s *PgStore) CreateReleaseFreeze(ctx context.Context, f ReleaseFreeze, maxUnlifted int, now time.Time) (ReleaseFreeze, error) {
	if !f.Owner().Valid() {
		return ReleaseFreeze{}, ErrInvalidArgument
	}
	if f.ID == "" {
		f.ID = uuid.NewString()
	}
	weekly := f.Kind == ReleaseFreezeWeekly
	params := sqlc.CreateReleaseFreezeWithinLimitParams{
		ID: mustPgUUID(f.ID), AccountID: mustPgUUID(f.AccountID), OrgID: mustPgUUID(f.OrgID), AppID: mustPgUUID(f.AppID), Kind: f.Kind,
		StartsAt: nullableTimestamptzPtr(f.StartsAt), EndsAt: nullableTimestamptzPtr(f.EndsAt),
		WeeklyDays: optionalPgInt2(f.WeeklyDays, weekly), WeeklyStartMinute: optionalPgInt2(f.WeeklyStartMinute, weekly),
		WeeklyDurationMinutes: optionalPgInt4(f.WeeklyDurationMinutes, weekly), TimeZone: optionalPgText(f.TimeZone),
		AfterRollbackMinutes: optionalPgInt4(f.AfterRollbackMinutes, f.Kind == ReleaseFreezeAfterRollback),
		Reason:               f.Reason, CreatedBy: f.CreatedBy, Now: pgTimestamptzUTC(now), MaxUnlifted: int64(maxUnlifted),
	}
	row, err := sqlc.New().CreateReleaseFreezeWithinLimit(ctx, s.pool, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReleaseFreeze{}, ErrReleaseFreezeLimit
	}
	if err != nil {
		return ReleaseFreeze{}, mapErr(err)
	}
	return releaseFreezeFromRow(row), nil
}

func (s *PgStore) CreateRollbackReleaseFreeze(ctx context.Context, f ReleaseFreeze) (bool, error) {
	if f.ID == "" {
		f.ID = uuid.NewString()
	}
	if !f.Owner().Valid() || f.StartsAt == nil || f.EndsAt == nil {
		return false, ErrInvalidArgument
	}
	n, err := sqlc.New().CreateRollbackReleaseFreeze(ctx, s.pool, sqlc.CreateRollbackReleaseFreezeParams{
		ID: mustPgUUID(f.ID), AccountID: mustPgUUID(f.AccountID), OrgID: mustPgUUID(f.OrgID), AppID: mustPgUUID(f.AppID),
		StartsAt: pgTimestamptzUTC(*f.StartsAt), EndsAt: pgTimestamptzUTC(*f.EndsAt), Reason: f.Reason,
		SourceDeploymentID: mustPgUUID(f.SourceDeploymentID), CreatedBy: f.CreatedBy,
	})
	if err != nil {
		return false, mapErr(err)
	}
	return n == 1, nil
}

func (s *PgStore) ListReleaseFreezes(ctx context.Context, owner ReleaseFreezeOwner, now time.Time) ([]ReleaseFreeze, error) {
	if !owner.Valid() {
		return nil, ErrInvalidArgument
	}
	q := sqlc.New()
	var rows []sqlc.ReleaseFreeze
	var err error
	if owner.OrgID != "" {
		rows, err = q.ListUnliftedOrgReleaseFreezes(ctx, s.pool, sqlc.ListUnliftedOrgReleaseFreezesParams{
			OrgID: mustPgUUID(owner.OrgID), Now: pgTimestamptzUTC(now),
		})
	} else {
		rows, err = q.ListUnliftedReleaseFreezes(ctx, s.pool, sqlc.ListUnliftedReleaseFreezesParams{
			AccountID: mustPgUUID(owner.AccountID), Now: pgTimestamptzUTC(now),
		})
	}
	if err != nil {
		return nil, mapErr(err)
	}
	return releaseFreezesFromRows(rows), nil
}

func (s *PgStore) LiftReleaseFreeze(ctx context.Context, owner ReleaseFreezeOwner, id, liftedBy string, now time.Time) (ReleaseFreeze, error) {
	if _, err := uuid.Parse(id); err != nil || !owner.Valid() {
		return ReleaseFreeze{}, ErrNotFound
	}
	q := sqlc.New()
	var row sqlc.ReleaseFreeze
	var err error
	if owner.OrgID != "" {
		row, err = q.LiftOrgReleaseFreeze(ctx, s.pool, sqlc.LiftOrgReleaseFreezeParams{
			OrgID: mustPgUUID(owner.OrgID), ID: mustPgUUID(id), LiftedBy: liftedBy, Now: pgTimestamptzUTC(now),
		})
	} else {
		row, err = q.LiftReleaseFreeze(ctx, s.pool, sqlc.LiftReleaseFreezeParams{
			AccountID: mustPgUUID(owner.AccountID), ID: mustPgUUID(id), LiftedBy: liftedBy, Now: pgTimestamptzUTC(now),
		})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ReleaseFreeze{}, ErrNotFound
	}
	if err != nil {
		return ReleaseFreeze{}, mapErr(err)
	}
	return releaseFreezeFromRow(row), nil
}
