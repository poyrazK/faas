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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state"
)

const cloneCompensationReceiptMigration = "20261011232006413_clone_terminal_compensation_receipts.sql"

func terminalCompensationFixture(t *testing.T) (*state.PgStore, *pgxpool.Pool, state.ProjectEnvironmentCloneLease) {
	t.Helper()
	return terminalCompensationFixtureForPhase(t, true)
}

func terminalCompensationFixtureForPhase(t *testing.T, cleanEnvironment bool) (*state.PgStore, *pgxpool.Pool, state.ProjectEnvironmentCloneLease) {
	t.Helper()
	// Reuse the original owned, unpublished deployment and native proof chain.
	s, pool, l, _ := deploymentCleanupFixture(t)
	if err := s.CleanupProjectEnvironmentCloneDeploymentsForLease(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	if cleanEnvironment {
		if err := s.CleanupProjectEnvironmentCloneEnvironmentForLease(t.Context(), l); err != nil {
			t.Fatal(err)
		}
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
	return s, pool, l
}

func TestPgCloneTerminalCompensationCommitAndReplay(t *testing.T) {
	s, conn, l := terminalCompensationFixture(t)
	before, err := s.ProjectEnvironmentCloneOperationByID(t.Context(), l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := s.CompleteProjectEnvironmentCloneCheckpointCompensation(t.Context(), l)
	if err != nil || len(receipt.SHA256) != 64 || receipt.CompletedAt.IsZero() || len(receipt.Report.Blockers) != 0 {
		t.Fatal(receipt, err)
	}
	after, err := s.ProjectEnvironmentCloneOperationByID(t.Context(), l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != state.CloneOperationCompensated || after.Revision != before.Revision+1 || !reflect.DeepEqual(after.Resources, before.Resources) || after.ErrorCode != before.ErrorCode {
		t.Fatal("terminal commit changed inventory", before, after)
	}
	var cleared bool
	if err = conn.QueryRow(t.Context(), "SELECT lease_token IS NULL AND lease_until IS NULL FROM project_environment_clone_operations WHERE id=$1", l.Operation.ID).Scan(&cleared); err != nil || !cleared {
		t.Fatal("lease retained", err)
	}
	l.ExpiresAt = time.Now().Add(-time.Hour)
	replay, err := s.CompleteProjectEnvironmentCloneCheckpointCompensation(t.Context(), l)
	if err != nil || !reflect.DeepEqual(receipt, replay) {
		t.Fatal("lost-response retry changed receipt", replay, err)
	}
	for _, fault := range []string{"token", "revision", "account", "project"} {
		bad := l
		switch fault {
		case "token":
			bad.Token = uuid.NewString()
		case "revision":
			bad.Operation.Revision++
		case "account":
			bad.Operation.AccountID = uuid.NewString()
		case "project":
			bad.Operation.ProjectID = uuid.NewString()
		}
		got, e := s.CompleteProjectEnvironmentCloneCheckpointCompensation(t.Context(), bad)
		if e == nil || !reflect.DeepEqual(got, state.ProjectEnvironmentCloneCompensationReceipt{}) {
			t.Fatal("foreign replay", fault, got, e)
		}
	}
	if _, err = s.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("terminal operation reclaimed", err)
	}
	l.ExpiresAt = time.Now().Add(time.Minute)
	// A receipt is historical evidence. It never makes ordinary native replay
	// eligible again after its worker lease has been cleared.
	if _, dispatch, err := s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l); !errors.Is(err, state.ErrConflict) || dispatch {
		t.Fatal("terminal receipt redispatched native work", err)
	}
	for _, table := range []string{"project_environment_clone_environment_cleanups", "project_environment_clone_deployment_cleanups", "project_environment_clone_checkpoint_capture_retirements"} {
		_, err = conn.Exec(t.Context(), "DELETE FROM "+table+" WHERE operation_id=$1", l.Operation.ID)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "23503" {
			t.Fatal("terminal predecessor removed", table, err)
		}
	}
	raw, err := migrations.FS.ReadFile(cloneCompensationReceiptMigration)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := conn.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(t.Context(), strings.SplitN(string(raw), "-- +goose Down", 2)[1])
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.ConstraintName != "clone_compensation_receipt_down_no_evidence" {
		t.Fatal("terminal evidence downgraded", err)
	}
	_ = tx.Rollback(t.Context())
}

func TestPgCloneTerminalCompensationRejectsInvalidClosure(t *testing.T) {
	for _, fault := range []string{"incomplete", "stale", "expired", "late_borrower", "changed_receipt", "write_failure"} {
		t.Run(fault, func(t *testing.T) {
			s, conn, l := terminalCompensationFixtureForPhase(t, fault != "incomplete")
			before, err := s.ProjectEnvironmentCloneOperationByID(t.Context(), l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "stale":
				l.Token = uuid.NewString()
			case "expired":
				l.ExpiresAt = time.Now().Add(-time.Second)
			case "late_borrower":
				_, err = conn.Exec(t.Context(), "INSERT INTO deployment_audit(deployment_id,kind,actor) SELECT target_deployment_id,'deploy.created','late' FROM project_environment_clone_workloads WHERE operation_id=$1", l.Operation.ID)
			case "changed_receipt":
				_, err = conn.Exec(t.Context(), "UPDATE project_environment_clone_environment_cleanups SET workload_cleanup_sha256=$2 WHERE operation_id=$1", l.Operation.ID, strings.Repeat("a", 64))
			case "write_failure":
				_, err = conn.Exec(t.Context(), `CREATE FUNCTION reject_terminal_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.status='compensated' THEN RAISE EXCEPTION 'test terminal failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_terminal_commit BEFORE UPDATE ON project_environment_clone_operations FOR EACH ROW EXECUTE FUNCTION reject_terminal_commit();`)
			}
			if err != nil {
				t.Fatal(err)
			}
			receipt, err := s.CompleteProjectEnvironmentCloneCheckpointCompensation(t.Context(), l)
			if err == nil || !reflect.DeepEqual(receipt, state.ProjectEnvironmentCloneCompensationReceipt{}) {
				t.Fatal("invalid completion", fault, receipt, err)
			}
			after, err := s.ProjectEnvironmentCloneOperationByID(t.Context(), l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("failed completion mutated operation", err)
			}
			var n int
			if err = conn.QueryRow(t.Context(), "SELECT count(*) FROM project_environment_clone_compensation_receipts WHERE operation_id=$1", l.Operation.ID).Scan(&n); err != nil || n != 0 {
				t.Fatal("failed completion retained receipt", n, err)
			}
		})
	}
}

func TestPgCloneTerminalCompensationRejectsTamperedReplay(t *testing.T) {
	for _, fault := range []string{"hash", "report", "token", "time", "inventory"} {
		t.Run(fault, func(t *testing.T) {
			s, conn, l := terminalCompensationFixture(t)
			if _, err := s.CompleteProjectEnvironmentCloneCheckpointCompensation(t.Context(), l); err != nil {
				t.Fatal(err)
			}
			var err error
			switch fault {
			case "hash":
				_, err = conn.Exec(t.Context(), "UPDATE project_environment_clone_compensation_receipts SET receipt_sha256=$2 WHERE operation_id=$1", l.Operation.ID, strings.Repeat("a", 64))
			case "report":
				_, err = conn.Exec(t.Context(), `UPDATE project_environment_clone_compensation_receipts SET report=jsonb_set(report,'{WorkloadClosureSHA256}',to_jsonb($2::text)) WHERE operation_id=$1`, l.Operation.ID, strings.Repeat("a", 64))
			case "token":
				_, err = conn.Exec(t.Context(), "UPDATE project_environment_clone_compensation_receipts SET worker_token_sha256=$2 WHERE operation_id=$1", l.Operation.ID, strings.Repeat("a", 64))
			case "time":
				_, err = conn.Exec(t.Context(), "UPDATE project_environment_clone_compensation_receipts SET completed_at=clock_timestamp()+interval '1 day' WHERE operation_id=$1", l.Operation.ID)
			case "inventory":
				raw, _ := json.Marshal(append(l.Operation.Resources, state.ProjectEnvironmentCloneResource{Kind: "future_resource"}))
				_, err = conn.Exec(t.Context(), "UPDATE project_environment_clone_operations SET resources=$2 WHERE id=$1", l.Operation.ID, raw)
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.CompleteProjectEnvironmentCloneCheckpointCompensation(t.Context(), l)
			if !errors.Is(err, state.ErrConflict) || !reflect.DeepEqual(got, state.ProjectEnvironmentCloneCompensationReceipt{}) {
				t.Fatal("tampered receipt replay", fault, got, err)
			}
		})
	}
}

func dropEmptyCloneCompensationReceiptForParentMigration(t *testing.T, tx pgx.Tx) {
	t.Helper()
	var present bool
	if err := tx.QueryRow(t.Context(), "SELECT to_regclass('project_environment_clone_compensation_receipts') IS NOT NULL").Scan(&present); err != nil {
		t.Fatal(err)
	}
	if !present {
		return
	}
	raw, err := migrations.FS.ReadFile(cloneCompensationReceiptMigration)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), strings.SplitN(string(raw), "-- +goose Down", 2)[1]); err != nil {
		t.Fatal("terminal receipt child downgrade", err)
	}
}

func TestPgCloneTerminalCompensationEmptyMigrationRoundTrip(t *testing.T) {
	_, pool, _, _ := configurationCleanupFixture(t, true)
	raw, err := migrations.FS.ReadFile(cloneCompensationReceiptMigration)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	for _, sql := range []string{parts[1], parts[0], parts[1], parts[0]} {
		if _, err = tx.Exec(t.Context(), sql); err != nil {
			t.Fatal("terminal migration round trip", err)
		}
	}
}

func TestPgCloneTerminalCompensationFencesThroughCommit(t *testing.T) {
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
	if _, err = pool.Exec(ctx, `CREATE FUNCTION pause_terminal_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 PERFORM pg_advisory_xact_lock(59012222); RETURN NEW; END $$;
 CREATE TRIGGER pause_terminal_receipt BEFORE INSERT ON project_environment_clone_compensation_receipts FOR EACH ROW EXECUTE FUNCTION pause_terminal_receipt();`); err != nil {
		t.Fatal(err)
	}
	type result struct {
		report state.ProjectEnvironmentCloneCheckpointCompensationReconciliation
		err    error
	}
	done := make(chan result, 1)
	go func() {
		receipt, e := s.CompleteProjectEnvironmentCloneCheckpointCompensation(ctx, l)
		done <- result{receipt.Report, e}
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
	// After commit new soft references do not rewrite historical completion or
	// grant another worker authority to delete them.
	if _, err = pool.Exec(ctx, "INSERT INTO deployment_audit(deployment_id,kind,actor) VALUES($1,'deploy.created','late')", native.Plan.Workloads[0].TargetDeploymentID); err != nil {
		t.Fatal(err)
	}
	receipt, err := s.CompleteProjectEnvironmentCloneCheckpointCompensation(ctx, l)
	if err != nil || len(receipt.SHA256) != 64 {
		t.Fatal("historical replay changed after new borrower", receipt, err)
	}
}
