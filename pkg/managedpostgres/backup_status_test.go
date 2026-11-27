package managedpostgres

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNativeBackupStatusUsesDatabaseRestoreEvidence(t *testing.T) {
	store, pool, ctx, accountID := postgresStoreFixture(t)
	catalogID, runtimeID, otherRuntimeID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	fingerprint := strings.Repeat("a", 64)
	at := time.Date(2026, 11, 21, 12, 0, 0, 123000000, time.UTC)
	exec := func(statement string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, statement, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO database_nodes (id, target_url, admission_ceiling_mb, volume_capacity_bytes)
		VALUES ('db-a', 'unix:///a', 4096, 21474836480)`)
	exec(`INSERT INTO database_vms (database_id, node_id, postgres_major, vcpu_count, mem_mib, volume_bytes, state, bootstrapped)
		VALUES ($1, 'db-a', 17, 1, 384, 1073741824, 'parked', true),
		       ($2, 'db-a', 17, 1, 384, 1073741824, 'parked', true)`, runtimeID, otherRuntimeID)
	exec(`INSERT INTO managed_postgres_databases (id, account_id, name, region, postgres_major, service_class, availability,
		backend_id, backend_fingerprint, provider_resource_id, data_resource_id, state, observed_generation, restore_window_seconds)
		VALUES ($1, $2, 'backup-db', 'eu', 17, 'development', 'single_zone',
			'native', $3, $4, $4, 'ready', 1, 604800)`, catalogID, accountID, fingerprint, runtimeID)
	drill := func(source, result string, start time.Time, recoveredTo *time.Time) {
		t.Helper()
		var finishedAt *time.Time
		if result != "running" {
			finished := start.Add(time.Minute)
			finishedAt = &finished
		}
		exec(`INSERT INTO database_restore_drills (drill_id, node_id, source_id, result, started_at, target_at, finished_at, recovered_to)
			VALUES ($1, 'db-a', $2, $3, $4, $5, $6, $7)`,
			uuid.NewString(), source, result, start, start.Add(-time.Second), finishedAt, recoveredTo)
	}
	drill(otherRuntimeID, "ok", at.Add(6*time.Hour), &at)

	service := Service{store: store, registry: &Registry{backends: map[string]Backend{
		"native":  {Driver: "firecracker", Fingerprint: fingerprint},
		"foreign": {Driver: "neon", Fingerprint: fingerprint},
	}}}
	db, err := customerDatabase(ctx, store, accountID, catalogID)
	if err != nil {
		t.Fatal(err)
	}
	status, err := service.BackupStatus(ctx, db)
	if err != nil || status == nil || status.RestoreWindowSeconds != 604800 ||
		status.LastBaseBackupAt != nil || status.LatestRestoreDrill != nil || status.LastVerifiedRestore != nil {
		t.Fatalf("status before own backup or drill = %+v, %v", status, err)
	}

	exec(`UPDATE database_vms SET last_backup_at = $2 WHERE database_id = $1`, runtimeID, at)
	drill(runtimeID, "ok", at.Add(time.Hour), &at)
	drill(runtimeID, "failed", at.Add(2*time.Hour), nil)
	status, err = service.BackupStatus(ctx, db)
	if err != nil || status == nil || status.LastBaseBackupAt == nil || !status.LastBaseBackupAt.Equal(at) {
		t.Fatalf("status after recorded backup = %+v, %v", status, err)
	}
	if latest := status.LatestRestoreDrill; latest == nil || latest.Outcome != "failed" || latest.RecoveredTo != nil ||
		!latest.StartedAt.Equal(at.Add(2*time.Hour)) {
		t.Fatalf("latest drill = %+v", latest)
	}
	if verified := status.LastVerifiedRestore; verified == nil || verified.Outcome != "succeeded" ||
		verified.FinishedAt == nil || verified.RecoveredTo == nil || !verified.RecoveredTo.Equal(at) {
		t.Fatalf("last verified restore = %+v", verified)
	}

	drill(runtimeID, "running", at.Add(3*time.Hour), nil)
	status, err = service.BackupStatus(ctx, db)
	if err != nil || status == nil || status.LatestRestoreDrill == nil || status.LatestRestoreDrill.Outcome != "running" ||
		status.LatestRestoreDrill.FinishedAt != nil || status.LastVerifiedRestore == nil {
		t.Fatalf("status during another drill = %+v, %v", status, err)
	}

	requestedID := uuid.NewString()
	exec(`INSERT INTO database_restore_drills
		(drill_id, node_id, source_id, request_id, result, started_at, target_at)
		VALUES ($1, 'db-a', $2, $3, 'queued', $4, $5)`,
		requestedID, runtimeID, uuid.NewString(), at.Add(4*time.Hour), time.Time{})
	status, err = service.BackupStatus(ctx, db)
	if err != nil || status == nil || status.LatestRestoreDrill == nil ||
		status.LatestRestoreDrill.DrillID != requestedID || status.LatestRestoreDrill.Outcome != "queued" ||
		!status.LatestRestoreDrill.TargetAt.IsZero() || status.LastVerifiedRestore == nil {
		t.Fatalf("status after a requested drill = %+v, %v", status, err)
	}

	var state string
	if err := pool.QueryRow(ctx, `SELECT state FROM database_vms WHERE database_id = $1`, runtimeID).Scan(&state); err != nil || state != "parked" {
		t.Fatalf("status reads left VM in %q, %v", state, err)
	}
	db.BackendID = "foreign"
	if status, err := service.BackupStatus(ctx, db); err != nil || status != nil {
		t.Fatalf("backup evidence for non-native backend = %+v, %v", status, err)
	}
}
