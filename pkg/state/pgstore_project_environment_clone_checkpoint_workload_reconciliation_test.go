//go:build !no_pg

// adr: 590
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgCheckpointCompensationReconciliationWorkloadCleanup(t *testing.T) {
	for _, phase := range []string{"retired", "configuration", "workload", "deployment", "environment"} {
		t.Run(phase, func(t *testing.T) {
			s, pool, l, _ := configurationCleanupFixture(t, true)
			if _, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l); err != nil {
				t.Fatal(err)
			}
			retireConfigurationCleanupFixture(t, s, l)
			if _, err := s.ReserveProjectEnvironmentCloneCheckpointCaptureRetirement(t.Context(), l); err != nil {
				t.Fatal(err)
			}
			capture, dispatch, err := s.ClaimProjectEnvironmentCloneCheckpointCaptureRetirement(t.Context(), l)
			if err != nil || !dispatch {
				t.Fatal(err)
			}
			if err = s.FinishProjectEnvironmentCloneCheckpointCaptureRetirement(t.Context(), l, pgCaptureRetirementProof(t, capture)); err != nil {
				t.Fatal(err)
			}
			cleanups := []func() error{
				func() error { return s.CleanupProjectEnvironmentCloneConfigurationForLease(t.Context(), l) },
				func() error { return s.CleanupProjectEnvironmentCloneWorkloadsForLease(t.Context(), l) },
				func() error { return s.CleanupProjectEnvironmentCloneDeploymentsForLease(t.Context(), l) },
				func() error { return s.CleanupProjectEnvironmentCloneEnvironmentForLease(t.Context(), l) },
			}
			n := map[string]int{"retired": 0, "configuration": 1, "workload": 2, "deployment": 3, "environment": 4}[phase]
			for _, cleanup := range cleanups[:n] {
				if err = cleanup(); err != nil {
					t.Fatal(err)
				}
			}
			before, err := s.ProjectEnvironmentCloneOperationByID(t.Context(), l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID)
			if err != nil {
				t.Fatal(err)
			}
			report, err := s.ReconcileProjectEnvironmentCloneCheckpointCompensation(t.Context(), l)
			if err != nil {
				t.Fatal(err)
			}
			if report.OperationID != l.Operation.ID || report.Revision != l.Operation.Revision || len(report.EnvironmentID) != 36 || len(report.OwnershipSHA256) != 64 || len(report.NativePlanSHA256) != 64 || len(report.NativeRetirementSHA256) != 64 {
				t.Fatal("original authority omitted", report)
			}
			hashes := []string{report.ConfigurationCleanupSHA256, report.WorkloadCleanupSHA256, report.DeploymentCleanupSHA256, report.EnvironmentCleanupSHA256}
			for i, hash := range hashes {
				if (len(hash) == 64) != (i < n) {
					t.Fatal("cleanup evidence invented or omitted", i, report)
				}
			}
			missing := 0
			workloadBlocked := false
			for _, b := range report.Blockers {
				if b.Kind == "stage_cleanup" {
					missing++
				}
				if b.Reason == "workload_configuration_retirement_unverified" {
					workloadBlocked = true
				}
			}
			if missing != 4-n || workloadBlocked != (n < 4) || (len(report.WorkloadClosureSHA256) == 64) != (n == 4) {
				t.Fatal("partial cleanup bypassed closure", report)
			}
			replay, err := s.ReconcileProjectEnvironmentCloneCheckpointCompensation(t.Context(), l)
			if err != nil || !reflect.DeepEqual(report, replay) {
				t.Fatal("replay changed report", replay, err)
			}
			after, err := s.ProjectEnvironmentCloneOperationByID(t.Context(), l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("report mutated operation", err)
			}
			if n != 4 {
				return
			}
			if len(report.Blockers) != 0 {
				t.Fatal("original cleanup still blocked", report)
			}
			// Reconciliation cannot replace the original captured inventory with
			// arbitrary compensated labels to open the generic terminal guard.
			resources := append([]state.ProjectEnvironmentCloneResource(nil), l.Operation.Resources...)
			for i := range resources {
				resources[i].Status = "compensated"
			}
			if _, err = s.AdvanceProjectEnvironmentCloneOperation(t.Context(), l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID, state.CloneOperationCompensating, state.CloneOperationCompensated, l.Operation.Revision, resources, ""); !errors.Is(err, state.ErrConflict) {
				t.Fatal("report enabled generic completion", err)
			}
			old := l
			if _, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_operations SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1", l.Operation.ID); err != nil {
				t.Fatal(err)
			}
			l, err = s.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if stale, e := s.ReconcileProjectEnvironmentCloneCheckpointCompensation(t.Context(), old); !errors.Is(e, state.ErrConflict) || !reflect.DeepEqual(stale, state.ProjectEnvironmentCloneCheckpointCompensationReconciliation{}) {
				t.Fatal("stale report", stale, e)
			}
			handedOff, err := s.ReconcileProjectEnvironmentCloneCheckpointCompensation(t.Context(), l)
			report.Revision = l.Operation.Revision
			if err != nil || !reflect.DeepEqual(report, handedOff) {
				t.Fatal("handoff changed proof", handedOff, err)
			}
			// Changing the original adopted inventory invalidates its digest,
			// even after all cleanup receipts authenticate.
			resources = append(l.Operation.Resources, state.ProjectEnvironmentCloneResource{Kind: "future_resource", Name: "unknown", Status: "compensated"})
			raw, err := json.Marshal(resources)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_operations SET resources=$2 WHERE id=$1", l.Operation.ID, raw); err != nil {
				t.Fatal(err)
			}
			changed, err := s.ReconcileProjectEnvironmentCloneCheckpointCompensation(t.Context(), l)
			if !errors.Is(err, state.ErrConflict) || !reflect.DeepEqual(changed, state.ProjectEnvironmentCloneCheckpointCompensationReconciliation{}) {
				t.Fatal("changed adopted inventory returned evidence", changed, err)
			}

		})
	}
}

