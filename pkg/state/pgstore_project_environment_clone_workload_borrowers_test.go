//go:build !no_pg

// adr: 590
package state_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedCloneWorkloadBorrower(t *testing.T, pool *pgxpool.Pool, l state.ProjectEnvironmentCloneLease, p state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan, kind string) {
	t.Helper()
	ctx := t.Context()
	w := p.Workloads[0]
	envID := p.EnvironmentID
	if kind == "cross_environment_queue" {
		if err := pool.QueryRow(ctx, "SELECT id FROM project_environments WHERE project_id=$1 AND slug=$2", l.Operation.ProjectID, l.Operation.SourceEnvironment).Scan(&envID); err != nil {
			t.Fatal(err)
		}
	}
	if kind == "foreign_pin" {
		id := uuid.NewString()
		if _, err := pool.Exec(ctx, `INSERT INTO deployments SELECT (jsonb_populate_record(NULL::deployments,to_jsonb(d)||jsonb_build_object('id',$1::text,'reason','foreign','revision',d.revision+1))).* FROM deployments d WHERE d.id=$2`, id, w.TargetDeploymentID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, "INSERT INTO project_environment_workload_deployment_specs(deployment_id,spec_id) VALUES($1,$2)", id, w.SpecID); err != nil {
			t.Fatal(err)
		}
		return
	}
	if kind == "promotion" {
		id := uuid.NewString()
		if _, err := pool.Exec(ctx, "INSERT INTO project_environment_promotions(id,account_id,project_id,project_slug,from_environment,to_environment,promotion_hash,idempotency_key) VALUES($1,$2,$3,'borrower','production','stage',$4,'borrower')", id, l.Operation.AccountID, l.Operation.ProjectID, strings.Repeat("a", 64)); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, "INSERT INTO project_environment_promotion_workload_specs(promotion_id,app_id,deployment_id,prepared_spec_id,previous_spec_id,source_hash,previous_hash,previous_settings) VALUES($1,$2,$3,$4,$4,$5,$5,'{}')", id, w.AppID, w.TargetDeploymentID, w.SpecID, w.SpecHash); err != nil {
			t.Fatal(err)
		}
		return
	}
	var invocationID string
	if kind == "work_admission" || kind == "queue_admission" {
		if err := pool.QueryRow(ctx, "INSERT INTO invocations(account_id,app_id,source) VALUES($1,$2,'async_invoke') RETURNING id", l.Operation.AccountID, w.AppID).Scan(&invocationID); err != nil {
			t.Fatal(err)
		}
	}
	if kind == "work_admission" {
		if _, err := pool.Exec(ctx, "INSERT INTO invocation_work_environment_admissions(invocation_id,environment_id,workload_spec_id,settings_hash,app_id,policy_name,policy_revision,key_digest,fairness_limit) VALUES($1,$2,$3,$4,$5,'original',1,$6,0)", invocationID, envID, w.SpecID, w.SpecHash, w.AppID, make([]byte, 32)); err != nil {
			t.Fatal(err)
		}
		return
	}
	runtimeID := uuid.NewString()
	if _, err := pool.Exec(ctx, "INSERT INTO project_environment_queue_runtime_sets(id,account_id,project_id,environment_id,app_id,deployment_id,workload_spec_id,settings_hash,queue_revision,binding_count,book_hash) VALUES($1,$2,$3,$4,$5,$6,$7,$8,1,0,$8)", runtimeID, l.Operation.AccountID, l.Operation.ProjectID, envID, w.AppID, w.TargetDeploymentID, w.SpecID, w.SpecHash); err != nil {
		t.Fatal(err)
	}
	if kind == "queue_admission" {
		consumerID := uuid.NewString()
		if _, err := pool.Exec(ctx, `INSERT INTO project_environment_queue_consumers(id,runtime_set_id,name,queue_name,definition,definition_hash) VALUES($1,$2,'original','messages','{"name":"original","queue_name":"messages"}',$3)`, consumerID, runtimeID, w.SpecHash); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, "INSERT INTO invocation_environment_queue_admissions(invocation_id,environment_id,account_id,app_id,consumer_id,runtime_set_id,deployment_id,workload_spec_id,pin_hash,settings_hash,definition_hash,queue_name,admitted_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$9,$9,'messages',clock_timestamp())", invocationID, envID, l.Operation.AccountID, w.AppID, consumerID, runtimeID, w.TargetDeploymentID, w.SpecID, w.SpecHash); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPgCloneWorkloadBorrowersPreventConfigurationDeletion(t *testing.T) {
	for _, kind := range []string{"none", "foreign_pin", "promotion", "queue", "cross_environment_queue", "work_admission", "queue_admission"} {
		t.Run(kind, func(t *testing.T) {
			s, pool, l, appID := configurationCleanupFixture(t, true)
			native, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l)
			if err != nil {
				t.Fatal(err)
			}
			retireConfigurationCleanupFixture(t, s, l)
			if kind != "none" {
				seedCloneWorkloadBorrower(t, pool, l, native.Plan, kind)
			}
			report, err := s.ProjectEnvironmentCloneWorkloadBorrowersForLease(t.Context(), l)
			if err != nil || report.OperationID != l.Operation.ID || report.EnvironmentID != native.Plan.EnvironmentID || report.NativePlanSHA256 != state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlanSHA256(native.Plan) || len(report.NativeObservationSHA256) != 64 {
				t.Fatal("unauthenticated borrower report", report, err)
			}
			counts := []int64{report.ForeignDeploymentPins, report.PromotionPreparedSpecs, report.PromotionPreviousSpecs, report.QueueRuntimeSets, report.InvocationWorkAdmissions, report.InvocationQueueAdmissions}
			expected := make([]int64, 6)
			switch kind {
			case "foreign_pin":
				expected[0] = 1
			case "promotion":
				expected[1], expected[2] = 1, 1
			case "queue", "cross_environment_queue":
				expected[3] = 1
			case "work_admission":
				expected[4] = 1
			case "queue_admission":
				expected[3], expected[5] = 1, 1
			}
			if !reflect.DeepEqual(counts, expected) {
				t.Fatal("borrower omitted or owned pin misclassified", counts, expected)
			}
			err = s.CleanupProjectEnvironmentCloneConfigurationForLease(t.Context(), l)
			if kind == "none" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, state.ErrConflict) {
				t.Fatal("borrowed settings deleted", err)
			}
			var original bool
			if err = pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM app_envs WHERE app_id=$1 AND scope=$2 AND key='ORIGINAL')", appID, l.Operation.TargetEnvironment).Scan(&original); err != nil || !original {
				t.Fatal("borrower guard allowed partial deletion", err)
			}
			replay, err := s.ProjectEnvironmentCloneWorkloadBorrowersForLease(t.Context(), l)
			if err != nil || !reflect.DeepEqual(report, replay) {
				t.Fatal("observer changed borrower ownership", err)
			}
		})
	}
}

