//go:build !no_pg

// adr: 590
package state_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgCloneWorkloadCleanupAtomicRecovery(t *testing.T) {
	for _, fault := range []string{"none", "write", "borrower", "stale", "missing_configuration"} {
		t.Run(fault, func(t *testing.T) {
			s, pool, l, _ := configurationCleanupFixture(t, true)
			original, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l)
			if err != nil {
				t.Fatal(err)
			}
			retireConfigurationCleanupFixture(t, s, l)
			if fault != "missing_configuration" {
				if err = s.CleanupProjectEnvironmentCloneConfigurationForLease(t.Context(), l); err != nil {
					t.Fatal(err)
				}
			}
			switch fault {
			case "write":
				_, err = pool.Exec(t.Context(), `CREATE FUNCTION reject_workload_cleanup() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected receipt failure' USING ERRCODE='40001'; END $$; CREATE TRIGGER reject_workload_cleanup BEFORE INSERT ON project_environment_clone_workload_cleanups FOR EACH ROW EXECUTE FUNCTION reject_workload_cleanup();`)
			case "borrower":
				seedCloneWorkloadBorrower(t, pool, l, original.Plan, "cross_environment_queue")
			case "stale":
				l.Token = uuid.NewString()
			}
			if err != nil {
				t.Fatal(err)
			}
			err = s.CleanupProjectEnvironmentCloneWorkloadsForLease(t.Context(), l)
			if fault == "none" && err != nil {
				t.Fatal("workload cleanup", err)
			}
			if fault != "none" && err == nil {
				t.Fatal("unsafe workload removal committed")
			}
			var heads, specs, pins, receipt, environment bool
			if err = pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM project_environment_workload_heads WHERE environment_id=$1),EXISTS(SELECT 1 FROM project_environment_workload_specs WHERE environment_id=$1),EXISTS(SELECT 1 FROM project_environment_workload_deployment_specs WHERE deployment_id=$2),EXISTS(SELECT 1 FROM project_environment_clone_workload_cleanups WHERE operation_id=$3),EXISTS(SELECT 1 FROM project_environments WHERE id=$1)", original.Plan.EnvironmentID, original.Plan.Workloads[0].TargetDeploymentID, l.Operation.ID).Scan(&heads, &specs, &pins, &receipt, &environment); err != nil {
				t.Fatal(err)
			}
			if !environment {
				t.Fatal("environment identity cascaded")
			}
			if fault != "none" {
				if !heads || !specs || !pins || receipt {
					t.Fatal("partial deletion survived")
				}
				return
			}
			if heads || specs || pins || !receipt {
				t.Fatal("workload catalogue incompletely removed")
			}
			if err = s.CleanupProjectEnvironmentCloneWorkloadsForLease(t.Context(), l); err != nil {
				t.Fatal("lost commit reply", err)
			}
			if err = s.CleanupProjectEnvironmentCloneConfigurationForLease(t.Context(), l); err != nil {
				t.Fatal("settings receipt replay", err)
			}
			replay, dispatch, err := s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l)
			if err != nil || dispatch || replay.State != "retired" || state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlanSHA256(replay.Plan) != state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlanSHA256(original.Plan) {
				t.Fatal("native replay lost original authority", err)
			}
			if _, err = s.ProjectEnvironmentCloneWorkloadBorrowersForLease(t.Context(), l); err != nil {
				t.Fatal("post-cleanup inventory", err)
			}
		})
	}
}