func TestPgCheckpointCompensationReconciliationRejectsChangedWorkloadClosure(t *testing.T) {
	for _, fault := range []string{"native_missing", "native", "configuration", "workload", "deployment", "environment", "late_soft", "replacement"} {
		t.Run(fault, func(t *testing.T) {
			s, pool, l, native := deploymentCleanupFixture(t)
			if err := s.CleanupProjectEnvironmentCloneDeploymentsForLease(t.Context(), l); err != nil {
				t.Fatal(err)
			}
			if err := s.CleanupProjectEnvironmentCloneEnvironmentForLease(t.Context(), l); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ReserveProjectEnvironmentCloneCheckpointCaptureRetirement(t.Context(), l); err != nil {
				t.Fatal(err)
			}
			capture, dispatch, err := s.ClaimProjectEnvironmentCloneCheckpointCaptureRetirement(t.Context(), l)
			if err != nil || !dispatch {
				t.Fatal(err)
			}
			if err = s.FinishProjectEnvironmentCloneCheckpointCaptureRetirement(t.Context(), l, pgCaptureRetirementProof(t, capture)); err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "native_missing":
				_, err = pool.Exec(t.Context(), "DELETE FROM project_environment_clone_checkpoint_workload_retirements WHERE operation_id=$1", l.Operation.ID)
				var pgerr *pgconn.PgError
				if !errors.As(err, &pgerr) || pgerr.ConstraintName != "clone_checkpoint_scheduler_fence_retained" {
					t.Fatal("native predecessor deleted", err)
				}
				retained, e := s.ReconcileProjectEnvironmentCloneCheckpointCompensation(t.Context(), l)
				if e != nil || len(retained.WorkloadClosureSHA256) != 64 {
					t.Fatal("rejected predecessor deletion damaged proof", retained, e)
				}
				return
			case "native":
				_, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_checkpoint_workload_retirements SET observation_sha256=$2 WHERE operation_id=$1", l.Operation.ID, strings.Repeat("a", 64))
			case "configuration":
				_, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_configuration_cleanups SET manifest_sha256=$2 WHERE operation_id=$1", l.Operation.ID, strings.Repeat("a", 64))
			case "workload":
				_, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_workload_cleanups SET configuration_cleanup_sha256=$2 WHERE operation_id=$1", l.Operation.ID, strings.Repeat("a", 64))
			case "deployment":
				_, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_deployment_cleanups SET native_plan_sha256=$2 WHERE operation_id=$1", l.Operation.ID, strings.Repeat("a", 64))
			case "environment":
				_, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_environment_cleanups SET workload_cleanup_sha256=$2 WHERE operation_id=$1", l.Operation.ID, strings.Repeat("a", 64))
			case "late_soft":
				_, err = pool.Exec(t.Context(), "INSERT INTO deployment_audit(deployment_id,kind,actor) VALUES($1,'deploy.created','late')", native.Plan.Workloads[0].TargetDeploymentID)
			case "replacement":
				_, err = pool.Exec(t.Context(), "INSERT INTO project_environments(id,account_id,project_id,slug) VALUES($1,$2,$3,$4)", uuid.NewString(), l.Operation.AccountID, l.Operation.ProjectID, l.Operation.TargetEnvironment)
			}
			if err != nil {
				t.Fatal(err)
			}
			report, err := s.ReconcileProjectEnvironmentCloneCheckpointCompensation(t.Context(), l)
			if !errors.Is(err, state.ErrConflict) || !reflect.DeepEqual(report, state.ProjectEnvironmentCloneCheckpointCompensationReconciliation{}) {
				t.Fatal("partial authority after changed proof", fault, report, err)
			}
		})
	}
}

