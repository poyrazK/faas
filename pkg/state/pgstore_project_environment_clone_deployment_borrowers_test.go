//go:build !no_pg

// adr: 590
package state_test

import (
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// A blocked member has no native drain row, so it independently borrows its
// captured deployment. These retained UUIDs deliberately have no parent FKs.
const drainPlanDeploymentBorrowerSQL = `WITH plan AS (
 INSERT INTO managed_postgres_cutover_drain_plans
 (id,cutover_id,app_id,owner_node_id,owner_revision,fenced_at,inventory_count)
 VALUES(gen_random_uuid(),gen_random_uuid(),gen_random_uuid(),gen_random_uuid(),1,clock_timestamp(),1)
 RETURNING id
)
INSERT INTO managed_postgres_cutover_drain_plan_members
 (plan_id,instance_id,deployment_id,node_id,wake_id,instance_kind,instance_mode,observed_state,deployment_scope,qualification_live,blocker_code)
SELECT id,gen_random_uuid(),$1::uuid,gen_random_uuid(),gen_random_uuid(),'wake','normal','parked','test',false,'parked_instance_unproven' FROM plan`

func TestPgCloneDeploymentBorrowers(t *testing.T) {
	for _, fault := range []string{"none", "stale", "unretired", "legacy", "cleaned"} {
		t.Run(fault, func(t *testing.T) {
			s, pool, l, _ := configurationCleanupFixture(t, fault != "legacy")
			native, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l)
			if err != nil {
				t.Fatal(err)
			}
			if fault != "unretired" {
				retireConfigurationCleanupFixture(t, s, l)
			}
			if fault == "cleaned" {
				if err = s.CleanupProjectEnvironmentCloneConfigurationForLease(t.Context(), l); err != nil {
					t.Fatal(err)
				}
				if err = s.CleanupProjectEnvironmentCloneWorkloadsForLease(t.Context(), l); err != nil {
					t.Fatal(err)
				}
			}
			if fault == "stale" {
				l.Token = uuid.NewString()
			}
			report, err := s.ProjectEnvironmentCloneDeploymentBorrowersForLease(t.Context(), l)
			if fault == "stale" || fault == "unretired" || fault == "legacy" {
				if !errors.Is(err, state.ErrConflict) || !reflect.DeepEqual(report, state.ProjectEnvironmentCloneDeploymentBorrowers{}) {
					t.Fatal(report, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			id := native.Plan.Workloads[0].TargetDeploymentID
			counts := report.Deployments[id]
			if len(report.Deployments) != 1 || report.OperationID != l.Operation.ID || report.NativePlanSHA256 != state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlanSHA256(native.Plan) || len(report.NativeObservationSHA256) != 64 {
				t.Fatal(report)
			}
			// Schema changes must update the explicit SQL inventory, including CASCADE
			// and SET NULL references and both sides of lineage/mirror relationships.
			rows, err := pool.Query(t.Context(), "SELECT conname FROM pg_constraint WHERE contype='f' AND confrelid='deployments'::regclass")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close() // Fatal inventory assertions must release the pool connection.
			n := 0
			for rows.Next() {
				var name string
				if err = rows.Scan(&name); err != nil {
					t.Fatal(err)
				}
				if _, ok := counts[name]; !ok {
					t.Fatal("missing deployment borrower", name)
				}
				n++
			}
			rows.Close()
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
			if len(counts) != n {
				t.Fatal("stale borrower inventory", counts, n)
			}
			softRows, err := pool.Query(t.Context(), `SELECT c.table_name||'.'||c.column_name
 FROM information_schema.columns c JOIN information_schema.tables t USING(table_schema,table_name)
 WHERE c.table_schema='public' AND t.table_type='BASE TABLE'
 AND (c.column_name ~ 'deployment.*_id$' OR c.table_name='environment_workload_retirements' AND c.column_name='deployment_ids')
 AND NOT EXISTS(SELECT 1 FROM pg_inherits i JOIN pg_class child ON child.oid=i.inhrelid JOIN pg_namespace ns ON ns.oid=child.relnamespace WHERE ns.nspname=c.table_schema AND child.relname=c.table_name)
 AND NOT (c.table_name='github_deployments' AND c.column_name='github_deployment_id')
 AND NOT EXISTS(SELECT 1 FROM pg_constraint f JOIN pg_class tab ON tab.oid=f.conrelid
 JOIN pg_namespace ns ON ns.oid=tab.relnamespace JOIN pg_attribute a ON a.attrelid=tab.oid AND a.attnum=ANY(f.conkey)
 WHERE f.contype='f' AND f.confrelid='deployments'::regclass AND ns.nspname=c.table_schema AND tab.relname=c.table_name AND a.attname=c.column_name)
 ORDER BY 1`)
			if err != nil {
				t.Fatal(err)
			}
			defer softRows.Close()
			var actual []string
			for softRows.Next() {
				var name string
				if err = softRows.Scan(&name); err != nil {
					t.Fatal(err)
				}
				actual = append(actual, name)
			}
			softRows.Close()
			if err = softRows.Err(); err != nil {
				t.Fatal(err)
			}
			var inventoried []string
			for name := range report.SoftReferences[id] {
				inventoried = append(inventoried, name)
			}
			sort.Strings(inventoried)
			if !reflect.DeepEqual(actual, inventoried) {
				t.Fatal("soft deployment reference inventory changed", actual, inventoried)
			}
			// Retained logs count even though deleting the deployment would cascade
			// them away. A source deployment's log must not enter the target count.
			beforeLogs := counts["deployment_logs_deployment_id_fkey"]
			for _, deploymentID := range []string{id, native.Plan.Workloads[0].SourceDeploymentID} {
				if _, err = pool.Exec(t.Context(), "INSERT INTO deployment_logs(deployment_id,seq,line) VALUES($1,987654321,'retained')", deploymentID); err != nil {
					t.Fatal(err)
				}
			}
			withLogs, err := s.ProjectEnvironmentCloneDeploymentBorrowersForLease(t.Context(), l)
			if err != nil || withLogs.Deployments[id]["deployment_logs_deployment_id_fkey"] != beforeLogs+1 {
				t.Fatal("retained or source references miscounted", withLogs, err)
			}
			original := counts["deployment_revision_pins_deployment_id_fkey"]
			counts["deployment_revision_pins_deployment_id_fkey"] = 999
			replay, err := s.ProjectEnvironmentCloneDeploymentBorrowersForLease(t.Context(), l)
			if err != nil || replay.Deployments[id]["deployment_revision_pins_deployment_id_fkey"] != original {
				t.Fatal("caller mutation changed inventory", replay, err)
			}
			if _, err = pool.Exec(t.Context(), drainPlanDeploymentBorrowerSQL, id); err != nil {
				t.Fatal(err)
			}
			withPlan, err := s.ProjectEnvironmentCloneDeploymentBorrowersForLease(t.Context(), l)
			if err != nil || withPlan.SoftReferences[id]["managed_postgres_cutover_drain_plan_members.deployment_id"] != 1 {
				t.Fatal("blocked retirement evidence omitted", withPlan, err)
			}
		})
	}
}
