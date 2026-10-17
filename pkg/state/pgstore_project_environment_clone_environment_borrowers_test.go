//go:build !no_pg

// adr: 590
package state_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgCloneEnvironmentBorrowersAuthenticationAndCleanup(t *testing.T) {
	for _, fault := range []string{"none", "legacy", "unretired", "stale", "changed", "native", "configuration_receipt", "workload_receipt", "protected", "settings_cleaned", "cleaned", "predecessor_guard"} {
		t.Run(fault, func(t *testing.T) {
			s, pool, l, appID := configurationCleanupFixture(t, fault != "legacy")
			original, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l)
			if err != nil {
				t.Fatal(err)
			}
			if fault != "unretired" {
				retireConfigurationCleanupFixture(t, s, l)
			}
			if fault == "configuration_receipt" || fault == "workload_receipt" || fault == "settings_cleaned" || fault == "cleaned" || fault == "predecessor_guard" {
				if err = s.CleanupProjectEnvironmentCloneConfigurationForLease(t.Context(), l); err != nil {
					t.Fatal(err)
				}
			}
			if fault == "workload_receipt" || fault == "cleaned" || fault == "predecessor_guard" {
				if err = s.CleanupProjectEnvironmentCloneWorkloadsForLease(t.Context(), l); err != nil {
					t.Fatal(err)
				}
			}
			switch fault {
			case "stale":
				l.Token = uuid.NewString()
			case "changed":
				_, err = pool.Exec(t.Context(), "UPDATE app_envs SET value='changed' WHERE app_id=$1 AND scope=$2", appID, l.Operation.TargetEnvironment)
			case "native":
				_, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_checkpoint_workload_retirements SET observation_sha256=$2 WHERE operation_id=$1", l.Operation.ID, strings.Repeat("a", 64))
			case "configuration_receipt":
				_, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_configuration_cleanups SET manifest_sha256=$2 WHERE operation_id=$1", l.Operation.ID, strings.Repeat("a", 64))
			case "workload_receipt":
				_, err = pool.Exec(t.Context(), "UPDATE project_environment_clone_workload_cleanups SET configuration_cleanup_sha256=$2 WHERE operation_id=$1", l.Operation.ID, strings.Repeat("a", 64))
			case "predecessor_guard":
				_, err = pool.Exec(t.Context(), "DELETE FROM project_environment_clone_configuration_cleanups WHERE operation_id=$1", l.Operation.ID)
				var pgerr *pgconn.PgError
				if !errors.As(err, &pgerr) || pgerr.Code != "23503" {
					t.Fatal("workload receipt lost its settings predecessor", err)
				}
				err = nil
			case "protected":
				_, err = pool.Exec(t.Context(), "UPDATE project_environments SET protected=true WHERE id=$1", original.Plan.EnvironmentID)
			}
			if err != nil {
				t.Fatal(err)
			}
			report, err := s.ProjectEnvironmentCloneEnvironmentBorrowersForLease(t.Context(), l)
			if fault != "none" && fault != "settings_cleaned" && fault != "cleaned" && fault != "predecessor_guard" {
				if !errors.Is(err, state.ErrConflict) || !reflect.DeepEqual(report, state.ProjectEnvironmentCloneEnvironmentBorrowers{}) {
					t.Fatal("unauthenticated or partial inventory", report, err)
				}
				return
			}
			if err != nil || report.OperationID != l.Operation.ID || report.EnvironmentID != original.Plan.EnvironmentID || len(report.OwnershipSHA256) != 64 || report.NativePlanSHA256 != state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlanSHA256(original.Plan) || len(report.NativeObservationSHA256) != 64 || len(report.References) != 25 || report.OwnedDeployments != 1 || report.ForeignDeployments != 0 {
				t.Fatal("inventory lost original authority", report, err)
			}
			specs := int64(1)
			if fault == "cleaned" || fault == "predecessor_guard" {
				specs = 0
				if len(report.ConfigurationCleanupSHA256) != 64 || len(report.WorkloadCleanupSHA256) != 64 {
					t.Fatal("cleanup evidence omitted", report)
				}
			} else if fault == "settings_cleaned" {
				if len(report.ConfigurationCleanupSHA256) != 64 || report.WorkloadCleanupSHA256 != "" {
					t.Fatal("partial cleanup evidence omitted or invented", report)
				}
			} else if report.ConfigurationCleanupSHA256 != "" || report.WorkloadCleanupSHA256 != "" {
				t.Fatal("invented cleanup evidence", report)
			}
			if report.References["project_environment_workload_specs"] != specs {
				t.Fatal("copied specs miscounted", report)
			}
			replay, err := s.ProjectEnvironmentCloneEnvironmentBorrowersForLease(t.Context(), l)
			if err != nil || !reflect.DeepEqual(report, replay) {
				t.Fatal("observer altered ownership", err)
			}
			// Mutating a returned map cannot poison another observation.
			report.References["custom_domains"] = 100
			later, err := s.ProjectEnvironmentCloneEnvironmentBorrowersForLease(t.Context(), l)
			if err != nil || !reflect.DeepEqual(replay, later) {
				t.Fatal("inventory retained caller memory", err)
			}
		})
	}
}

