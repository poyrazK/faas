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

func requireCloneSchedulerFence(t *testing.T, err error) {
	t.Helper()
	var e *pgconn.PgError
	if !errors.As(err, &e) || e.Code != "23514" || e.ConstraintName != "clone_checkpoint_scheduler_request_fenced" {
		t.Fatalf("want permanent admission fence, got %v", err)
	}
}

func TestPgCloneSchedulerFenceAdmissionsAndRetention(t *testing.T) {
	s, pool, l := pgWorkloadRetirementFixture(t, "deployed")
	ctx := t.Context()
	in, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	w := in.Plan.Workloads[0]
	node := resolveDefaultLocal(t, ctx, s)
	ins, err := s.CreateInstance(ctx, w.AppID, w.TargetDeploymentID, string(state.StateRunning), 128, node, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	in, dispatch, err := s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
	if err != nil || !dispatch {
		t.Fatal("claim", err)
	}
	for _, mode := range []string{"normal", "mirror", "service", "worker", "job"} {
		_, err = s.CreateInstanceWithMode(ctx, w.AppID, w.TargetDeploymentID, string(state.StateColdBooting), 128, node, uuid.NewString(), mode)
		requireCloneSchedulerFence(t, err)
	}
	// Direct SQL represents independent exclusive/qualification admission paths.
	_, err = pool.Exec(ctx, "INSERT INTO instances(app_id,deployment_id,state,ram_mb,node_id) VALUES($1,$2,'cold_booting',128,$3)", w.AppID, w.TargetDeploymentID, node)
	requireCloneSchedulerFence(t, err)
	_, err = pool.Exec(ctx, "UPDATE deployments SET reason='foreign' WHERE id=$1", w.TargetDeploymentID)
	requireCloneSchedulerFence(t, err)
	_, err = pool.Exec(ctx, "UPDATE instances SET state='parked' WHERE id=$1", ins.ID)
	if err != nil {
		t.Fatal("cleanup transition", err)
	}
	_, err = pool.Exec(ctx, "UPDATE instances SET state='waking' WHERE id=$1", ins.ID)
	requireCloneSchedulerFence(t, err)
	// ADR-595 makes a wake instance's artifact identity immutable, so that
	// guard refuses the move before the clone fence can.
	_, err = pool.Exec(ctx, "UPDATE instances SET deployment_id=$2 WHERE id=$1", ins.ID, w.SourceDeploymentID)
	var identity *pgconn.PgError
	if !errors.As(err, &identity) || identity.ConstraintName != "application_standard_runtime_identity" {
		requireCloneSchedulerFence(t, err)
	}
	if _, err = pool.Exec(ctx, "DELETE FROM instances WHERE id=$1", ins.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l, pgWorkloadRetirementProof(in.Plan)); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"TRUNCATE project_environment_clone_checkpoint_workload_retirements CASCADE",
		"DELETE FROM project_environment_clone_checkpoint_workload_retirements WHERE operation_id=$1",
		"UPDATE project_environment_clone_checkpoint_workload_retirements SET state='planned',requested_at=NULL,observation=NULL,observation_sha256=NULL,retired_at=NULL WHERE operation_id=$1",
		"UPDATE project_environment_clone_checkpoint_workload_retirements SET plan=jsonb_set(plan,'{Workloads}','[]') WHERE operation_id=$1",
	} {
		if strings.HasPrefix(q, "TRUNCATE") {
			_, err = pool.Exec(ctx, q)
		} else {
			_, err = pool.Exec(ctx, q, l.Operation.ID)
		}
		var e *pgconn.PgError
		if !errors.As(err, &e) || e.ConstraintName != "clone_checkpoint_scheduler_fence_retained" {
			t.Fatal("lost retained fence", err)
		}
	}
	// Removing catalogue metadata cannot reopen either the frozen UUID or namespace.
	if _, err = pool.Exec(ctx, "DELETE FROM project_environment_workload_deployment_specs WHERE deployment_id=$1", w.TargetDeploymentID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "DELETE FROM deployments WHERE id=$1", w.TargetDeploymentID); err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct{ id, reason string }{{w.TargetDeploymentID, "foreign"}, {uuid.NewString(), "environment-clone:" + l.Operation.ID}} {
		_, err = pool.Exec(ctx, `INSERT INTO deployments SELECT (jsonb_populate_record(NULL::deployments,to_jsonb(d)||jsonb_build_object('id',$1::text,'reason',$2::text,'revision',d.revision+1))).* FROM deployments d WHERE d.id=$3`, v.id, v.reason, w.SourceDeploymentID)
		requireCloneSchedulerFence(t, err)
	}
	// Unrelated source work remains schedulable.
	if _, err = s.CreateInstance(ctx, w.AppID, w.SourceDeploymentID, string(state.StateColdBooting), 128, node, uuid.NewString()); err != nil {
		t.Fatal("source admission", err)
	}
	raw, err := migrations.FS.ReadFile("20261011232006416_clone_scheduler_request_fences.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, strings.SplitN(string(raw), "-- +goose Down", 2)[1])
	var e *pgconn.PgError
	if !errors.As(err, &e) || e.ConstraintName != "clone_checkpoint_scheduler_fence_down_no_evidence" {
		t.Fatal("unsafe downgrade", err)
	}
}

func TestPgCloneSchedulerFenceWaitingAndOldSnapshots(t *testing.T) {
	s, pool, l := pgWorkloadRetirementFixture(t, "deployed")
	ctx := t.Context()
	in, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	w := in.Plan.Workloads[0]
	node := resolveDefaultLocal(t, ctx, s)
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
	result := make(chan error, 1)
	go func() {
		_, e := pool.Exec(ctx, "INSERT INTO instances(app_id,deployment_id,state,ram_mb,node_id) VALUES($1,$2,'cold_booting',128,$3)", w.AppID, w.TargetDeploymentID, node)
		result <- e
	}()
	// Verify the writer is actually queued on the fence, rather than rely on sleep.
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err = pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE 'INSERT INTO instances(app_id,deployment_id,state,ram_mb,node_id)%')").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("admission did not queue")
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
	case err = <-result:
		requireCloneSchedulerFence(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("queued writer did not return")
	}
	_, err = old.Exec(ctx, "INSERT INTO instances(app_id,deployment_id,state,ram_mb,node_id) VALUES($1,$2,'cold_booting',128,$3)", w.AppID, w.TargetDeploymentID, node)
	var e *pgconn.PgError
	if !errors.As(err, &e) || e.Code != "40001" {
		t.Fatal("stale snapshot admitted", err)
	}
}

func TestPgCloneSchedulerFenceEmptyMigrationReplay(t *testing.T) {
	_, pool, _ := pgWorkloadRetirementFixture(t, "unmaterialized")
	raw, err := migrations.FS.ReadFile("20261011232006416_clone_scheduler_request_fences.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	for _, q := range []string{parts[1], parts[0], parts[1], parts[0]} {
		if _, err = tx.Exec(t.Context(), q); err != nil {
			t.Fatal("migration replay", err)
		}
	}
}
