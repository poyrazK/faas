//go:build !no_pg

// adr: 590
package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgCloneEnvironmentCleanupRetainsMCPBorrowers(t *testing.T) {
	for _, tc := range []struct{ table, scopeColumn, insert string }{
		{"app_mcp_readiness_policies", "scope", `INSERT INTO app_mcp_readiness_policies(app_id,scope,policy) SELECT id,$3::text,jsonb_build_object('scope',$3::text,'enabled',false) FROM apps WHERE id=$1::uuid AND account_id=$2::uuid`},
		{"app_mcp_readiness_policy_history", "scope", `INSERT INTO app_mcp_readiness_policy_history(revision,app_id,scope,policy) SELECT gen_random_uuid(),id,$3::text,jsonb_build_object('scope',$3::text,'enabled',false) FROM apps WHERE id=$1::uuid AND account_id=$2::uuid`},
		{"mcp_readiness_incidents", "scope", `WITH rule AS (INSERT INTO alert_rules(account_id,app_id,name,metric,comparison,threshold,window_spec,webhook_url,webhook_secret_sealed) SELECT $2::uuid,id,'retained-'||$3::text,'mcp_readiness_unhealthy','gt',0,'5m','https://example.com','retained' FROM apps WHERE id=$1::uuid RETURNING id,app_id,account_id) INSERT INTO mcp_readiness_incidents(id,rule_id,account_id,app_id,scope,coverage,opened_at,resolved_at) SELECT gen_random_uuid(),id,account_id,app_id,$3::text,'retained',clock_timestamp()-interval '2 hours',clock_timestamp()-interval '1 hour' FROM rule`},
		{"mcp_readiness_maintenance_windows", "scope", `INSERT INTO mcp_readiness_maintenance_windows(id,account_id,app_id,scope,starts_at,ends_at,reason,created_by,created_at,cancelled_at,cancelled_by,cancel_reason) SELECT gen_random_uuid(),$2::uuid,id,$3::text,clock_timestamp()-interval '2 hours',clock_timestamp()-interval '1 hour','retained',$2::uuid,clock_timestamp()-interval '3 hours',clock_timestamp()-interval '90 minutes',$2::uuid,'cancelled' FROM apps WHERE id=$1::uuid`},
		{"alert_rules", "mcp_readiness_scope", `INSERT INTO alert_rules(account_id,app_id,name,enabled,metric,comparison,threshold,window_spec,webhook_url,webhook_secret_sealed,mcp_readiness_scope) SELECT $2::uuid,id,'retained-'||$3::text,false,'mcp_readiness_unhealthy','gt',0,'5m','https://example.com','retained',$3::text FROM apps WHERE id=$1::uuid`},
	} {
		t.Run(tc.table, func(t *testing.T) {
			s, pool, l, appID := configurationCleanupFixtureForPhase(t, true, "materialized")
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
			for _, scope := range []string{l.Operation.SourceEnvironment, l.Operation.TargetEnvironment} {
				if tag, err := pool.Exec(t.Context(), tc.insert, appID, l.Operation.AccountID, scope); err != nil || tag.RowsAffected() != 1 {
					t.Fatal("missing retained MCP fixture", scope, tag, err)
				}
			}
			// Disabled policies, resolved incidents and expired/cancelled maintenance
			// remain references until their own lifecycle has removed the rows.
			if err = s.CleanupProjectEnvironmentCloneEnvironmentForLease(t.Context(), l); !errors.Is(err, state.ErrConflict) {
				t.Fatal("retained MCP reference allowed environment deletion", err)
			}
			var live, receipt bool
			if err = pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM project_environments WHERE id=$1),EXISTS(SELECT 1 FROM project_environment_clone_environment_cleanups WHERE operation_id=$2)", native.Plan.EnvironmentID, l.Operation.ID).Scan(&live, &receipt); err != nil || !live || receipt {
				t.Fatal("partial environment cleanup", live, receipt, err)
			}
			if tag, err := pool.Exec(t.Context(), "DELETE FROM "+tc.table+" WHERE app_id=$1 AND "+tc.scopeColumn+"=$2", appID, l.Operation.TargetEnvironment); err != nil || tag.RowsAffected() != 1 {
				t.Fatal("remove only target fixture", tag, err)
			}
			if err = s.CleanupProjectEnvironmentCloneEnvironmentForLease(t.Context(), l); err != nil {
				t.Fatal("source MCP reference blocked target cleanup", err)
			}
			var sourceCount int
			if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM "+tc.table+" WHERE app_id=$1 AND "+tc.scopeColumn+"=$2", appID, l.Operation.SourceEnvironment).Scan(&sourceCount); err != nil || sourceCount != 1 {
				t.Fatal("source MCP reference changed", sourceCount, err)
			}
		})
	}
}

