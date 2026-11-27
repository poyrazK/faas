package postgresexport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// BackupDestination is an exact, authorized private bucket placement in the
// same resource ownership scope as the source database.
type BackupDestination struct {
	OrgID, BucketAccountID, BucketAppID, BucketID string
}

// BackupObjects delivers through Object Storage's ordinary write admission.
// Put verifies size and digest, and accepts an existing object only if its
// content matches. Callers publish the manifest after the archive succeeds.
type BackupObjects interface {
	CheckBackupDestination(context.Context, BackupDestination) error
	PutBackupObject(ctx context.Context, dest BackupDestination, key, mediaType, hash string, size int64, body io.Reader) error
}

type BackupSchedules struct {
	store   *db.IntentPool
	exports *Service
	objects BackupObjects
}

func NewBackupSchedules(pool *pgxpool.Pool, exports *Service, objects BackupObjects) *BackupSchedules {
	if pool == nil || exports == nil || objects == nil {
		return nil
	}
	return &BackupSchedules{store: db.NewIntentPool(pool), exports: exports, objects: objects}
}

func parseDailyTime(clock string) (int16, error) {
	parsed, err := time.Parse("15:04", clock)
	if err != nil || parsed.Format("15:04") != clock {
		return 0, managedpostgres.ErrInvalid
	}
	return int16(parsed.Hour()*60 + parsed.Minute()), nil
}

func nextDailyBackup(now time.Time, minute int16) time.Time {
	at := now.UTC().Truncate(24 * time.Hour).Add(time.Duration(minute) * time.Minute)
	if !at.After(now) {
		at = at.Add(24 * time.Hour)
	}
	return at
}

func scheduleUUID(value string) pgtype.UUID {
	id, err := uuid.Parse(value)
	return pgtype.UUID{Bytes: id, Valid: err == nil && id != uuid.Nil}
}

func scheduleView(row sqlc.NativePostgresBackupSchedule) api.ManagedPostgresBackupSchedule {
	out := api.ManagedPostgresBackupSchedule{
		DatabaseID: row.DatabaseID.String(), BucketAppID: row.BucketAppID.String(), BucketID: row.BucketID.String(),
		DailyAtUTC: fmt.Sprintf("%02d:%02d", row.UtcMinute/60, row.UtcMinute%60), NextRunAt: row.NextRunAt.Time,
		State: "scheduled", LastArchive: row.LastArchiveKey.String, LastError: row.LastErrorCode.String,
	}
	if row.RunID.Valid {
		out.State = "pending"
		out.ActiveRunID = row.RunID.String()
	}
	if row.LeaseUntil.Valid && row.LeaseUntil.Time.After(time.Now()) {
		out.State = "running"
	}
	if row.LastSuccessAt.Valid {
		out.LastSuccessAt = &row.LastSuccessAt.Time
	}
	return out
}

func (s *BackupSchedules) Get(ctx context.Context, owner, org, database string) (api.ManagedPostgresBackupSchedule, error) {
	row, err := sqlc.New().GetNativePostgresBackupSchedule(ctx, s.store, sqlc.GetNativePostgresBackupScheduleParams{DatabaseID: scheduleUUID(database), OwnerID: owner, OrgID: org})
	if errors.Is(err, pgx.ErrNoRows) {
		err = managedpostgres.ErrNotFound
	}
	return scheduleView(row), err
}

