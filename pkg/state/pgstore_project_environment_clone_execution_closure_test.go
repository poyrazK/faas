//go:build !no_pg

// adr: 590
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state"
)

// Reconstruct a previously admitted immutable execution, without a live GitOps
// graph or native VM. Controlled receipts exercise storage fencing only; they
// do not qualify native retirement or supply a production adapter.
func seedCloneQualificationExecution(t *testing.T, pool *pgxpool.Pool, ins state.Instance, p state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan, dispatched bool) state.EnvironmentQualificationExecution {
	t.Helper()
	frame := state.EnvironmentQualificationExecution{InstanceID: ins.ID, RequestID: uuid.NewString(), GraphID: uuid.NewString(), AppID: ins.AppID, DeploymentID: ins.DeploymentID, NodeID: ins.NodeID, WakeID: ins.WakeID, EnvironmentID: p.EnvironmentID, RAMMB: ins.RAMMB, Attempt: 1, CleanupToken: uuid.NewString()}
	data, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	// Only the unrelated graph-admission guard is bypassed during reconstruction.
	// The clone dispatch guard remains active, including in late-frame tests.
	if _, err = tx.Exec(t.Context(), "ALTER TABLE environment_qualification_executions DISABLE TRIGGER guard_environment_qualification_execution"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), "INSERT INTO environment_qualification_executions(instance_id,request_id,frame,cleanup_token,dispatch_started) VALUES($1,$2,$3,$4,$5)", frame.InstanceID, frame.RequestID, data, frame.CleanupToken, dispatched); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), "ALTER TABLE environment_qualification_executions ENABLE TRIGGER guard_environment_qualification_execution"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	return frame
}

func TestPgCloneWorkloadRetirementRejectsRemainingInstances(t *testing.T) {
	for _, st := range []state.State{state.StateColdBooting, state.StateRunning, state.StateParked, state.StateStopped} {
		t.Run(string(st), func(t *testing.T) {
			s, pool, l := pgWorkloadRetirementFixture(t, "deployed")
			ctx := t.Context()
			in, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
			if err != nil {
				t.Fatal(err)
			}
			w := in.Plan.Workloads[0]
			node := resolveDefaultLocal(t, ctx, s)
			ins, err := s.CreateInstance(ctx, w.AppID, w.TargetDeploymentID, string(st), 128, node, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			in, _, err = s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
			if err != nil {
				t.Fatal(err)
			}
			// A caller's all-clear flags cannot overrule actual original instance rows.
			if err = s.FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l, pgWorkloadRetirementProof(in.Plan)); !errors.Is(err, state.ErrConflict) {
				t.Fatal("accepted remaining instance", err)
			}
			saved, err := s.ProjectEnvironmentCloneCheckpointWorkloadRetirementForLease(ctx, l)
			if err != nil || saved.State != "requested" || saved.ObservationSHA256 != "" {
				t.Fatal("partial receipt", saved, err)
			}
			if _, err = pool.Exec(ctx, "DELETE FROM instances WHERE id=$1", ins.ID); err != nil {
				t.Fatal(err)
			}
			if err = s.FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l, pgWorkloadRetirementProof(in.Plan)); err != nil {
				t.Fatal("closure after original row removal", err)
			}
		})
	}
}