func TestPgCloneDeploymentCleanupRetainsMCPBorrowers(t *testing.T) {
	for _, tc := range []struct{ table, insert string }{
		{"deployment_mcp_readiness_evidence", `INSERT INTO deployment_mcp_readiness_evidence(deployment_id,role,policy_revision,artifact_sha256,passed,observed_at,evidence) SELECT d.id,'server',gen_random_uuid(),'retained',false,clock_timestamp(),'{}' FROM deployments d JOIN apps a ON a.id=d.app_id WHERE d.id=$1::uuid AND a.account_id=$2::uuid`},
		{"deployment_mcp_readiness_monitors", `INSERT INTO deployment_mcp_readiness_monitors(deployment_id,role,status,next_probe_at) SELECT d.id,'server','{}',clock_timestamp()-interval '1 hour' FROM deployments d JOIN apps a ON a.id=d.app_id WHERE d.id=$1::uuid AND a.account_id=$2::uuid`},
		{"deployment_mcp_readiness_overrides", `INSERT INTO deployment_mcp_readiness_overrides(id,deployment_id,policy_revision,artifact_sha256,reason,created_by,expires_at,used_at) SELECT gen_random_uuid(),id,gen_random_uuid(),'retained','retained',$2::uuid,clock_timestamp()-interval '1 hour',clock_timestamp()-interval '2 hours' FROM deployments WHERE id=$1::uuid`},
	} {
		t.Run(tc.table, func(t *testing.T) {
			s, pool, l, native := deploymentCleanupFixture(t)
			target, source := native.Plan.Workloads[0].TargetDeploymentID, native.Plan.Workloads[0].SourceDeploymentID
			for _, id := range []string{source, target} {
				if tag, err := pool.Exec(t.Context(), tc.insert, id, l.Operation.AccountID); err != nil || tag.RowsAffected() != 1 {
					t.Fatal("missing MCP deployment fixture", tag, err)
				}
			}
			report, err := s.ProjectEnvironmentCloneDeploymentBorrowersForLease(t.Context(), l)
			if err != nil || report.Deployments[target][tc.table+"_deployment_id_fkey"] != 1 {
				t.Fatal("MCP borrower count or source isolation", report, err)
			}
			if err = s.CleanupProjectEnvironmentCloneDeploymentsForLease(t.Context(), l); !errors.Is(err, state.ErrConflict) {
				t.Fatal("retained MCP evidence allowed deployment cascade", err)
			}
			var live, receipt bool
			if err = pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM deployments WHERE id=$1),EXISTS(SELECT 1 FROM project_environment_clone_deployment_cleanups WHERE operation_id=$2)", target, l.Operation.ID).Scan(&live, &receipt); err != nil || !live || receipt {
				t.Fatal("partial deployment cleanup", live, receipt, err)
			}
			if tag, err := pool.Exec(t.Context(), "DELETE FROM "+tc.table+" WHERE deployment_id=$1", target); err != nil || tag.RowsAffected() != 1 {
				t.Fatal("remove only target fixture", tag, err)
			}
			if err = s.CleanupProjectEnvironmentCloneDeploymentsForLease(t.Context(), l); err != nil {
				t.Fatal("source MCP evidence blocked target cleanup", err)
			}
			var sourceCount int
			if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM "+tc.table+" WHERE deployment_id=$1", source).Scan(&sourceCount); err != nil || sourceCount != 1 {
				t.Fatal("source evidence changed", sourceCount, err)
			}
		})
	}
}