// Pause after both writer fences, before any reconciliation evidence is read.
// Scope-only references need this fence even after their original environment
// and deployments have been deleted; row/FK locks alone cannot protect them.
func TestPgCheckpointCompensationReconciliationFencesBorrowers(t *testing.T) {
	s, pool, l, native := deploymentCleanupFixture(t)
	if err := s.CleanupProjectEnvironmentCloneDeploymentsForLease(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	if err := s.CleanupProjectEnvironmentCloneEnvironmentForLease(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveProjectEnvironmentCloneCheckpointCaptureRetirement(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	capture, dispatch, err := s.ClaimProjectEnvironmentCloneCheckpointCaptureRetirement(t.Context(), l)
	if err != nil || !dispatch {
		t.Fatal(err)
	}
	if err = s.FinishProjectEnvironmentCloneCheckpointCaptureRetirement(t.Context(), l, pgCaptureRetirementProof(t, capture)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	gate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gate.Rollback(context.WithoutCancel(ctx)) }()
	var gatePID int32
	if err = gate.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&gatePID); err != nil {
		t.Fatal(err)
	}
	if _, err = gate.Exec(ctx, "SELECT pg_advisory_xact_lock(59012222)"); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `ALTER FUNCTION lock_clone_environment_cleanup_scope_borrowers() RENAME TO original_reconciliation_scope_fence;
 CREATE FUNCTION lock_clone_environment_cleanup_scope_borrowers() RETURNS void LANGUAGE plpgsql AS $$ BEGIN
 PERFORM original_reconciliation_scope_fence();
 PERFORM pg_advisory_xact_lock(59012222);
 END $$;`); err != nil {
		t.Fatal(err)
	}
	type result struct {
		report state.ProjectEnvironmentCloneCheckpointCompensationReconciliation
		err    error
	}
	done := make(chan result, 1)
	go func() {
		report, e := s.ReconcileProjectEnvironmentCloneCheckpointCompensation(ctx, l)
		done <- result{report, e}
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		if err = pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1=ANY(pg_blocking_pids(pid)))", gatePID).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case r := <-done:
			t.Fatal("reconciliation missed fence", r.err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	for _, borrower := range []string{"scope", "deployment", "partition"} {
		tx, e := pool.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(ctx, "SET LOCAL lock_timeout='150ms'"); e != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(e)
		}
		switch borrower {
		case "scope":
			_, e = tx.Exec(ctx, "INSERT INTO queue_bindings(account_id,app_id,name,queue_name,deployment_scope,environment_id) VALUES($1,$2,'racing','racing',$3,$4)", l.Operation.AccountID, native.Plan.Workloads[0].AppID, l.Operation.TargetEnvironment, native.Plan.EnvironmentID)
		case "deployment":
			_, e = tx.Exec(ctx, "INSERT INTO deployment_audit(deployment_id,kind,actor) VALUES($1,'deploy.created','racing')", native.Plan.Workloads[0].TargetDeploymentID)
		case "partition":
			_, e = tx.Exec(ctx, "INSERT INTO log_events_default(occurred_at,account_id,app_id,deployment_id,source,message) VALUES('2000-01-01',$1,$2,$3,'runtime','racing')", l.Operation.AccountID, native.Plan.Workloads[0].AppID, native.Plan.Workloads[0].TargetDeploymentID)
		}
		_ = tx.Rollback(ctx)
		var pgerr *pgconn.PgError
		if !errors.As(e, &pgerr) || pgerr.Code != "55P03" {
			t.Fatal("borrower entered reconciliation window", borrower, e)
		}
	}
	if err = gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.err != nil || len(r.report.Blockers) != 0 || len(r.report.WorkloadClosureSHA256) != 64 {
			t.Fatal("fenced closure failed", r.report, r.err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// Commit releases the fences: a new soft borrower can enter, but a later
	// reconciliation must reject it rather than reuse the earlier report.
	if _, err = pool.Exec(ctx, "INSERT INTO deployment_audit(deployment_id,kind,actor) VALUES($1,'deploy.created','late')", native.Plan.Workloads[0].TargetDeploymentID); err != nil {
		t.Fatal(err)
	}
	report, err := s.ReconcileProjectEnvironmentCloneCheckpointCompensation(ctx, l)
	if !errors.Is(err, state.ErrConflict) || !reflect.DeepEqual(report, state.ProjectEnvironmentCloneCheckpointCompensationReconciliation{}) {
		t.Fatal("late borrower reused closure", report, err)
	}
}
