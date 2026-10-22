package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func decodeCutoverRecord(raw []byte, version int64) (MigrationCutoverRecord, error) {
	var r MigrationCutoverRecord
	err := json.Unmarshal(raw, &r)
	r.Version = version
	return r, err
}
func (s *PgStore) GetMigrationCutover(ctx context.Context, account, app, id string) (MigrationCutoverRecord, error) {
	row, err := sqlc.New().GetMigrationCutover(ctx, s.pool, sqlc.GetMigrationCutoverParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), ID: mustPgUUID(id)})
	if err != nil {
		return MigrationCutoverRecord{}, mapErr(err)
	}
	return decodeCutoverRecord(row.Snapshot, row.Version)
}
func (s *PgStore) ListMigrationCutovers(ctx context.Context, account, app string) ([]MigrationCutoverRecord, error) {
	rows, err := sqlc.New().ListMigrationCutovers(ctx, s.pool, sqlc.ListMigrationCutoversParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), Limit: api.MigrationCutoverListLimit})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]MigrationCutoverRecord, 0, len(rows))
	for _, row := range rows {
		r, err := decodeCutoverRecord(row.Snapshot, row.Version)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
func cutoverDueTimestamp(r MigrationCutoverRecord) pgtype.Timestamptz {
	due := migrationCutoverDue(r)
	return pgtype.Timestamptz{Time: due, Valid: !due.IsZero()}
}
func (s *PgStore) CreateMigrationCutover(ctx context.Context, r MigrationCutoverRecord) (MigrationCutoverRecord, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return r, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.LockMigrationVerificationApp(ctx, tx, sqlc.LockMigrationVerificationAppParams{AccountID: mustPgUUID(r.AccountID), ID: mustPgUUID(r.AppID)}); err != nil {
		return r, mapErr(err)
	}
	old, err := q.GetMigrationCutover(ctx, tx, sqlc.GetMigrationCutoverParams{AccountID: mustPgUUID(r.AccountID), AppID: mustPgUUID(r.AppID), ID: mustPgUUID(r.Report.ID)})
	if err == nil {
		existing, err := decodeCutoverRecord(old.Snapshot, old.Version)
		if err != nil {
			return r, err
		}
		if !reflect.DeepEqual(existing.Report.Request, r.Report.Request) {
			return r, ErrConflict
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return r, mapErr(err)
	}
	if err = q.PruneMigrationCutovers(ctx, tx, sqlc.PruneMigrationCutoversParams{AppID: mustPgUUID(r.AppID), CreatedAt: NewPgtypeTime(time.Now().Add(-api.MigrationCutoverRetention))}); err != nil {
		return r, err
	}
	count, err := q.CountMigrationCutovers(ctx, tx, mustPgUUID(r.AppID))
	if err != nil {
		return r, err
	}
	if count >= api.MigrationCutoverListLimit {
		return r, ErrConflict
	}
	r.Version = 1
	raw, err := json.Marshal(r)
	if err != nil {
		return r, err
	}
	err = q.CreateMigrationCutover(ctx, tx, sqlc.CreateMigrationCutoverParams{ID: mustPgUUID(r.Report.ID), AccountID: mustPgUUID(r.AccountID), AppID: mustPgUUID(r.AppID), CreatedAt: NewPgtypeTime(r.Report.CreatedAt), Version: r.Version, Snapshot: raw, NextAttemptAt: cutoverDueTimestamp(r)})
	if err != nil {
		return r, mapErr(err)
	}
	return r, tx.Commit(ctx)
}
func (s *PgStore) SaveMigrationCutover(ctx context.Context, r MigrationCutoverRecord, version int64) (MigrationCutoverRecord, error) {
	r.Version = version + 1
	raw, err := json.Marshal(r)
	if err != nil {
		return r, err
	}
	n, err := sqlc.New().SaveMigrationCutover(ctx, s.pool, sqlc.SaveMigrationCutoverParams{AccountID: mustPgUUID(r.AccountID), AppID: mustPgUUID(r.AppID), ID: mustPgUUID(r.Report.ID), Snapshot: raw, Version: version, NextAttemptAt: cutoverDueTimestamp(r)})
	if err != nil {
		return r, mapErr(err)
	}
	if n != 1 {
		return r, ErrConflict
	}
	return r, nil
}
func (s *PgStore) ListDueMigrationCutovers(ctx context.Context, now time.Time, limit int32) ([]MigrationCutoverRecord, error) {
	if limit <= 0 || limit > api.MigrationCutoverWorkerBatch {
		limit = api.MigrationCutoverWorkerBatch
	}
	rows, err := sqlc.New().ListDueMigrationCutovers(ctx, s.pool, sqlc.ListDueMigrationCutoversParams{NextAttemptAt: NewPgtypeTime(now), Limit: limit})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]MigrationCutoverRecord, 0, len(rows))
	for _, row := range rows {
		r, err := decodeCutoverRecord(row.Snapshot, row.Version)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