func TestPgCloneEnvironmentBorrowersRetainInactiveAndScopeOnlyRows(t *testing.T) {
	s, pool, l, appID := configurationCleanupFixture(t, true)
	native, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l)
	if err != nil {
		t.Fatal(err)
	}
	retireConfigurationCleanupFixture(t, s, l)
	envID := native.Plan.EnvironmentID
	var sourceID string
	if err = pool.QueryRow(t.Context(), "SELECT id FROM project_environments WHERE project_id=$1 AND slug=$2", l.Operation.ProjectID, l.Operation.SourceEnvironment).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	before, err := s.ProjectEnvironmentCloneEnvironmentBorrowersForLease(t.Context(), l)
	if err != nil {
		t.Fatal(err)
	}
	// These are retained references, even when their operational owners call them
	// expired, revoked, inactive, detached, or otherwise finished.
	for _, id := range []string{envID, sourceID} {
		if _, err = pool.Exec(t.Context(), "INSERT INTO custom_domains(domain,app_id,environment_id) VALUES($1,$2,$3)", id+".example.com", appID, id); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(t.Context(), "INSERT INTO app_environment_workload_intents(account_id,app_id,environment_id) VALUES($1,$2,$3)", l.Operation.AccountID, appID, id); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(t.Context(), "INSERT INTO invocation_work_environment_domains(app_id,environment_id,policy_name,kind,digest) VALUES($1,$2,$3,'key',$4)", appID, id, "original-"+id, make([]byte, 32)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = pool.Exec(t.Context(), `INSERT INTO dev_bridge_sessions(id,account_id,target_app_id,environment_id,scope,attachment_digest,request_digest,expires_at,revoked_at) VALUES('retained',$1,$2,$3,jsonb_build_object('account_id',($1::uuid)::text,'target_app_id',($2::uuid)::text,'environment_id',($3::uuid)::text,'developer_id','test','project_id',($4::uuid)::text),$5,$5,now()-interval '1 hour',now())`, l.Operation.AccountID, appID, envID, l.Operation.ProjectID, make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), "INSERT INTO environment_external_field_owners(environment_id,resource,field_path,manager_id) VALUES($1,'environment','source','terraform')", envID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), "INSERT INTO environment_managed_fields(environment_id,resource,field_path,manager_kind,manager_id,desired_value) VALUES($1,'environment','variables/TOKEN','operator','original','{}')", envID); err != nil {
		t.Fatal(err)
	}
	gitID, revisionID := uuid.NewString(), uuid.NewString()
	if _, err = pool.Exec(t.Context(), "INSERT INTO environment_git_sources(id,account_id,project_id,environment_id,repository_id,installation_id,repository,source_ref,manifest_path,mode,approval_policy,suspended,detached) VALUES($1,$2,$3,$4,1,1,'test/repository','main','gregale.yaml','enforce','manual',false,false)", gitID, l.Operation.AccountID, l.Operation.ProjectID, envID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), "INSERT INTO environment_desired_revisions(id,source_id,commit_sha,definition_digest,definition,approved_by) VALUES($1,$2,$3,$4,'{\"workloads\":{}}','test')", revisionID, gitID, strings.Repeat("a", 40), strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), "UPDATE environment_git_sources SET generation=1,approved_revision_id=$2 WHERE id=$1", gitID, revisionID); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err = tx.Exec(t.Context(), "INSERT INTO environment_gitops_jobs(source_id,desired_generation,claimed_generation,lease_token,lease_until) VALUES($1,1,1,'original',now()+interval '1 hour') ON CONFLICT(source_id) DO UPDATE SET desired_generation=1,claimed_generation=1,lease_token='original',lease_until=excluded.lease_until", gitID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), "SELECT set_config('gregale.gitops_lease','original',true)"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), "INSERT INTO environment_workload_graphs(source_id,environment_id,revision_id,generation,intent_version,plan_hash,definition_digest,members,resource_ids) VALUES($1,$2,$3,1,0,$4,$4,'[]','{}')", gitID, envID, revisionID, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	// Historical fixture only: a cancelled journal remains an environment
	// borrower after its source detaches. Runtime lease authorization is covered
	// by the GitOps retirement integration tests; FK and CHECK guards stay active.
	if _, err = tx.Exec(t.Context(), "ALTER TABLE environment_workload_retirements DISABLE TRIGGER guard_environment_workload_retirement"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), `INSERT INTO environment_workload_retirements(source_id,account_id,project_id,environment_id,app_id,resource,revision_id,generation,plan_hash,phase,deployment_ids,route_deployment_id) VALUES($1,$2,$3,$4,$5,'workload/api',$6,1,$7,'cancelled',ARRAY[$8::uuid],$8)`, gitID, l.Operation.AccountID, l.Operation.ProjectID, envID, appID, revisionID, strings.Repeat("a", 64), native.Plan.Workloads[0].TargetDeploymentID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), "ALTER TABLE environment_workload_retirements ENABLE TRIGGER guard_environment_workload_retirement"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), "UPDATE environment_git_sources SET mode='report',suspended=true,detached=true WHERE id=$1", gitID); err != nil {
		t.Fatal(err)
	}

	releaseID := uuid.NewString()
	if _, err = pool.Exec(t.Context(), "INSERT INTO project_release_sets(id,account_id,project_id,environment_slug,ttl_seconds,expires_at) VALUES($1,$2,$3,$4,60,now()-interval '1 hour')", releaseID, l.Operation.AccountID, l.Operation.ProjectID, l.Operation.TargetEnvironment); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), `INSERT INTO project_environment_qualifications(account_id,project_id,environment_slug,release_set_id,status,checks,created_at,expires_at) VALUES($1,$2,$3,$4,'passed','[{"name":"health","status":"passed","results":[{}]},{"name":"smoke","status":"passed","results":[{}]}]',now()-interval '2 hours',now()-interval '1 hour')`, l.Operation.AccountID, l.Operation.ProjectID, l.Operation.TargetEnvironment, releaseID); err != nil {
		t.Fatal(err)
	}
	for i, row := range []struct {
		id    any
		scope string
	}{{envID, l.Operation.SourceEnvironment}, {sourceID, l.Operation.TargetEnvironment}, {nil, l.Operation.TargetEnvironment}, {sourceID, l.Operation.SourceEnvironment}} {
		if _, err = pool.Exec(t.Context(), "INSERT INTO invocations(account_id,app_id,source,environment_id,deployment_scope) VALUES($1,$2,'async_invoke',$3,$4)", l.Operation.AccountID, appID, row.id, row.scope); err != nil {
			t.Fatal(err)
		}
		if i == 3 {
			continue
		}
		if _, err = pool.Exec(t.Context(), "INSERT INTO runtime_environment_scaling_states(app_id,environment_key,environment_id,scope) VALUES($1,CASE WHEN $2::uuid IS NULL THEN 'scope:'||$3::text ELSE 'environment:'||($2::uuid)::text END,$2,$3) ON CONFLICT(app_id,environment_key) DO UPDATE SET scope=excluded.scope", appID, row.id, row.scope); err != nil {
			t.Fatal(err)
		}
	}
	// The matching source environment/scope row is excluded; the three target
	// UUID or scope references are retained, without double-counting overlap.

	foreignID := uuid.NewString()
	if _, err = pool.Exec(t.Context(), `INSERT INTO deployments SELECT (jsonb_populate_record(NULL::deployments,to_jsonb(d)||jsonb_build_object('id',$1::text,'reason','foreign','revision',d.revision+1))).* FROM deployments d WHERE d.id=$2`, foreignID, native.Plan.Workloads[0].TargetDeploymentID); err != nil {
		t.Fatal(err)
	}
	after, err := s.ProjectEnvironmentCloneEnvironmentBorrowersForLease(t.Context(), l)
	if err != nil {
		t.Fatal(err)
	}
	expected := before.References
	for _, table := range []string{"custom_domains", "app_environment_workload_intents", "invocation_work_environment_domains", "dev_bridge_sessions", "environment_external_field_owners", "environment_managed_fields", "environment_git_sources", "environment_workload_graphs", "project_release_sets", "project_environment_qualifications"} {
		expected[table]++
	}
	expected["invocations"] += 3
	expected["environment_workload_retirements"]++
	expected["runtime_environment_scaling_states"] += 3
	if !reflect.DeepEqual(after.References, expected) || after.OwnedDeployments != 1 || after.ForeignDeployments != 1 || after.OwnershipSHA256 != before.OwnershipSHA256 || after.NativePlanSHA256 != before.NativePlanSHA256 {
		t.Fatal("retained metadata omitted or ownership adopted", after, expected)
	}
	deployments, err := s.ProjectEnvironmentCloneDeploymentBorrowersForLease(t.Context(), l)
	target := native.Plan.Workloads[0].TargetDeploymentID
	if err != nil || deployments.Deployments[target]["environment_workload_retirements_route_deployment_id_fkey"] != 1 || deployments.SoftReferences[target]["environment_workload_retirements.deployment_ids"] != 1 {
		t.Fatal("completed retirement lost its original deployment references", deployments, err)
	}
	replay, err := s.ProjectEnvironmentCloneEnvironmentBorrowersForLease(t.Context(), l)
	if err != nil || !reflect.DeepEqual(after, replay) {
		t.Fatal("inventory mutated retained metadata", err)
	}
}