func (s *BackupSchedules) Set(ctx context.Context, owner, database, clock string, dest BackupDestination) (api.ManagedPostgresBackupSchedule, error) {
	minute, err := parseDailyTime(clock)
	if err != nil || !validID(database) || !validID(dest.BucketID) || !validID(dest.BucketAppID) || !validID(dest.BucketAccountID) {
		return api.ManagedPostgresBackupSchedule{}, managedpostgres.ErrInvalid
	}
	source, err := s.exports.source.ExportSource(ctx, owner, database)
	if err != nil {
		return api.ManagedPostgresBackupSchedule{}, err
	}
	if source.OrgID != dest.OrgID {
		return api.ManagedPostgresBackupSchedule{}, managedpostgres.ErrNotFound
	}
	if err := s.objects.CheckBackupDestination(ctx, dest); err != nil {
		return api.ManagedPostgresBackupSchedule{}, err
	}
	row, err := sqlc.New().SaveNativePostgresBackupSchedule(ctx, s.store, sqlc.SaveNativePostgresBackupScheduleParams{
		DatabaseID: scheduleUUID(database), OwnerID: owner, OrgID: source.OrgID, BucketID: scheduleUUID(dest.BucketID),
		BucketAccountID: scheduleUUID(dest.BucketAccountID), BucketAppID: scheduleUUID(dest.BucketAppID),
		UtcMinute: minute, NextRunAt: pgtype.Timestamptz{Time: nextDailyBackup(time.Now(), minute), Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		current, readErr := s.Get(ctx, owner, source.OrgID, database)
		if readErr == nil && current.DailyAtUTC == clock && current.BucketID == dest.BucketID && current.BucketAppID == dest.BucketAppID {
			return current, nil
		}
		return api.ManagedPostgresBackupSchedule{}, managedpostgres.ErrConflict
	}
	return scheduleView(row), err
}

func (s *BackupSchedules) Delete(ctx context.Context, owner, org, database string) error {
	removed, err := sqlc.New().DeleteNativePostgresBackupSchedule(ctx, s.store, sqlc.DeleteNativePostgresBackupScheduleParams{DatabaseID: scheduleUUID(database), OwnerID: owner, OrgID: org})
	if err != nil || removed == 1 {
		return err
	}
	_, err = s.Get(ctx, owner, org, database)
	switch {
	case errors.Is(err, managedpostgres.ErrNotFound):
		return nil
	case err != nil:
		return err
	default:
		return managedpostgres.ErrConflict
	}
}

func (s *BackupSchedules) Run(ctx context.Context, log *slog.Logger) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if err := s.sweep(ctx); err != nil && !errors.Is(err, pgx.ErrNoRows) && ctx.Err() == nil {
			log.Warn("PostgreSQL backup schedule sweep failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *BackupSchedules) sweep(ctx context.Context) error {
	row, err := sqlc.New().ClaimNativePostgresBackupSchedule(ctx, s.store, sqlc.ClaimNativePostgresBackupScheduleParams{
		LeaseToken: scheduleUUID(uuid.NewString()), RunID: scheduleUUID(uuid.NewString()),
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithDeadline(managedpostgres.WithOwnership(ctx, row.OrgID, row.OwnerID), row.LeaseUntil.Time.Add(-time.Minute))
	defer cancel()
	if !time.Now().Before(row.RunDeadline.Time) {
		return s.finish(ctx, row, "", "run_deadline_exceeded")
	}
	dest := BackupDestination{
		OrgID: row.OrgID, BucketAccountID: row.BucketAccountID.String(),
		BucketAppID: row.BucketAppID.String(), BucketID: row.BucketID.String(),
	}
	if err := s.objects.CheckBackupDestination(ctx, dest); err != nil {
		return s.retry(ctx, row, row.ExportQueued, "destination_unavailable")
	}
	if !row.ExportQueued {
		_, err := s.exports.Start(ctx, row.OwnerID, row.DatabaseID.String(), row.RunID.String())
		if err != nil {
			return s.retry(ctx, row, false, "export_unavailable")
		}
		return s.retry(ctx, row, true, "")
	}
	return s.deliver(ctx, row, dest)
}

func (s *BackupSchedules) deliver(ctx context.Context, row sqlc.NativePostgresBackupSchedule, dest BackupDestination) error {
	database, id := row.DatabaseID.String(), row.RunID.String()
	job, err := s.exports.Status(ctx, row.OwnerID, row.OrgID, database, id)
	switch {
	case errors.Is(err, managedpostgres.ErrNotFound):
		code := "export_expired"
		if row.LastErrorCode.Valid {
			code = row.LastErrorCode.String
		}
		return s.finish(ctx, row, "", code)
	case err != nil:
		return s.retry(ctx, row, true, "export_unavailable")
	case job.State == "queued" || job.State == "running":
		return s.retry(ctx, row, true, "")
	case job.State != "ready":
		return s.finish(ctx, row, "", "export_failed")
	}
	source, err := s.exports.source.ExportSource(ctx, row.OwnerID, database)
	if err != nil || source.OrgID != row.OrgID {
		return s.retry(ctx, row, true, "source_unavailable")
	}
	manifest, body, err := s.exports.Open(ctx, row.OwnerID, row.OrgID, database, id)
	if err != nil {
		return s.retry(ctx, row, true, "archive_unavailable")
	}
	defer body.Close() //nolint:errcheck
	key := fmt.Sprintf("postgres-backups/%s/%s/%s.dump", database, row.NextRunAt.Time.UTC().Format("2006-01-02"), id)
	if err := s.writeBackup(ctx, dest, key, manifest, body); err != nil {
		var limit *state.ObjectStorageLimitError
		if errors.As(err, &limit) && limit.Kind == "backup_single_put_bytes" {
			return s.finish(ctx, row, "", "archive_exceeds_bucket_upload_limit")
		}
		return s.retry(ctx, row, true, "delivery_failed")
	}
	return s.finish(ctx, row, key, "")
}

func (s *BackupSchedules) writeBackup(ctx context.Context, dest BackupDestination, key string, manifest api.ManagedPostgresExportManifest, body io.Reader) error {
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if err := s.objects.PutBackupObject(ctx, dest, key, "application/octet-stream", manifest.SHA256, manifest.SizeBytes, body); err != nil {
		return err
	}
	hash := sha256.Sum256(data)
	return s.objects.PutBackupObject(ctx, dest, key+".manifest.json", "application/json", hex.EncodeToString(hash[:]), int64(len(data)), bytes.NewReader(data))
}

func (s *BackupSchedules) retry(ctx context.Context, row sqlc.NativePostgresBackupSchedule, queued bool, code string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	delay := float64(10)
	if code != "" {
		delay = 60
	}
	count, err := sqlc.New().RetryNativePostgresBackupSchedule(ctx, s.store, sqlc.RetryNativePostgresBackupScheduleParams{
		DatabaseID: row.DatabaseID, LeaseToken: row.LeaseToken, ExportQueued: queued, ErrorCode: code, RetrySeconds: delay,
	})
	return settleBackupLease(count, err)
}

func (s *BackupSchedules) finish(ctx context.Context, row sqlc.NativePostgresBackupSchedule, key, code string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	now := time.Now().UTC()
	count, err := sqlc.New().FinishNativePostgresBackupSchedule(ctx, s.store, sqlc.FinishNativePostgresBackupScheduleParams{
		DatabaseID: row.DatabaseID, LeaseToken: row.LeaseToken, ErrorCode: code,
		NextRunAt: pgtype.Timestamptz{Time: nextDailyBackup(now, row.UtcMinute), Valid: true},
		SuccessAt: pgtype.Timestamptz{Time: now, Valid: key != ""}, ArchiveKey: pgtype.Text{String: key, Valid: key != ""},
	})
	return settleBackupLease(count, err)
}

func settleBackupLease(count int64, err error) error {
	if err == nil && count != 1 {
		return managedpostgres.ErrConflict
	}
	return err
}