func TestPgCloneWorkloadBorrowersRequireOriginalClosureAndLease(t *testing.T) {
	for _, fault := range []string{"unretired", "stale", "tampered"} {
		t.Run(fault, func(t *testing.T) {
			s, pool, l, _ := configurationCleanupFixture(t, true)
			if _, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l); err != nil {
				t.Fatal(err)
			}
			if fault != "unretired" {
				retireConfigurationCleanupFixture(t, s, l)
			}
			if fault == "stale" {
				l.Token = uuid.NewString()
			}
			if fault == "tampered" {
				if _, err := pool.Exec(t.Context(), "UPDATE project_environment_clone_checkpoint_workload_retirements SET observation_sha256=$2 WHERE operation_id=$1", l.Operation.ID, strings.Repeat("a", 64)); err != nil {
					t.Fatal(err)
				}
			}
			report, err := s.ProjectEnvironmentCloneWorkloadBorrowersForLease(t.Context(), l)
			if !errors.Is(err, state.ErrConflict) || report != (state.ProjectEnvironmentCloneWorkloadBorrowers{}) {
				t.Fatal("partial or unauthenticated report", report, err)
			}
		})
	}
}

func TestPgCloneWorkloadBorrowerForeignKeyInventory(t *testing.T) {
	_, _, pool := pgWithPool(t)
	rows, err := pool.Query(t.Context(), `SELECT c.conrelid::regclass::text||'.'||a.attname FROM pg_constraint c CROSS JOIN LATERAL unnest(c.conkey) k(attnum) JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=k.attnum WHERE c.contype='f' AND c.confrelid='project_environment_workload_specs'::regclass AND a.attname NOT IN ('environment_id','app_id') ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actual []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		actual = append(actual, name)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	expected := []string{"invocation_environment_queue_admissions.workload_spec_id", "invocation_work_environment_admissions.workload_spec_id", "project_environment_promotion_workload_specs.prepared_spec_id", "project_environment_promotion_workload_specs.previous_spec_id", "project_environment_queue_runtime_sets.workload_spec_id", "project_environment_workload_deployment_specs.spec_id", "project_environment_workload_heads.spec_id"}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("workload-spec borrower inventory changed; update explicit guards", actual)
	}
}
