//go:build !no_pg

// adr: 590
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const cloneDeploymentCleanupMigration = "20261011232006411_clone_deployment_cleanup_receipts.sql"

func deploymentCleanupFixture(t *testing.T) (*state.PgStore, *pgxpool.Pool, state.ProjectEnvironmentCloneLease, state.ProjectEnvironmentCloneCheckpointWorkloadRetirement) {
	t.Helper()
	s, pool, l, _ := configurationCleanupFixture(t, true)
	native, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l)
	if err != nil {
		t.Fatal(err)
	}
	retireConfigurationCleanupFixture(t, s, l)
	if err = s.CleanupProjectEnvironmentCloneConfigurationForLease(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	if err = s.CleanupProjectEnvironmentCloneWorkloadsForLease(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	return s, pool, l, native
}

func TestPgCloneDeploymentCleanupAtomicRecovery(t *testing.T) {
	for _, fault := range []string{"none", "write", "foreign_key", "soft", "partition", "drain_plan", "stale", "artifact", "sidecar", "runtime_owner", "missing_workload", "other_capture"} {
		t.Run(fault, func(t *testing.T) {
			s, pool, l, native := deploymentCleanupFixture(t)
			id := native.Plan.Workloads[0].TargetDeploymentID
			var sourceBefore []byte
			if err := pool.QueryRow(t.Context(), "SELECT to_jsonb(d) FROM deployments d WHERE id=$1", native.Plan.Workloads[0].SourceDeploymentID).Scan(&sourceBefore); err != nil {
				t.Fatal(err)
			}
			var err error
			switch fault {
			case "write":
				_, err = pool.Exec(t.Context(), `CREATE FUNCTION reject_deployment_cleanup() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected receipt failure' USING ERRCODE='40001'; END $$; CREATE TRIGGER reject_deployment_cleanup BEFORE INSERT ON project_environment_clone_deployment_cleanups FOR EACH ROW EXECUTE FUNCTION reject_deployment_cleanup();`)
			case "foreign_key":
				_, err = pool.Exec(t.Context(), "INSERT INTO deployment_logs(deployment_id,seq,line) VALUES($1,987654321,'retained')", id)
			case "soft":
				_, err = pool.Exec(t.Context(), "INSERT INTO deployment_audit(deployment_id,kind,actor) VALUES($1,'deploy.created','retained')", id)
			case "partition":
				_, err = pool.Exec(t.Context(), "INSERT INTO log_events(occurred_at,account_id,app_id,deployment_id,source,message) VALUES('2000-01-01',$1,$2,$3,'runtime','retained')", l.Operation.AccountID, native.Plan.Workloads[0].AppID, id)
			case "drain_plan":
				_, err = pool.Exec(t.Context(), drainPlanDeploymentBorrowerSQL, id)
			case "stale":
				l.Token = uuid.NewString()
			case "artifact":
				_, err = pool.Exec(t.Context(), "UPDATE deployments SET reason='replacement' WHERE id=$1", id)
				requireCloneSchedulerFence(t, err)
				// Rejected relabelling preserves the original cleanup authority.
				err = nil
				fault = "none"
			case "sidecar":
				_, err = pool.Exec(t.Context(), "INSERT INTO deployment_sidecar_layers(deployment_id,sidecar_name,storage_key,bytes,content_digest) VALUES($1,'foreign','foreign',1,'sha256:foreign')", id)
			case "runtime_owner":
				_, err = pool.Exec(t.Context(), "UPDATE deployment_runtime_environment_owners SET environment_id=$2 WHERE deployment_id=$1", id, uuid.NewString())
			case "missing_workload":
				_, err = pool.Exec(t.Context(), "DELETE FROM project_environment_clone_workload_cleanups WHERE operation_id=$1", l.Operation.ID)
			case "other_capture":
				// A second frozen source reference remains a borrower, regardless of that
				// operation's tenant metadata or terminal status.
				_, err = pool.Exec(t.Context(), `INSERT INTO project_environment_clone_workloads SELECT (jsonb_populate_record(NULL::project_environment_clone_workloads,to_jsonb(w)||jsonb_build_object('operation_id',(SELECT id FROM project_environment_clone_operations WHERE id<>$1 LIMIT 1),'source_deployment_id',$2::text,'target_deployment_id',NULL,'target_settings_hash',''))).* FROM project_environment_clone_workloads w WHERE operation_id=$1`, l.Operation.ID, id)
			}
			if err != nil {
				t.Fatal(err)
			}
			// Ensure the fixture actually has another captured workload for this case.
			if fault == "other_capture" {
				var n int
				if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM project_environment_clone_workloads WHERE operation_id<>$1 AND source_deployment_id=$2", l.Operation.ID, id).Scan(&n); err != nil || n == 0 {
					t.Fatal("missing foreign capture fixture", n, err)
				}
			}
			err = s.CleanupProjectEnvironmentCloneDeploymentsForLease(t.Context(), l)
			if fault == "none" && err != nil {
				t.Fatal("cleanup", err)
			}
			if fault != "none" && err == nil {
				t.Fatal("unsafe deployment removal committed")
			}
			var live, owner, receipt bool
			if err = pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM deployments WHERE id=$1),EXISTS(SELECT 1 FROM deployment_runtime_environment_owners WHERE deployment_id=$1),EXISTS(SELECT 1 FROM project_environment_clone_deployment_cleanups WHERE operation_id=$2)", id, l.Operation.ID).Scan(&live, &owner, &receipt); err != nil {
				t.Fatal(err)
			}
			if fault != "none" {
				if !live || !owner || receipt {
					t.Fatal("partial deletion survived", live, owner, receipt)
				}
				return
			}
			if live || owner || !receipt {
				t.Fatal("incomplete deletion", live, owner, receipt)
			}
			if err = s.CleanupProjectEnvironmentCloneDeploymentsForLease(t.Context(), l); err != nil {
				t.Fatal("lost reply", err)
			}
			if err = s.CleanupProjectEnvironmentCloneWorkloadsForLease(t.Context(), l); err != nil {
				t.Fatal("workload replay", err)
			}
			if err = s.CleanupProjectEnvironmentCloneConfigurationForLease(t.Context(), l); err != nil {
				t.Fatal("configuration replay", err)
			}
			replay, dispatch, err := s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l)
			if err != nil || dispatch || !reflect.DeepEqual(replay.Plan, native.Plan) {
				t.Fatal("native replay changed or redispatched", replay, dispatch, err)
			}
			report, err := s.ProjectEnvironmentCloneDeploymentBorrowersForLease(t.Context(), l)
			if err != nil {
				t.Fatal(err)
			}
			for _, counts := range []map[string]int64{report.Deployments[id], report.SoftReferences[id]} {
				for name, n := range counts {
					if n != 0 {
						t.Fatal("retained deployment borrower", name, n)
					}
				}
			}
			var sourceAfter []byte
			if err = pool.QueryRow(t.Context(), "SELECT to_jsonb(d) FROM deployments d WHERE id=$1", native.Plan.Workloads[0].SourceDeploymentID).Scan(&sourceAfter); err != nil || string(sourceBefore) != string(sourceAfter) {
				t.Fatal("source changed", err)
			}
			if err = s.CleanupProjectEnvironmentCloneEnvironmentForLease(t.Context(), l); err != nil {
				t.Fatal("deployed environment cleanup", err)
			}
			if err = s.CleanupProjectEnvironmentCloneDeploymentsForLease(t.Context(), l); err != nil {
				t.Fatal("deployment replay after environment removal", err)
			}
			if _, dispatch, err = s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l); err != nil || dispatch {
				t.Fatal("native replay after environment removal", err)
			}
		})
	}
}