func TestPgCloneWorkloadCleanupRejectsChangedEvidence(t *testing.T) {
	for _, fault := range []string{"receipt", "missing", "configuration_receipt", "time", "replacement_spec", "replacement_pin", "artifact"} {
		t.Run(fault, func(t *testing.T) {
			s, pool, l, _ := configurationCleanupFixture(t, true)
			original, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l)
			if err != nil {
				t.Fatal(err)
			}
			var spec []byte
			if err = pool.QueryRow(t.Context(), "SELECT to_jsonb(s) FROM project_environment_workload_specs s WHERE id=$1", original.Plan.Workloads[0].SpecID).Scan(&spec); err != nil {
				t.Fatal(err)
			}
			retireConfigurationCleanupFixture(t, s, l)
			if err = s.CleanupProjectEnvironmentCloneConfigurationForLease(t.Context(), l); err != nil {
				t.Fatal(err)
			}
			if err = s.CleanupProjectEnvironmentCloneWorkloadsForLease(t.Context(), l); err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "receipt":
				_, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_workload_cleanups SET manifest_sha256=$2 WHERE operation_id=$1", l.Operation.ID, strings.Repeat("a", 64))
			case "missing":
				_, err = pool.Exec(t.Context(), "DELETE FROM project_environment_clone_workload_cleanups WHERE operation_id=$1", l.Operation.ID)
			case "configuration_receipt":
				_, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_configuration_cleanups SET cleaned_at=cleaned_at+interval '1 microsecond' WHERE operation_id=$1", l.Operation.ID)
			case "time":
				_, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_workload_cleanups SET cleaned_at=cleaned_at-interval '1 day' WHERE operation_id=$1", l.Operation.ID)
			case "replacement_spec":
				_, err = pool.Exec(t.Context(), "INSERT INTO project_environment_workload_specs SELECT (jsonb_populate_record(NULL::project_environment_workload_specs,$1::jsonb)).*", spec)
			case "replacement_pin":
				envID, specID := uuid.NewString(), uuid.NewString()
				if _, err = pool.Exec(t.Context(), "INSERT INTO project_environments(id,account_id,project_id,slug) VALUES($1,$2,$3,'foreign-pin')", envID, l.Operation.AccountID, l.Operation.ProjectID); err != nil {
					t.Fatal(err)
				}
				if _, err = pool.Exec(t.Context(), "INSERT INTO project_environment_workload_specs(id,environment_id,app_id,revision,config_hash,settings) VALUES($1,$2,$3,1,$4,'{}')", specID, envID, original.Plan.Workloads[0].AppID, original.Plan.Workloads[0].SpecHash); err != nil {
					t.Fatal(err)
				}
				_, err = pool.Exec(t.Context(), "INSERT INTO project_environment_workload_deployment_specs(deployment_id,spec_id) VALUES($1,$2)", original.Plan.Workloads[0].TargetDeploymentID, specID)
				var pgerr *pgconn.PgError
				if !errors.As(err, &pgerr) || pgerr.Code != "P0002" {
					t.Fatal("cross-environment pin write accepted", err)
				}
				if err = s.CleanupProjectEnvironmentCloneWorkloadsForLease(t.Context(), l); err != nil {
					t.Fatal("rejected pin write damaged cleanup replay", err)
				}
				return
			case "artifact":
				_, err = pool.Exec(t.Context(), "UPDATE deployments SET reason='replacement' WHERE id=$1", original.Plan.Workloads[0].TargetDeploymentID)
				// The permanent deployment ownership guard rejects this mutation
				// before it can invalidate the retained cleanup evidence.
				requireCloneSchedulerFence(t, err)
				if err = s.CleanupProjectEnvironmentCloneWorkloadsForLease(t.Context(), l); err != nil {
					t.Fatal("rejected artifact write damaged cleanup replay", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = s.CleanupProjectEnvironmentCloneWorkloadsForLease(t.Context(), l); !errors.Is(err, state.ErrConflict) {
				t.Fatal("changed cleanup accepted", fault, err)
			}
			if _, dispatch, err := s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l); !errors.Is(err, state.ErrConflict) || dispatch {
				t.Fatal("changed cleanup granted native replay", fault, err)
			}
		})
	}
}

func dropEmptyCloneWorkloadCleanupForParentMigration(t *testing.T, tx pgx.Tx) {
	t.Helper()
	dropEmptyCloneDeploymentCleanupForParentMigration(t, tx)
	dropEmptyCloneEnvironmentCleanupForParentMigration(t, tx)
	raw, err := migrations.FS.ReadFile("20261011232006400_clone_workload_configuration_cleanups.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), strings.SplitN(string(raw), "-- +goose Down", 2)[1]); err != nil {
		t.Fatal("dependent workload cleanup downgrade", err)
	}
}

func TestPgCloneWorkloadCleanupMigrationRefusesEvidence(t *testing.T) {
	s, pool, l, _ := configurationCleanupFixture(t, true)
	raw, err := migrations.FS.ReadFile("20261011232006400_clone_workload_configuration_cleanups.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	dropEmptyCloneDeploymentCleanupForParentMigration(t, tx)
	dropEmptyCloneEnvironmentCleanupForParentMigration(t, tx)
	for _, statement := range []string{parts[1], parts[0], parts[1], parts[0]} {
		if _, err = tx.Exec(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	_ = tx.Rollback(t.Context())
	if _, err = s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	retireConfigurationCleanupFixture(t, s, l)
	if err = s.CleanupProjectEnvironmentCloneConfigurationForLease(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	if err = s.CleanupProjectEnvironmentCloneWorkloadsForLease(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(t.Context(), parts[1])
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.ConstraintName != "clone_workload_cleanup_down_no_evidence" {
		t.Fatal("retained evidence downgrade", err)
	}
	_ = tx.Rollback(t.Context())
}
