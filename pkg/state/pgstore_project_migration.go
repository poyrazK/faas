package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func decodeProjectMigration(raw []byte, version int64) (ProjectMigrationRecord, error) {
	var r ProjectMigrationRecord
	err := json.Unmarshal(raw, &r)
	r.Version = version
	return r, err
}
func (s *PgStore) GetProjectMigration(ctx context.Context, account, id string) (ProjectMigrationRecord, error) {
	row, err := sqlc.New().GetProjectMigration(ctx, s.pool, sqlc.GetProjectMigrationParams{AccountID: mustPgUUID(account), ID: mustPgUUID(id)})
	if err != nil {
		return ProjectMigrationRecord{}, mapErr(err)
	}
	return decodeProjectMigration(row.Snapshot, row.Version)
}
func (s *PgStore) ListProjectMigrations(ctx context.Context, account, project string) ([]ProjectMigrationRecord, error) {
	rows, err := sqlc.New().ListProjectMigrations(ctx, s.pool, sqlc.ListProjectMigrationsParams{AccountID: mustPgUUID(account), Column2: project, Limit: api.ProjectMigrationListLimit})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]ProjectMigrationRecord, 0, len(rows))
	for _, row := range rows {
		r, e := decodeProjectMigration(row.Snapshot, row.Version)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, nil
}
func (s *PgStore) CreateProjectMigration(ctx context.Context, r ProjectMigrationRecord) (ProjectMigrationRecord, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return r, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.LockProjectMigrationAccount(ctx, tx, mustPgUUID(r.AccountID)); err != nil {
		return r, mapErr(err)
	}
	old, err := q.GetProjectMigration(ctx, tx, sqlc.GetProjectMigrationParams{AccountID: mustPgUUID(r.AccountID), ID: mustPgUUID(r.Report.ID)})
	if err == nil {
		existing, e := decodeProjectMigration(old.Snapshot, old.Version)
		if e != nil {
			return r, e
		}
		if existing.RequestDigest != r.RequestDigest {
			return r, ErrConflict
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return r, mapErr(err)
	}
	if err = q.PruneProjectMigrations(ctx, tx, sqlc.PruneProjectMigrationsParams{AccountID: mustPgUUID(r.AccountID), CreatedAt: NewPgtypeTime(time.Now().Add(-api.ProjectMigrationRetention))}); err != nil {
		return r, err
	}
	count, err := q.CountProjectMigrations(ctx, tx, mustPgUUID(r.AccountID))
	if err != nil {
		return r, err
	}
	if count >= api.ProjectMigrationListLimit {
		return r, ErrConflict
	}
	r.Version = 1
	raw, err := json.Marshal(r)
	if err != nil {
		return r, err
	}
	err = q.CreateProjectMigration(ctx, tx, sqlc.CreateProjectMigrationParams{ID: mustPgUUID(r.Report.ID), AccountID: mustPgUUID(r.AccountID), ProjectSlug: r.Report.Project, CreatedAt: NewPgtypeTime(r.Report.CreatedAt), LeaseUntil: projectMigrationLease(r), Snapshot: raw})
	if err != nil {
		return r, mapErr(err)
	}
	return r, tx.Commit(ctx)
}
func projectMigrationLease(r ProjectMigrationRecord) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: r.LeaseUntil, Valid: !r.LeaseUntil.IsZero()}
}
func (s *PgStore) SaveProjectMigration(ctx context.Context, r ProjectMigrationRecord, version int64) (ProjectMigrationRecord, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return r, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.LockProjectMigrationAccount(ctx, tx, mustPgUUID(r.AccountID)); err != nil {
		return r, mapErr(err)
	}
	if r.LeaseUntil.After(time.Now()) {
		_, err = q.ActiveProjectMigrationLease(ctx, tx, sqlc.ActiveProjectMigrationLeaseParams{AccountID: mustPgUUID(r.AccountID), ProjectSlug: r.Report.Project, ID: mustPgUUID(r.Report.ID)})
		if err == nil {
			return r, ErrConflict
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return r, mapErr(err)
		}
	}
	r.Version = version + 1
	raw, err := json.Marshal(r)
	if err != nil {
		return r, err
	}
	n, err := q.SaveProjectMigration(ctx, tx, sqlc.SaveProjectMigrationParams{AccountID: mustPgUUID(r.AccountID), ID: mustPgUUID(r.Report.ID), Snapshot: raw, Version: version, LeaseUntil: projectMigrationLease(r)})
	if err != nil {
		return r, mapErr(err)
	}
	if n != 1 {
		return r, ErrConflict
	}
	return r, tx.Commit(ctx)
}