func TestPgCloneDeploymentCleanupPreservesMigrationBuildRecords(t *testing.T) {
	for _, status := range []string{"waiting", "ready", "failed", "public_build_urls"} {
		t.Run(status, func(t *testing.T) {
			s, pool, lease, native := deploymentCleanupFixture(t)
			deploymentID := native.Plan.Workloads[0].TargetDeploymentID
			var constraint, read string
			if status == "public_build_urls" {
				constraint = "deployment_public_build_urls_deployment_id_fkey"
				read = "SELECT to_jsonb(r) FROM deployment_public_build_urls r WHERE deployment_id=$1"
				if _, err := pool.Exec(t.Context(), `INSERT INTO deployment_public_build_urls(deployment_id,urls) VALUES($1,'{"API_URL":"https://captured.example.invalid"}')`, deploymentID); err != nil {
					t.Fatal(err)
				}
			} else {
				constraint = "deployment_build_dependency_gates_deployment_id_fkey"
				read = "SELECT to_jsonb(r) FROM deployment_build_dependency_gates r WHERE deployment_id=$1"
				gate := state.BuildDependencyGate{
					Dependencies: []state.DeploymentDependencyPin{{Service: "api", AppID: uuid.NewString(), DeploymentID: uuid.NewString()}},
					Status:       status,
					DeadlineAt:   time.Now().UTC().Add(api.ProjectMigrationBuildWaitTimeout),
				}
				progress, err := json.Marshal(gate)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(t.Context(), "INSERT INTO deployment_build_dependency_gates(deployment_id,progress) VALUES($1,$2::jsonb)", deploymentID, progress); err != nil {
					t.Fatal(err)
				}
			}
			var before []byte
			if err := pool.QueryRow(t.Context(), read, deploymentID).Scan(&before); err != nil {
				t.Fatal(err)
			}
			report, err := s.ProjectEnvironmentCloneDeploymentBorrowersForLease(t.Context(), lease)
			if err != nil || report.Deployments[deploymentID][constraint] != 1 {
				t.Fatal("migration build record missing from retained references", report, err)
			}
			if err = s.CleanupProjectEnvironmentCloneDeploymentsForLease(t.Context(), lease); !errors.Is(err, state.ErrConflict) || !strings.Contains(err.Error(), constraint) {
				t.Fatal("migration build record did not block deployment removal", err)
			}
			var after []byte
			if err := pool.QueryRow(t.Context(), read, deploymentID).Scan(&after); err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("cleanup changed saved migration build data", err)
			}
			var live, owner, receipt bool
			if err := pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM deployments WHERE id=$1),EXISTS(SELECT 1 FROM deployment_runtime_environment_owners WHERE deployment_id=$1),EXISTS(SELECT 1 FROM project_environment_clone_deployment_cleanups WHERE operation_id=$2)", deploymentID, lease.Operation.ID).Scan(&live, &owner, &receipt); err != nil || !live || !owner || receipt {
				t.Fatal("blocked cleanup changed deployment ownership or committed a receipt", live, owner, receipt, err)
			}
		})
	}
}