func TestPgCloneWorkloadRetirementRequiresQualificationClosure(t *testing.T) {
	for _, dispatched := range []bool{false, true} {
		name := "never_dispatched"
		if dispatched {
			name = "unknown_native_outcome"
		}
		t.Run(name, func(t *testing.T) {
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
			frame := seedCloneQualificationExecution(t, pool, ins, in.Plan, dispatched)
			in, _, err = s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
			if err != nil {
				t.Fatal(err)
			}
			if !dispatched {
				_, err = pool.Exec(ctx, "UPDATE environment_qualification_executions SET dispatch_started=true WHERE instance_id=$1", ins.ID)
				requireCloneSchedulerFence(t, err)
			}
			if err = s.FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l, pgWorkloadRetirementProof(in.Plan)); !errors.Is(err, state.ErrConflict) {
				t.Fatal("accepted unresolved execution", err)
			}
			// No-dispatch proof cannot settle an earlier dispatch with an unknown reply.
			if dispatched {
				err = s.RetireEnvironmentQualificationExecution(ctx, frame, state.EnvironmentQualificationRetirement{Kind: state.QualificationNeverDispatched})
				if !errors.Is(err, state.ErrConflict) {
					t.Fatal("unknown effect erased", err)
				}
			}
			proof := state.EnvironmentQualificationRetirement{Kind: state.QualificationNeverDispatched}
			if dispatched {
				proof = state.EnvironmentQualificationRetirement{Kind: state.QualificationNativeRetired, ReceiptID: uuid.NewString(), NativeGeneration: uuid.NewString(), KernelBootID: uuid.NewString(), ProcessesExited: true, ResourcesRemoved: true}
			}
			if err = s.RetireEnvironmentQualificationExecution(ctx, frame, proof); err != nil {
				t.Fatal("original cleanup capability", err)
			}
			// Even a closed execution does not make a retained stopped instance absent.
			if err = s.FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l, pgWorkloadRetirementProof(in.Plan)); !errors.Is(err, state.ErrConflict) {
				t.Fatal("retained stopped row accepted", err)
			}
			if _, err = pool.Exec(ctx, "DELETE FROM instances WHERE id=$1", ins.ID); err != nil {
				t.Fatal(err)
			}
			if err = s.FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l, pgWorkloadRetirementProof(in.Plan)); err != nil {
				t.Fatal("original execution closure", err)
			}
			if _, err = s.ProjectEnvironmentCloneCheckpointWorkloadRetirementForLease(ctx, l); err != nil {
				t.Fatal("closed replay", err)
			}
			// Retired ownership also blocks late frames whose parent metadata
			// cannot establish a fresh admission. Cover both ownership selectors.
			for _, identity := range []struct{ deployment, environment string }{
				{frame.DeploymentID, uuid.NewString()},
				{uuid.NewString(), frame.EnvironmentID},
			} {
				_, err = pool.Exec(ctx, `INSERT INTO environment_qualification_executions(instance_id,request_id,frame,cleanup_token)
SELECT $1::text::uuid,$2::text::uuid,frame||jsonb_build_object('instance_id',$1::text,'request_id',$2::text,'deployment_id',$3::text,'environment_id',$4::text),gen_random_uuid()
FROM environment_qualification_executions WHERE instance_id=$5`, uuid.NewString(), uuid.NewString(), identity.deployment, identity.environment, ins.ID)
				requireCloneSchedulerFence(t, err)
			}
			_, err = pool.Exec(ctx, "TRUNCATE environment_qualification_executions CASCADE")
			var e *pgconn.PgError
			if !errors.As(err, &e) || e.ConstraintName != "clone_checkpoint_execution_retained" {
				t.Fatal("erased execution frames", err)
			}
		})
	}
}

func TestPgCloneWorkloadRetirementChecksOrphanExecutionAndEnvironment(t *testing.T) {
	for _, match := range []string{"deployment", "environment", "unrelated"} {
		t.Run(match, func(t *testing.T) {
			s, pool, l := pgWorkloadRetirementFixture(t, "deployed")
			ctx := t.Context()
			in, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
			if err != nil {
				t.Fatal(err)
			}
			// A frame survives lost parent metadata. An unrelated frame is not authority
			// or a borrower of this original clone. No actual native resource is created.
			fake := state.Instance{ID: uuid.NewString(), AppID: in.Plan.Workloads[0].AppID, DeploymentID: in.Plan.Workloads[0].TargetDeploymentID, NodeID: uuid.NewString(), WakeID: uuid.NewString(), RAMMB: 128}
			p := in.Plan
			if match != "deployment" {
				fake.DeploymentID = uuid.NewString()
			}
			if match == "unrelated" {
				p.EnvironmentID = uuid.NewString()
			}
			frame := seedCloneQualificationExecution(t, pool, fake, p, true)
			in, _, err = s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
			if err != nil {
				t.Fatal(err)
			}
			err = s.FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l, pgWorkloadRetirementProof(in.Plan))
			if match == "unrelated" {
				if err != nil {
					t.Fatal("unrelated frame blocked closure", err)
				}
				return
			}
			if !errors.Is(err, state.ErrConflict) {
				t.Fatal("orphan execution accepted", err)
			}
			saved, err := s.ProjectEnvironmentCloneCheckpointWorkloadRetirementForLease(ctx, l)
			if err != nil || saved.State != "requested" {
				t.Fatal(saved, err)
			}
			// Missing instance metadata cannot erase a dispatched cleanup reservation.
			status, err := s.EnvironmentQualificationExecution(ctx, frame.InstanceID)
			if err != nil || !status.DispatchStarted || status.RetiredAt != nil {
				t.Fatal("orphan frame lost", status, err)
			}
		})
	}
}

