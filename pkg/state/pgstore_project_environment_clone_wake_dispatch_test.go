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

func TestPgCloneWakeDispatchRetainsUnknownRPCOutcome(t *testing.T) {
	for _, method := range []string{"cold_boot", "restore"} {
		t.Run(method, func(t *testing.T) {
			s, pool, l := pgWorkloadRetirementFixture(t, "deployed")
			ctx := t.Context()
			in, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
			if err != nil {
				t.Fatal(err)
			}
			w := in.Plan.Workloads[0]
			node := resolveDefaultLocal(t, ctx, s)
			ins, err := s.CreateInstance(ctx, w.AppID, w.TargetDeploymentID, string(state.StateColdBooting), 128, node, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			record := func() error {
				return s.RecordCloneCheckpointWakeDispatch(ctx, ins.ID, ins.AppID, ins.DeploymentID, node, method)
			}
			if err = s.RecordCloneCheckpointWakeDispatch(ctx, ins.ID, ins.AppID, ins.DeploymentID, uuid.NewString(), method); err == nil {
				t.Fatal("foreign node accepted")
			}
			if err = record(); err != nil {
				t.Fatal(err)
			}
			if err = record(); err == nil {
				t.Fatal("unknown RPC redispatched")
			}
			in, _, err = s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
			if err != nil {
				t.Fatal(err)
			}
			requireCloneSchedulerFence(t, record())
			// Metadata absence and a caller's all-clear flags cannot settle the RPC.
			if _, err = pool.Exec(ctx, "DELETE FROM instances WHERE id=$1", ins.ID); err != nil {
				t.Fatal(err)
			}
			if err = s.FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l, pgWorkloadRetirementProof(in.Plan)); !errors.Is(err, state.ErrConflict) {
				t.Fatal("unknown RPC accepted", err)
			}
			for _, q := range []string{"DELETE FROM project_environment_clone_wake_dispatches", "UPDATE project_environment_clone_wake_dispatches SET node_id=gen_random_uuid()", "TRUNCATE project_environment_clone_wake_dispatches"} {
				_, err = pool.Exec(ctx, q)
				var e *pgconn.PgError
				if !errors.As(err, &e) || e.ConstraintName != "clone_checkpoint_wake_dispatch_retained" {
					t.Fatal("lost dispatch evidence", err)
				}
			}
			var dep, reason, wake string
			if err = pool.QueryRow(ctx, "SELECT deployment_id::text,request_reason,wake_id::text FROM project_environment_clone_wake_dispatches WHERE instance_id=$1", ins.ID).Scan(&dep, &reason, &wake); err != nil || dep != ins.DeploymentID || reason != "environment-clone:"+l.Operation.ID || wake != ins.WakeID {
				t.Fatal("original identity lost", dep, reason, wake, err)
			}
			source, err := s.CreateInstance(ctx, w.AppID, w.SourceDeploymentID, string(state.StateColdBooting), 128, node, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if err = s.RecordCloneCheckpointWakeDispatch(ctx, source.ID, source.AppID, source.DeploymentID, node, method); err != nil {
					t.Fatal("source dispatch", err)
				}
			}
			var count int
			if err = pool.QueryRow(ctx, "SELECT count(*) FROM project_environment_clone_wake_dispatches").Scan(&count); err != nil || count != 1 {
				t.Fatal("source acquired clone retention", count, err)
			}
			raw, err := migrations.FS.ReadFile("20261011232006426_clone_wake_dispatch_evidence.sql")
			if err != nil {
				t.Fatal(err)
			}
			_, err = pool.Exec(ctx, strings.SplitN(string(raw), "-- +goose Down", 2)[1])
			var e *pgconn.PgError
			if !errors.As(err, &e) || e.ConstraintName != "clone_checkpoint_wake_dispatch_down_no_evidence" {
				t.Fatal("unsafe downgrade", err)
			}
		})
	}
}

func TestPgCloneWakeDispatchQueuedAndStaleSnapshot(t *testing.T) {
	s, pool, l := pgWorkloadRetirementFixture(t, "deployed")
	ctx := t.Context()
	in, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	w := in.Plan.Workloads[0]
	node := resolveDefaultLocal(t, ctx, s)
	ins, err := s.CreateInstance(ctx, w.AppID, w.TargetDeploymentID, string(state.StateWaking), 128, node, uuid.NewString())
	if err != nil {
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
		done <- s.RecordCloneCheckpointWakeDispatch(ctx, ins.ID, ins.AppID, ins.DeploymentID, node, "restore")
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err = pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%SELECT record_clone_checkpoint_wake_dispatch%')").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("wake did not queue")
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
		t.Fatal("wake remained queued")
	}
	_, err = old.Exec(ctx, "SELECT record_clone_checkpoint_wake_dispatch($1,$2,$3,$4,'restore')", ins.ID, ins.AppID, ins.DeploymentID, node)
	var e *pgconn.PgError
	if !errors.As(err, &e) || e.Code != "40001" {
		t.Fatal("stale wake admitted", err)
	}
}