func TestPgCloneDeploymentCleanupRejectsChangedEvidence(t *testing.T) {
	for _, fault := range []string{"native_plan_sha256", "native_observation_sha256", "workload_cleanup_sha256", "environment_id", "count", "time", "missing", "replacement", "late_soft", "predecessor", "native"} {
		t.Run(fault, func(t *testing.T) {
			s, pool, l, native := deploymentCleanupFixture(t)
			id := native.Plan.Workloads[0].TargetDeploymentID
			var deployment []byte
			if err := pool.QueryRow(t.Context(), "SELECT to_jsonb(d) FROM deployments d WHERE id=$1", id).Scan(&deployment); err != nil {
				t.Fatal(err)
			}
			if err := s.CleanupProjectEnvironmentCloneDeploymentsForLease(t.Context(), l); err != nil {
				t.Fatal(err)
			}
			var err error
			switch fault {
			case "native_plan_sha256", "native_observation_sha256", "workload_cleanup_sha256":
				_, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_deployment_cleanups SET "+fault+"=$2 WHERE operation_id=$1", l.Operation.ID, strings.Repeat("a", 64))
			case "environment_id":
				_, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_deployment_cleanups SET environment_id=$2 WHERE operation_id=$1", l.Operation.ID, uuid.NewString())
			case "count":
				_, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_deployment_cleanups SET removed_deployments=0 WHERE operation_id=$1", l.Operation.ID)
			case "time":
				_, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_deployment_cleanups SET cleaned_at=cleaned_at-interval '1 day' WHERE operation_id=$1", l.Operation.ID)
			case "missing":
				_, err = pool.Exec(t.Context(), "DELETE FROM project_environment_clone_deployment_cleanups WHERE operation_id=$1", l.Operation.ID)
			case "replacement":
				_, err = pool.Exec(t.Context(), "INSERT INTO deployments SELECT (jsonb_populate_record(NULL::deployments,$1::jsonb)).*", deployment)
				requireCloneSchedulerFence(t, err)
				if err = s.CleanupProjectEnvironmentCloneDeploymentsForLease(t.Context(), l); err != nil {
					t.Fatal("fenced replacement damaged replay", err)
				}
				return
			case "late_soft":
				_, err = pool.Exec(t.Context(), "INSERT INTO deployment_audit(deployment_id,kind,actor) VALUES($1,'deploy.created','late')", id)
			case "predecessor":
				_, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_workload_cleanups SET cleaned_at=cleaned_at+interval '1 microsecond' WHERE operation_id=$1", l.Operation.ID)
			case "native":
				_, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_checkpoint_workload_retirements SET observation_sha256=$2 WHERE operation_id=$1", l.Operation.ID, strings.Repeat("b", 64))
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = s.CleanupProjectEnvironmentCloneDeploymentsForLease(t.Context(), l); !errors.Is(err, state.ErrConflict) {
				t.Fatal("changed receipt accepted", fault, err)
			}
			if _, dispatch, err := s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l); !errors.Is(err, state.ErrConflict) || dispatch {
				t.Fatal("changed evidence granted native replay", fault, err)
			}
			report, err := s.ProjectEnvironmentCloneDeploymentBorrowersForLease(t.Context(), l)
			if !errors.Is(err, state.ErrConflict) || !reflect.DeepEqual(report, state.ProjectEnvironmentCloneDeploymentBorrowers{}) {
				t.Fatal("partial inventory", report, err)
			}
		})
	}
}