func TestPgCloneQualificationFenceMigrationRetention(t *testing.T) {
	s, pool, l := pgWorkloadRetirementFixture(t, "unmaterialized")
	ctx := t.Context()
	raw, err := migrations.FS.ReadFile("20261011232006421_clone_qualification_dispatch_fences.sql")
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
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, parts[1])
	var e *pgconn.PgError
	if !errors.As(err, &e) || e.ConstraintName != "clone_checkpoint_execution_fence_down_no_evidence" {
		t.Fatal("unsafe downgrade", err)
	}
}

// Qualification rows are written independently of the instance table during
// dispatch. Verify the common claim fence blocks that writer through commit.
func TestPgCloneQualificationFenceSerializesDispatchWriters(t *testing.T) {
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
	seedCloneQualificationExecution(t, pool, ins, in.Plan, false)
	old, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = old.Rollback(ctx) }()
	if _, err = old.Exec(ctx, "SELECT frame FROM environment_qualification_executions"); err != nil {
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
		_, e := pool.Exec(ctx, "UPDATE environment_qualification_executions SET dispatch_started=true WHERE instance_id=$1", ins.ID)
		done <- e
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err = pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'UPDATE environment_qualification_executions SET dispatch_started=true%')").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("dispatch did not queue")
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
		t.Fatal("dispatch did not finish")
	}
	_, err = old.Exec(ctx, "UPDATE environment_qualification_executions SET dispatch_started=true WHERE instance_id=$1", ins.ID)
	var e *pgconn.PgError
	if !errors.As(err, &e) || e.Code != "40001" {
		t.Fatal("stale snapshot dispatched", err)
	}
}

func TestPgCloneQualificationCleanupAndClaimLockOrder(t *testing.T) {
	s, pool, l := pgWorkloadRetirementFixture(t, "deployed")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
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
	frame := seedCloneQualificationExecution(t, pool, ins, in.Plan, false)
	gate, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Release()
	if _, err = gate.Exec(ctx, "SELECT pg_advisory_lock(590,hashtext($1))", ins.ID); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = gate.Exec(context.Background(), "SELECT pg_advisory_unlock(590,hashtext($1))", ins.ID) }()
	if _, err = pool.Exec(ctx, `CREATE FUNCTION pause_clone_qualification_cleanup() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF NEW.retired_at IS NOT NULL THEN PERFORM pg_advisory_xact_lock(590,hashtext(NEW.instance_id::text)); END IF; RETURN NEW; END $$;
 CREATE TRIGGER pause_clone_qualification_cleanup BEFORE UPDATE ON environment_qualification_executions
 FOR EACH ROW EXECUTE FUNCTION pause_clone_qualification_cleanup();`); err != nil {
		t.Fatal(err)
	}
	cleaned, claimed := make(chan error, 1), make(chan error, 1)
	go func() {
		cleaned <- s.RetireEnvironmentQualificationExecution(ctx, frame, state.EnvironmentQualificationRetirement{Kind: state.QualificationNeverDispatched})
	}()
	waitQuery := func(query string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			var waiting bool
			if err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE $1)", query).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("writer did not reach gate", query)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitQuery("%UPDATE environment_qualification_executions SET retirement=%")
	go func() {
		_, dispatch, e := s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
		if e == nil && !dispatch {
			e = state.ErrConflict
		}
		claimed <- e
	}()
	waitQuery("%lock_clone_checkpoint_scheduler_admission%")
	if _, err = gate.Exec(ctx, "SELECT pg_advisory_unlock(590,hashtext($1))", ins.ID); err != nil {
		t.Fatal(err)
	}
	for _, result := range []<-chan error{cleaned, claimed} {
		select {
		case err = <-result:
			if err != nil {
				t.Fatal("cleanup/claim failed", err)
			}
		case <-ctx.Done():
			t.Fatal("cleanup/claim did not finish", ctx.Err())
		}
	}
	status, err := s.EnvironmentQualificationExecution(ctx, ins.ID)
	if err != nil || status.RetiredAt == nil {
		t.Fatal("cleanup did not settle", status, err)
	}
	stopped, err := s.InstanceByID(ctx, ins.ID)
	if err != nil || stopped.State != string(state.StateStopped) {
		t.Fatal("instance did not stop", stopped, err)
	}
}
