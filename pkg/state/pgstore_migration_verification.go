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

func decodeMigrationRecord(raw []byte, version int64) (MigrationVerificationRecord, error) {
	var r MigrationVerificationRecord
	err := json.Unmarshal(raw, &r)
	r.Version = version
	return r, err
}
func (s *PgStore) GetMigrationVerification(ctx context.Context, account, app, id string) (MigrationVerificationRecord, error) {
	row, err := sqlc.New().GetMigrationVerification(ctx, s.pool, sqlc.GetMigrationVerificationParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), ID: mustPgUUID(id)})
	if err != nil {
		return MigrationVerificationRecord{}, mapErr(err)
	}
	return decodeMigrationRecord(row.Snapshot, row.Version)
}
func (s *PgStore) ListMigrationVerifications(ctx context.Context, account, app string) ([]MigrationVerificationRecord, error) {
	rows, err := sqlc.New().ListMigrationVerifications(ctx, s.pool, sqlc.ListMigrationVerificationsParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), Limit: api.MigrationVerificationListLimit})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]MigrationVerificationRecord, 0, len(rows))
	for _, row := range rows {
		r, err := decodeMigrationRecord(row.Snapshot, row.Version)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *PgStore) ListDueMigrationVerifications(ctx context.Context, now time.Time, limit int32) ([]MigrationVerificationRecord, error) {
	if limit <= 0 || limit > api.MigrationVerificationWorkerBatch {
		limit = api.MigrationVerificationWorkerBatch
	}
	rows, err := sqlc.New().ListDueMigrationVerifications(ctx, s.pool, sqlc.ListDueMigrationVerificationsParams{NextAttemptAt: pgtype.Timestamptz{Time: now, Valid: true}, Limit: limit})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]MigrationVerificationRecord, 0, len(rows))
	for _, row := range rows {
		r, err := decodeMigrationRecord(row.Snapshot, row.Version)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func migrationVerificationDueTimestamp(r MigrationVerificationRecord) pgtype.Timestamptz {
	due := migrationVerificationDue(r)
	return pgtype.Timestamptz{Time: due, Valid: !due.IsZero()}
}
func (s *PgStore) CreateMigrationVerification(ctx context.Context, r MigrationVerificationRecord) (MigrationVerificationRecord, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return r, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.LockMigrationVerificationApp(ctx, tx, sqlc.LockMigrationVerificationAppParams{AccountID: mustPgUUID(r.AccountID), ID: mustPgUUID(r.AppID)}); err != nil {
		return r, mapErr(err)
	}
	old, err := q.GetMigrationVerification(ctx, tx, sqlc.GetMigrationVerificationParams{AccountID: mustPgUUID(r.AccountID), AppID: mustPgUUID(r.AppID), ID: mustPgUUID(r.Report.ID)})
	if err == nil {
		existing, err := decodeMigrationRecord(old.Snapshot, old.Version)
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
	if err = q.PruneMigrationVerifications(ctx, tx, sqlc.PruneMigrationVerificationsParams{AppID: mustPgUUID(r.AppID), CreatedAt: pgtype.Timestamptz{Time: time.Now().Add(-api.MigrationVerificationRetention), Valid: true}}); err != nil {
		return r, err
	}
	n, err := q.CountActiveMigrationVerifications(ctx, tx, mustPgUUID(r.AppID))
	if err != nil {
		return r, err
	}
	if n >= api.MigrationVerificationMaxActive {
		return r, ErrConflict
	}
	r.Version = 1
	raw, err := json.Marshal(r)
	if err != nil {
		return r, err
	}
	err = q.CreateMigrationVerification(ctx, tx, sqlc.CreateMigrationVerificationParams{ID: mustPgUUID(r.Report.ID), AccountID: mustPgUUID(r.AccountID), AppID: mustPgUUID(r.AppID), CreatedAt: pgtype.Timestamptz{Time: r.Report.CreatedAt, Valid: true}, Version: r.Version, Snapshot: raw, NextAttemptAt: migrationVerificationDueTimestamp(r)})
	if err != nil {
		return r, mapErr(err)
	}
	return r, tx.Commit(ctx)
}
func (s *PgStore) SaveMigrationVerification(ctx context.Context, r MigrationVerificationRecord, version int64, task *CreateAppTaskParams) (MigrationVerificationRecord, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return r, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	old, err := q.LockMigrationVerification(ctx, tx, sqlc.LockMigrationVerificationParams{AccountID: mustPgUUID(r.AccountID), AppID: mustPgUUID(r.AppID), ID: mustPgUUID(r.Report.ID)})
	if err != nil {
		return r, mapErr(err)
	}
	if old.Version != version {
		return r, ErrConflict
	}
	if task != nil && *migrationTaskSlot(&r) == "" {
		resolved, err := resolveCreateAppTask(*task)
		if err != nil {
			return r, err
		}
		row, err := q.CreateServiceBindingSmokeTask(ctx, tx, sqlc.CreateServiceBindingSmokeTaskParams{AccountID: mustPgUUID(resolved.AccountID), AppID: mustPgUUID(resolved.AppID), DeploymentID: mustPgUUID(resolved.DeploymentID), Command: resolved.Command, TimeoutSeconds: int32(resolved.TimeoutSeconds), MaxOutputBytes: int32(resolved.MaxOutputBytes), CreatedAt: pgtype.Timestamptz{Time: resolved.CreatedAt, Valid: true}})
		if err != nil {
			return r, mapErr(err)
		}
		*migrationTaskSlot(&r) = uuidString(row.ID)
		if err = q.SetVerificationRunTaskDeadline(ctx, tx, sqlc.SetVerificationRunTaskDeadlineParams{ID: row.ID, ExpiresAt: pgtype.Timestamptz{Time: r.Report.ExpiresAt, Valid: true}}); err != nil {
			return r, err
		}
	}
	r.Version = version + 1
	raw, err := json.Marshal(r)
	if err != nil {
		return r, err
	}
	n, err := q.SaveMigrationVerification(ctx, tx, sqlc.SaveMigrationVerificationParams{AccountID: mustPgUUID(r.AccountID), AppID: mustPgUUID(r.AppID), ID: mustPgUUID(r.Report.ID), Snapshot: raw, Version: version, NextAttemptAt: migrationVerificationDueTimestamp(r)})
	if err != nil {
		return r, mapErr(err)
	}
	if n != 1 {
		return r, ErrConflict
	}
	return r, tx.Commit(ctx)
}