func dropEmptyCloneDeploymentCleanupForParentMigration(t *testing.T, tx pgx.Tx) {
	t.Helper()
	dropEmptyCloneCompensationReceiptForParentMigration(t, tx)
	raw, err := migrations.FS.ReadFile(cloneDeploymentCleanupMigration)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), strings.SplitN(string(raw), "-- +goose Down", 2)[1]); err != nil {
		t.Fatal("dependent deployment cleanup downgrade", err)
	}
}

func TestPgCloneDeploymentCleanupMigrationRefusesEvidence(t *testing.T) {
	s, pool, l, _ := deploymentCleanupFixture(t)
	raw, err := migrations.FS.ReadFile(cloneDeploymentCleanupMigration)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	dropEmptyCloneCompensationReceiptForParentMigration(t, tx)
	for _, sql := range []string{parts[1], parts[0], parts[1], parts[0]} {
		if _, err = tx.Exec(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	_ = tx.Rollback(t.Context())
	if err = s.CleanupProjectEnvironmentCloneDeploymentsForLease(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(t.Context(), parts[1])
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.ConstraintName != "clone_deployment_cleanup_down_no_evidence" {
		t.Fatal("evidence downgrade", err)
	}
	_ = tx.Rollback(t.Context())
	_, err = pool.Exec(t.Context(), "DELETE FROM project_environment_clone_workload_cleanups WHERE operation_id=$1", l.Operation.ID)
	if !errors.As(err, &pgerr) || pgerr.Code != "23503" {
		t.Fatal("workload predecessor removed", err)
	}
}

func TestPgCloneDeploymentCleanupBlocksConcurrentBorrowers(t *testing.T) {
	s, pool, l, native := deploymentCleanupFixture(t)
	id := native.Plan.Workloads[0].TargetDeploymentID
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	gate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gate.Rollback(ctx) }()
	var gatePID int32
	if err = gate.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&gatePID); err != nil {
		t.Fatal(err)
	}
	if _, err = gate.Exec(ctx, "SELECT pg_advisory_xact_lock(59011221)"); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `CREATE FUNCTION pause_deployment_cleanup() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(59011221); RETURN NEW; END $$; CREATE TRIGGER pause_deployment_cleanup BEFORE INSERT ON project_environment_clone_deployment_cleanups FOR EACH ROW EXECUTE FUNCTION pause_deployment_cleanup();`); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- s.CleanupProjectEnvironmentCloneDeploymentsForLease(ctx, l) }()
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
		case err = <-result:
			t.Fatal("cleanup missed receipt gate", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	for _, borrower := range []string{"foreign_key", "soft", "soft_partition", "drain_plan", "retirement_journal", "scenario_cold_preparations", "scenario_postgres_faults", "scenario_recorder_sessions", "scenario_suspend_resumes", "scenario_worker_faults", "platform_mail_deployment_observations", "platform_mail_producer_outbox", "replacement"} {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout='150ms'"); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		switch borrower {
		case "foreign_key":
			_, err = tx.Exec(ctx, "INSERT INTO deployment_logs(deployment_id,seq,line) VALUES($1,987654321,'racing')", id)
		case "soft":
			_, err = tx.Exec(ctx, "INSERT INTO deployment_audit(deployment_id,kind,actor) VALUES($1,'deploy.created','racing')", id)
		case "soft_partition":
			// Direct writes to a child bypass the parent's INSERT routing. The
			// writer fence must lock descendants as well as the parent table.
			_, err = tx.Exec(ctx, "INSERT INTO log_events_default(occurred_at,account_id,app_id,deployment_id,source,message) VALUES('2000-01-01',$1,$2,$3,'runtime','racing')", l.Operation.AccountID, native.Plan.Workloads[0].AppID, id)
		case "drain_plan":
			_, err = tx.Exec(ctx, drainPlanDeploymentBorrowerSQL, id)
		case "retirement_journal":
			_, err = tx.Exec(ctx, "LOCK TABLE environment_workload_retirements IN ROW EXCLUSIVE MODE")
		case "scenario_cold_preparations", "scenario_postgres_faults", "scenario_recorder_sessions", "scenario_suspend_resumes", "scenario_worker_faults", "platform_mail_deployment_observations", "platform_mail_producer_outbox":
			// These retained rehearsal receipts have no deployment FK. Every writer
			// must remain fenced until deletion and its receipt commit together.
			_, err = tx.Exec(ctx, "LOCK TABLE "+borrower+" IN ROW EXCLUSIVE MODE")
		case "replacement":
			_, err = tx.Exec(ctx, `INSERT INTO deployments SELECT (jsonb_populate_record(NULL::deployments,to_jsonb(d)||jsonb_build_object('id',$1::text,'scope',$2::text))).* FROM deployments d WHERE d.id=$3`, id, l.Operation.TargetEnvironment, native.Plan.Workloads[0].SourceDeploymentID)
		}
		_ = tx.Rollback(ctx)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "55P03" {
			t.Fatal("borrower entered deletion window", borrower, err)
		}
	}
	if err = gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err = s.CleanupProjectEnvironmentCloneDeploymentsForLease(t.Context(), l); err != nil {
		t.Fatal("replay", err)
	}
}