func TestPgCloneEnvironmentBorrowerForeignKeyInventory(t *testing.T) {
	_, _, pool := pgWithPool(t)
	rows, err := pool.Query(t.Context(), `SELECT c.conrelid::regclass::text||'('||string_agg(a.attname,',' ORDER BY k.ordinality)||')' FROM pg_constraint c CROSS JOIN LATERAL unnest(c.conkey) WITH ORDINALITY k(attnum,ordinality) JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=k.attnum WHERE c.contype='f' AND c.confrelid='project_environments'::regclass GROUP BY c.oid,c.conrelid ORDER BY 1`)
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
	expected := []string{
		"app_environment_route_budgets(environment_id)", "app_environment_secret_ref_suppressions(environment_id)", "app_environment_secret_refs(environment_id)", "app_environment_workload_intents(environment_id)", "custom_domains(environment_id)", "dev_bridge_sessions(environment_id)", "environment_external_field_owners(environment_id)", "environment_git_sources(account_id,project_id,environment_id)", "environment_managed_fields(environment_id)", "environment_workload_graphs(environment_id)", "environment_workload_retirements(environment_id)", "feature_flag_versions(environment_id)", "hosting_sites(project_id,environment)", "invocation_environment_queue_admissions(environment_id)", "invocation_work_environment_admissions(environment_id)", "invocation_work_environment_domains(environment_id)", "invocations(environment_id)", "project_environment_edge_policies(project_id,environment_slug)", "project_environment_qualifications(project_id,environment_slug)", "project_environment_queue_runtime_sets(environment_id)", "project_environment_route_policies(project_id,environment_slug)", "project_environment_workload_specs(environment_id)", "project_release_sets(project_id,environment_slug)", "project_release_stages(project_id,environment_slug)", "runtime_environment_scaling_states(environment_id)",
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("environment borrower inventory changed; review explicit counts before metadata retirement", actual)
	}
}

