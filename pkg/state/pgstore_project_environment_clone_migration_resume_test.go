//go:build !no_pg

// adr: 590
package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgCloneMigrationResumeRetainsOriginalSource(t *testing.T) {
	s, pool, l := pgWorkloadRetirementFixture(t, "deployed")
	ctx := t.Context()
	in, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	w := in.Plan.Workloads[0]
	source := resolveDefaultLocal(t, ctx, s)
	ins, err := s.CreateInstance(ctx, w.AppID, w.TargetDeploymentID, string(state.StateRunning), 128, source, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	lease := state.MigrationLease{InstanceID: ins.ID, LeaseToken: uuid.NewString(), SourceNodeID: source, CreatedAt: time.Now().UTC().Truncate(time.Microsecond), LeaseExpiresAt: time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)}
	if err = s.ReserveMigrationLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkInstanceMigrating(ctx, ins.ID, source, lease.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err = s.CancelInstanceMigration(ctx, ins.ID, source, lease.LeaseToken); err != nil {
		t.Fatal(err)
	}
	record := func(node, token string) error {
		return s.RecordCloneCheckpointMigrationResume(ctx, ins.ID, node, token)
	}
	for _, bad := range []struct{ node, token string }{{uuid.NewString(), lease.LeaseToken}, {source, uuid.NewString()}} {
		if err = record(bad.node, bad.token); err == nil {
			t.Fatal("foreign source resumed")
		}
	}
	// Cancel follows durable rollback, whose parked row has no instance token.
	// Pending or expired source evidence must not disappear on this resume path.
	if err = record(source, lease.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err = record(source, lease.LeaseToken); err == nil {
		t.Fatal("unknown source resume redispatched")
	}
	if err = s.DeleteMigrationLease(ctx, lease.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteInstance(ctx, ins.ID); err != nil {
		t.Fatal(err)
	}
	var src, token, wake, dep string
	var created, expires time.Time
	if err = pool.QueryRow(ctx, "SELECT source_node_id,lease_token,wake_id::text,deployment_id::text,lease_created_at,lease_expires_at FROM project_environment_clone_migration_resumes WHERE instance_id=$1", ins.ID).Scan(&src, &token, &wake, &dep, &created, &expires); err != nil || src != source || token != lease.LeaseToken || wake != ins.WakeID || dep != w.TargetDeploymentID || !created.Equal(lease.CreatedAt) || !expires.Equal(lease.LeaseExpiresAt) {
		t.Fatal("original source evidence lost", err)
	}
	for _, q := range []string{"DELETE FROM project_environment_clone_migration_resumes", "UPDATE project_environment_clone_migration_resumes SET source_node_id='foreign'", "TRUNCATE project_environment_clone_migration_resumes"} {
		_, err = pool.Exec(ctx, q)
		var e *pgconn.PgError
		if !errors.As(err, &e) || e.ConstraintName != "clone_checkpoint_migration_resume_retained" {
			t.Fatal("resume evidence removed", err)
		}
	}
	in, _, err = s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l, pgWorkloadRetirementProof(in.Plan)); !errors.Is(err, state.ErrConflict) {
		t.Fatal("source uncertainty accepted as closure", err)
	}
	raw, err := migrations.FS.ReadFile("20261011232006432_clone_migration_resume_dispatch.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, strings.SplitN(string(raw), "-- +goose Down", 2)[1])
	var e *pgconn.PgError
	if !errors.As(err, &e) || e.ConstraintName != "clone_checkpoint_migration_resume_down_no_evidence" {
		t.Fatal("evidence-bearing rollback allowed", err)
	}
	other, err := s.CreateInstance(ctx, w.AppID, w.SourceDeploymentID, string(state.StateRunning), 128, source, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordCloneCheckpointMigrationResume(ctx, other.ID, source, "legacy-token"); err != nil {
		t.Fatal("unrelated source fenced", err)
	}
	var count int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM project_environment_clone_migration_resumes").Scan(&count); err != nil || count != 1 {
		t.Fatal("unrelated source recorded", err)
	}
}

func TestPgCloneMigrationResumeQueuedAndStaleSnapshot(t *testing.T) {
	s, pool, l := pgWorkloadRetirementFixture(t, "deployed")
	ctx := t.Context()
	in, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	w := in.Plan.Workloads[0]
	source := resolveDefaultLocal(t, ctx, s)
	ins, err := s.CreateInstance(ctx, w.AppID, w.TargetDeploymentID, string(state.StateRunning), 128, source, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	if err = s.ReserveMigrationLease(ctx, state.MigrationLease{InstanceID: ins.ID, LeaseToken: token, SourceNodeID: source, CreatedAt: time.Now().UTC(), LeaseExpiresAt: time.Now().UTC().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteMigrationLease(ctx, token, "mem", "state"); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkInstanceMigrating(ctx, ins.ID, source, token); err != nil {
		t.Fatal(err)
	}
	old, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = old.Rollback(ctx) }()
	if _, err = old.Exec(ctx, "SELECT state FROM project_environment_clone_checkpoint_workload_retirements"); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT lock_clone_checkpoint_scheduler_admission()"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- s.RecordCloneCheckpointMigrationResume(ctx, ins.ID, source, token)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err = pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%SELECT record_clone_checkpoint_migration_resume%')").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("resume did not queue")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = tx.Exec(ctx, "UPDATE project_environment_clone_checkpoint_workload_retirements SET state='requested',requested_at=clock_timestamp() WHERE operation_id=$1", l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		requireCloneSchedulerFence(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("resume remained queued")
	}
	_, err = old.Exec(ctx, "SELECT record_clone_checkpoint_migration_resume($1,$2,$3)", ins.ID, source, token)
	var e *pgconn.PgError
	if !errors.As(err, &e) || e.Code != "40001" {
		t.Fatal("stale resume admitted", err)
	}
}

func TestPgCloneMigrationResumeEmptyMigrationReplay(t *testing.T) {
	_, pool, _ := pgWorkloadRetirementFixture(t, "unmaterialized")
	ctx := t.Context()
	raw, err := migrations.FS.ReadFile("20261011232006432_clone_migration_resume_dispatch.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, q := range []string{parts[1], parts[0], parts[1], parts[0]} {
		if _, err = tx.Exec(ctx, q); err != nil {
			t.Fatal("empty migration replay", err)
		}
	}
}
