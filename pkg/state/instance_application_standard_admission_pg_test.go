//go:build !no_pg

package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func runtimeCapturePGStore(t *testing.T) (*PgStore, *pgxpool.Pool) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return NewPgStore(pool), pool
}

func TestPgInstanceApplicationStandardCapture(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardRuntimeCaptureLifecycle(t, s)
}
func TestPgInstanceApplicationStandardReenrollmentDuringBoot(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardRuntimeReenrollmentDuringBoot(t, s)
}
func TestPgInstanceApplicationStandardArtifactChangeDuringBoot(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardRuntimeArtifactChangeDuringBoot(t, s)
}

func TestPgInstanceApplicationStandardLoggingChangeDuringBoot(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardRuntimeLoggingChangeDuringBoot(t, s)
}

func TestPgInstanceApplicationStandardAccountGrace(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardRuntimeAccountGrace(t, s)
}

func TestPgInstanceApplicationStandardRawGuards(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	ctx := t.Context()
	f := newRuntimeCaptureFixture(t, s, true)
	ins, err := s.CreateInstance(ctx, f.app.ID, f.dep.ID, string(StateColdBooting), 128, f.nodeID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`UPDATE instance_application_standard_admissions SET input_snapshot='{}'::jsonb WHERE instance_id=$1`,
		`DELETE FROM instance_application_standard_admissions WHERE instance_id=$1`,
		`UPDATE instances SET app_id=NULL,kind='job_task' WHERE id=$1`,
		`UPDATE instances SET ram_mb=256 WHERE id=$1`,
		`UPDATE instances SET mode='worker' WHERE id=$1`,
	} {
		_, err := pool.Exec(ctx, sql, ins.ID)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Fatalf("raw SQL bypassed capture guard: %s: %v", sql, err)
		}
	}
	// A parked legacy row must not acquire a fabricated history from raw SQL.
	legacy, err := s.CreateInstance(ctx, f.app.ID, f.dep.ID, string(StateParked), 128, f.nodeID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO instance_application_standard_admissions(instance_id,app_id,deployment_id,input_snapshot)
 VALUES($1,$2,$3,application_standard_runtime_snapshot($2,$3))`, legacy.ID, f.app.ID, f.dep.ID)
	var failure *pgconn.PgError
	if !errors.As(err, &failure) || failure.ConstraintName != "application_standard_runtime_capture_immutable" {
		t.Fatalf("forged admission inserted: %v", err)
	}
	if err := s.UpdateInstanceState(ctx, legacy.ID, string(StateWaking)); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("legacy managed VM gained invented boot authority: %v", err)
	}
	if _, err := s.ScheduleAppDeletion(ctx, f.app.ID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimAppDeletion(ctx, f.app.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAppPermanently(ctx, f.app.ID); err != nil {
		t.Fatalf("owner erasure failed: %v", err)
	}
	var remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM instance_application_standard_admissions`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("capture retained on erasure: %d %v", remaining, err)
	}
}

func TestPgInstanceApplicationStandardCaptureInputContention(t *testing.T) {
	for _, lock := range []string{
		`SELECT 1 FROM apps WHERE id=$1 FOR UPDATE`,
		`SELECT 1 FROM app_application_standards WHERE app_id=$1 FOR UPDATE`,
		`SELECT pg_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.controls.' || $1::uuid::text,0))`,
	} {
		t.Run(lock, func(t *testing.T) {
			s, pool := runtimeCapturePGStore(t)
			ctx := t.Context()
			f := newRuntimeCaptureFixture(t, s, true)
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, lock, f.app.ID); err != nil {
				t.Fatal(err)
			}
			bounded, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			_, err = s.CreateInstance(bounded, f.app.ID, f.dep.ID, string(StateWaking), 128, f.nodeID, uuid.NewString())
			if !errors.Is(err, ErrApplicationStandardRuntimeBusy) {
				t.Fatalf("admission waited or missed input fence: %v", err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM instances WHERE app_id=$1`, f.app.ID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("failed capture reserved an instance: %d %v", count, err)
			}
			if _, err := s.CreateInstance(ctx, f.app.ID, f.dep.ID, string(StateWaking), 128, f.nodeID, uuid.NewString()); err != nil {
				t.Fatalf("retry stayed blocked: %v", err)
			}
		})
	}
}

func TestPgInstanceApplicationStandardCaptureFencesConcurrentArtifactWrite(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	ctx := t.Context()
	f := newRuntimeCaptureFixture(t, s, true)
	admitted, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer admitted.Rollback(ctx)
	var id string
	if err := admitted.QueryRow(ctx, `INSERT INTO instances(app_id,deployment_id,state,ram_mb,node_id) VALUES($1,$2,'cold_booting',128,$3) RETURNING id::text`, f.app.ID, f.dep.ID, f.nodeID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	// A content change cannot commit between the input snapshot and admission.
	bounded, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	err = s.SetDeploymentRootfs(bounded, f.dep.ID, "/new.ext4", "layers/new.ext4", 8192)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("artifact write escaped retained fence: %v", err)
	}
	if err := admitted.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(ctx, f.dep.ID, "/new.ext4", "layers/new.ext4", 8192); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishInstanceRuntime(ctx, id, string(StateColdBooting), "stale", "10.100.0.8", 20008); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("changed artifact published: %v", err)
	}
}