func TestPgCloneEnvironmentHostingBorrowers(t *testing.T) {
	for _, status := range []string{"active", "suspended", "deleting"} {
		t.Run(status, func(t *testing.T) {
			s, pool, l, _ := configurationCleanupFixtureForPhase(t, true, "materialized")
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
			targetID, sourceID := uuid.NewString(), uuid.NewString()
			for _, site := range []struct{ id, environment string }{{targetID, l.Operation.TargetEnvironment}, {sourceID, l.Operation.SourceEnvironment}} {
				storedStatus := status
				if status == "suspended" {
					storedStatus = "active"
				}
				if _, err = pool.Exec(t.Context(), `INSERT INTO hosting_sites(id,account_id,project_id,environment,name,hostname,origin_id,status,durable_routing,suspended) VALUES($1,$2,$3,$4,'retained',$5,$6,$7,true,$8)`, site.id, l.Operation.AccountID, l.Operation.ProjectID, site.environment, site.id+".example.com", strings.Repeat("a", 64), storedStatus, status == "suspended"); err != nil {
					t.Fatal(err)
				}
			}
			report, err := s.ProjectEnvironmentCloneEnvironmentBorrowersForLease(t.Context(), l)
			if err != nil || report.References["hosting_sites"] != 1 {
				t.Fatal("hosting inventory lost a retained site or included another environment", report, err)
			}
			if err = s.CleanupProjectEnvironmentCloneEnvironmentForLease(t.Context(), l); !errors.Is(err, state.ErrConflict) || !strings.Contains(err.Error(), "hosting_sites") {
				t.Fatal("hosting borrower did not block cleanup before deletion", err)
			}
			var retained bool
			if err = pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM hosting_sites WHERE id=$1) AND EXISTS(SELECT 1 FROM project_environments WHERE id=$2) AND NOT EXISTS(SELECT 1 FROM project_environment_clone_environment_cleanups WHERE operation_id=$3)`, targetID, native.Plan.EnvironmentID, l.Operation.ID).Scan(&retained); err != nil || !retained {
				t.Fatal("blocked hosting cleanup changed retained metadata", err)
			}
			// Simulate the separate hosting owner's completed retirement. A site
			// in the source environment must not block or be removed by recovery.
			if _, err = pool.Exec(t.Context(), "DELETE FROM hosting_sites WHERE id=$1", targetID); err != nil {
				t.Fatal(err)
			}
			if err = s.CleanupProjectEnvironmentCloneEnvironmentForLease(t.Context(), l); err != nil {
				t.Fatal("cleanup after hosting retirement", err)
			}
			if err = pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM hosting_sites WHERE id=$1)", sourceID).Scan(&retained); err != nil || !retained {
				t.Fatal("source hosting site was removed", err)
			}
		})
	}
}