func TestPgCloneWakeDispatchEmptyMigrationReplay(t *testing.T) {
	_, pool, _ := pgWorkloadRetirementFixture(t, "unmaterialized")
	ctx := t.Context()
	raw, err := migrations.FS.ReadFile("20261011232006426_clone_wake_dispatch_evidence.sql")
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
			t.Fatal("empty replay", err)
		}
	}
}

func TestPgCloneWakeDispatchPrepareOriginalTransport(t *testing.T) {
	for _, method := range []string{"cold_boot", "restore"} {
		t.Run(method, func(t *testing.T) {
			s, pool, l := pgWorkloadRetirementFixture(t, "deployed")
			ctx := t.Context()
			in, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
			if err != nil {
				t.Fatal(err)
			}
			w := in.Plan.Workloads[0]
			node := resolveDefaultLocal(t, ctx, s)
			ins, err := s.CreateInstance(ctx, w.AppID, w.TargetDeploymentID, string(state.StateColdBooting), 128, node, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			if out, err := s.PrepareCloneCheckpointWakeDispatch(ctx, ins.ID, ins.AppID, ins.DeploymentID, uuid.NewString(), method); err == nil || out != nil {
				t.Fatal("foreign node acquired ownership", err)
			}
			out, err := s.PrepareCloneCheckpointWakeDispatch(ctx, ins.ID, ins.AppID, ins.DeploymentID, node, method)
			if err != nil || out == nil {
				t.Fatal("original dispatch unavailable", err)
			}
			if out.InstanceID != ins.ID || out.AppID != ins.AppID || out.DeploymentID != ins.DeploymentID || out.NodeID != node || out.WakeID != ins.WakeID || out.Method != method || out.RequestReason != "environment-clone:"+l.Operation.ID || out.DispatchedAt.IsZero() {
				t.Fatal("original fields changed")
			}
			var at time.Time
			if err = pool.QueryRow(ctx, "SELECT dispatched_at FROM project_environment_clone_wake_dispatches WHERE instance_id=$1", ins.ID).Scan(&at); err != nil || !at.Equal(out.DispatchedAt) {
				t.Fatal("timestamp synthesized before commit", err)
			}
			if replay, err := s.PrepareCloneCheckpointWakeDispatch(ctx, ins.ID, ins.AppID, ins.DeploymentID, node, method); err == nil || replay != nil {
				t.Fatal("unknown RPC outcome replayed", err)
			}
			if _, err = pool.Exec(ctx, "UPDATE deployments SET reason='metadata-changed' WHERE id=$1", ins.DeploymentID); err != nil {
				t.Fatal(err)
			}
			if replay, err := s.PrepareCloneCheckpointWakeDispatch(ctx, ins.ID, ins.AppID, ins.DeploymentID, node, method); !errors.Is(err, state.ErrConflict) || replay != nil {
				t.Fatal("changed deployment metadata authorized replay", err)
			}
			if _, err = pool.Exec(ctx, "UPDATE deployments SET reason=$2 WHERE id=$1", ins.DeploymentID, "environment-clone:"+l.Operation.ID); err != nil {
				t.Fatal(err)
			}
			source, err := s.CreateInstance(ctx, w.AppID, w.SourceDeploymentID, string(state.StateColdBooting), 128, node, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			if ordinary, err := s.PrepareCloneCheckpointWakeDispatch(ctx, source.ID, source.AppID, source.DeploymentID, node, method); err != nil || ordinary != nil {
				t.Fatal("ordinary source acquired clone envelope", err)
			}
			if _, _, err = s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l); err != nil {
				t.Fatal(err)
			}
			fresh, err := s.CreateInstance(ctx, w.AppID, w.SourceDeploymentID, string(state.StateColdBooting), 128, node, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			if denied, err := s.PrepareCloneCheckpointWakeDispatch(ctx, fresh.ID, fresh.AppID, w.TargetDeploymentID, node, method); err == nil || denied != nil {
				t.Fatal("changed owner escaped fence", err)
			}
		})
	}
}