// Failed rehearsal receipts still retain the original deployment. A source
// receipt must not borrow the clone target or disappear during target cleanup.
func TestPgCloneDeploymentCleanupRetainsRehearsalEvidence(t *testing.T) {
	for _, target := range []bool{false, true} {
		t.Run(fmt.Sprint("target=", target), func(t *testing.T) {
			s, pool, l, native := deploymentCleanupFixture(t)
			workload := native.Plan.Workloads[0]
			deploymentID := workload.SourceDeploymentID
			if target {
				deploymentID = workload.TargetDeploymentID
			}
			var before []byte
			err := pool.QueryRow(t.Context(), `INSERT INTO scenario_worker_faults
 (id,account_id,app_id,run_id,instance_id,deployment_id,invocation_id,checkpoint_attempt_id,recorder_session_id,state)
 VALUES(gen_random_uuid(),$1,$2,repeat('a',32),gen_random_uuid(),$3,gen_random_uuid(),gen_random_uuid(),gen_random_uuid(),'failed')
 RETURNING to_jsonb(scenario_worker_faults)`, l.Operation.AccountID, workload.AppID, deploymentID).Scan(&before)
			if err != nil {
				t.Fatal(err)
			}
			report, err := s.ProjectEnvironmentCloneDeploymentBorrowersForLease(t.Context(), l)
			want := int64(0)
			if target {
				want = 1
			}
			if err != nil || report.SoftReferences[workload.TargetDeploymentID]["scenario_worker_faults.deployment_id"] != want {
				t.Fatal("retained rehearsal evidence miscounted", report, err)
			}
			err = s.CleanupProjectEnvironmentCloneDeploymentsForLease(t.Context(), l)
			if target && !errors.Is(err, state.ErrConflict) || !target && err != nil {
				t.Fatal("rehearsal borrower cleanup", err)
			}
			var live, owner, receipt bool
			if err = pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM deployments WHERE id=$1),EXISTS(SELECT 1 FROM deployment_runtime_environment_owners WHERE deployment_id=$1),EXISTS(SELECT 1 FROM project_environment_clone_deployment_cleanups WHERE operation_id=$2)", workload.TargetDeploymentID, l.Operation.ID).Scan(&live, &owner, &receipt); err != nil {
				t.Fatal(err)
			}
			if live != target || owner != target || receipt == target {
				t.Fatal("partial rehearsal borrower cleanup", live, owner, receipt)
			}
			var after []byte
			if err = pool.QueryRow(t.Context(), "SELECT to_jsonb(f) FROM scenario_worker_faults f WHERE app_id=$1", workload.AppID).Scan(&after); err != nil || string(before) != string(after) {
				t.Fatal("original rehearsal evidence changed", err)
			}
		})
	}
}

