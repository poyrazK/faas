package managedpostgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

// BackupStatus reads evidence for a database already resolved in the
// customer's ownership scope. It reads scheduler records only.
func (s *Service) BackupStatus(ctx context.Context, d Database) (*api.ManagedPostgresBackupStatus, error) {
	store, ok := s.store.(*PostgresStore)
	if !ok || d.ProviderResourceID == "" {
		return nil, nil
	}
	backend, err := s.registry.Resolve(d.BackendID, d.BackendFingerprint)
	if err != nil {
		return nil, err
	}
	if backend.Driver != "firecracker" {
		return nil, nil
	}
	id, err := postgresUUID(d.ProviderResourceID)
	if err != nil {
		return nil, err
	}

	status := api.ManagedPostgresBackupStatus{RestoreWindowSeconds: d.Spec.RestoreWindowSeconds}
	var latest, verified recordedRestoreDrill
	err = store.pool.QueryRow(ctx, `
		SELECT v.last_backup_at,
		       latest.drill_id, latest.result, latest.started_at, latest.target_at, latest.finished_at, latest.recovered_to, latest.error_code,
		       verified.drill_id, verified.result, verified.started_at, verified.target_at, verified.finished_at, verified.recovered_to, verified.error_code
		FROM database_vms v
		LEFT JOIN LATERAL (
			SELECT CASE WHEN request_id IS NOT NULL THEN drill_id::text END AS drill_id,
			       result, started_at, target_at, finished_at, recovered_to, error_code
			FROM database_restore_drills
			WHERE source_id = v.database_id
			  AND NOT EXISTS (SELECT 1 FROM database_upgrade_rehearsals r WHERE r.drill_id = database_restore_drills.drill_id)
			ORDER BY started_at DESC, drill_id DESC LIMIT 1
		) latest ON true
		LEFT JOIN LATERAL (
			SELECT CASE WHEN request_id IS NOT NULL THEN drill_id::text END AS drill_id,
			       result, started_at, target_at, finished_at, recovered_to, error_code
			FROM database_restore_drills
			WHERE source_id = v.database_id
			  AND NOT EXISTS (SELECT 1 FROM database_upgrade_rehearsals r WHERE r.drill_id = database_restore_drills.drill_id) AND result = 'ok'
			ORDER BY started_at DESC, drill_id DESC LIMIT 1
		) verified ON true
		WHERE v.database_id = $1`, id).
		Scan(&status.LastBaseBackupAt, &latest.id, &latest.result, &latest.startedAt, &latest.targetAt, &latest.finishedAt, &latest.recoveredTo, &latest.errorCode,
			&verified.id, &verified.result, &verified.startedAt, &verified.targetAt, &verified.finishedAt, &verified.recoveredTo, &verified.errorCode)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	status.LastBaseBackupAt = backupTimestampUTC(status.LastBaseBackupAt)
	status.LatestRestoreDrill = latest.status()
	status.LastVerifiedRestore = verified.status()
	status.Recovery = s.regionalRecoveryStatus(ctx, d)
	return &status, nil
}

type recordedRestoreDrill struct {
	id, result, errorCode                        *string
	startedAt, targetAt, finishedAt, recoveredTo *time.Time
}

func (d recordedRestoreDrill) status() *api.ManagedPostgresRestoreDrillStatus {
	if d.result == nil {
		return nil
	}
	outcome := *d.result
	if outcome == "ok" {
		outcome = "succeeded"
	}
	out := &api.ManagedPostgresRestoreDrillStatus{
		Outcome:     outcome,
		StartedAt:   d.startedAt.UTC(),
		TargetAt:    d.targetAt.UTC(),
		FinishedAt:  backupTimestampUTC(d.finishedAt),
		RecoveredTo: backupTimestampUTC(d.recoveredTo),
	}
	if d.id != nil {
		out.DrillID = *d.id
	}
	if d.errorCode != nil {
		out.ErrorCode = *d.errorCode
	}
	out.SetDuration()
	return out
}

func backupTimestampUTC(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
}