// Mail never borrows a deployment (#4972): a deployment-failed observation is a
// permanent skip receipt and would block cleanup forever. Cleanup proceeds and
// leaves the original observation (ADR-20261017155842) and the monotonic scope
// fence intact.
func TestPgCloneDeploymentCleanupRetainsMailEvidence(t *testing.T) {
	for _, target := range []bool{false, true} {
		t.Run(fmt.Sprint("target=", target), func(t *testing.T) {
			s, pool, l, native := deploymentCleanupFixture(t)
			workload := native.Plan.Workloads[0]
			deploymentID := workload.SourceDeploymentID
			if target {
				deploymentID = workload.TargetDeploymentID
			}
			var before []byte
			err := pool.QueryRow(t.Context(), `INSERT INTO platform_mail_deployment_observations
(deployment_id,failure_revision,account_id,disposition) VALUES($1,1,$2,'cooldown')
RETURNING to_jsonb(platform_mail_deployment_observations)`, deploymentID, l.Operation.AccountID).Scan(&before)
			if err != nil {
				t.Fatal(err)
			}
			var revision, success int64
			if err = pool.QueryRow(t.Context(), "SELECT revision,success_revision FROM platform_mail_deployment_fences WHERE app_id=$1 AND scope=$2", workload.AppID, l.Operation.TargetEnvironment).Scan(&revision, &success); err != nil {
				t.Fatal(err)
			}
			report, err := s.ProjectEnvironmentCloneDeploymentBorrowersForLease(t.Context(), l)
			if _, counted := report.SoftReferences[workload.TargetDeploymentID]["platform_mail_deployment_observations.deployment_id"]; err != nil || counted {
				t.Fatal("mail evidence counted as a deployment borrower", report, err)
			}
			if err = s.CleanupProjectEnvironmentCloneDeploymentsForLease(t.Context(), l); err != nil {
				t.Fatal("mail evidence blocked cleanup", err)
			}
			var live, owner, receipt bool
			if err = pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM deployments WHERE id=$1),EXISTS(SELECT 1 FROM deployment_runtime_environment_owners WHERE deployment_id=$1),EXISTS(SELECT 1 FROM project_environment_clone_deployment_cleanups WHERE operation_id=$2)", workload.TargetDeploymentID, l.Operation.ID).Scan(&live, &owner, &receipt); err != nil {
				t.Fatal(err)
			}
			if live || owner || !receipt {
				t.Fatal("mail evidence changed cleanup", live, owner, receipt)
			}
			var after []byte
			if err = pool.QueryRow(t.Context(), "SELECT to_jsonb(o) FROM platform_mail_deployment_observations o WHERE deployment_id=$1", deploymentID).Scan(&after); err != nil || string(before) != string(after) {
				t.Fatal("original mail observation changed", err)
			}
			if err = s.CleanupProjectEnvironmentCloneEnvironmentForLease(t.Context(), l); err != nil {
				t.Fatal("metadata cleanup", err)
			}
			revision++ // The authored deployment DELETE trigger advances its permanent fence.
			var gotRevision, gotSuccess int64
			if err = pool.QueryRow(t.Context(), "SELECT revision,success_revision FROM platform_mail_deployment_fences WHERE app_id=$1 AND scope=$2", workload.AppID, l.Operation.TargetEnvironment).Scan(&gotRevision, &gotSuccess); err != nil || gotRevision != revision || gotSuccess != success {
				t.Fatal("mail fence erased or rewound", gotRevision, revision, gotSuccess, success, err)
			}
		})
	}
}
