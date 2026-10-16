//go:build !no_pg

// adr: 590
// adr: 1465
package state_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgCloneEnvironmentCleanupRetainsCacheBorrowers(t *testing.T) {
	for _, tc := range []struct {
		table, insert string
	}{
		{"project_environment_cache_policies", `INSERT INTO project_environment_cache_policies(account_id,app_id,project_id,environment_id,revision,rules) VALUES(@account,@app,@project,@environment,1,'[]')`},
		{"environment_cache_policy_history", `INSERT INTO environment_cache_policy_history(account_id,app_id,project_id,environment_id,revision,snapshot) VALUES(@account,@app,@project,@environment,1,'{}')`},
		{"environment_cache_policy_receipts", `INSERT INTO environment_cache_policy_receipts(account_id,app_id,environment_id,request_id,request_hash,response) VALUES(@account,@app,@environment,gen_random_uuid(),'retained','{}')`},
		{"environment_cache_purges", `INSERT INTO environment_cache_purges(account_id,app_id,project_id,environment_id,request_id,path) VALUES(@account,@app,@project,@environment,gen_random_uuid(),'/retained')`},
		{"environment_cache_gateway_observations", `WITH node AS (INSERT INTO compute_nodes(name,target_url,vpcpus,mem_mb,max_concurrency,admission_ceiling_mb) VALUES('cache-borrower','unix:///run/test.sock',2,1024,10,512) RETURNING id) INSERT INTO environment_cache_gateway_observations(node_id,app_id,environment_id,session_id,revision) SELECT id,@app,@environment,gen_random_uuid(),1 FROM node`},
		{"environment_cache_schedule_event_counters", `INSERT INTO environment_cache_schedule_event_counters(account_id,app_id,project_id,environment_id,last_sequence,pruned_through) VALUES(@account,@app,@project,@environment,1,1)`},
		{"environment_cache_schedule_events", `INSERT INTO environment_cache_schedule_events(account_id,app_id,project_id,environment_id,scope,sequence,payload) VALUES(@account,@app,@project,@environment,@scope,1,'{}')`},
		{"environment_cache_schedules", `INSERT INTO environment_cache_schedules(schedule_id,account_id,app_id,project_id,environment_id,scope,request,execute_at,status,finished_at) VALUES(gen_random_uuid(),@account,@app,@project,@environment,@scope,'{}',now()-interval '1 hour','cancelled',now())`},
	} {
		for _, scopeOnly := range []bool{false, true} {
			if scopeOnly && tc.table != "environment_cache_schedule_events" && tc.table != "environment_cache_schedules" {
				continue
			}
			name := tc.table
			if scopeOnly {
				name += "/scope_only"
			}
			t.Run(name, func(t *testing.T) {
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
				// UUID and scope references each retain metadata independently.
				// Terminal schedules and fully pruned counters remain borrowers.
				environmentID, scope := native.Plan.EnvironmentID, l.Operation.SourceEnvironment
				if scopeOnly {
					source, err := s.ProjectEnvironmentBySlug(t.Context(), l.Operation.AccountID, l.Operation.ProjectID, l.Operation.SourceEnvironment)
					if err != nil {
						t.Fatal(err)
					}
					environmentID, scope = source.ID, l.Operation.TargetEnvironment
				}
				if _, err = pool.Exec(t.Context(), tc.insert, pgx.NamedArgs{"account": l.Operation.AccountID, "app": appID, "project": l.Operation.ProjectID, "environment": environmentID, "scope": scope}); err != nil {
					t.Fatal(err)
				}
				before, err := s.ProjectEnvironmentCloneEnvironmentBorrowersForLease(t.Context(), l)
				if err != nil || before.References[tc.table] != 1 {
					t.Fatal("cache borrower omitted", before, err)
				}
				if err = s.CleanupProjectEnvironmentCloneEnvironmentForLease(t.Context(), l); !errors.Is(err, state.ErrConflict) {
					t.Fatal("borrowed environment cleanup accepted", err)
				}
				var retained, receipt bool
				if err = pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM project_environments WHERE id=$1),EXISTS(SELECT 1 FROM project_environment_clone_environment_cleanups WHERE operation_id=$2)", native.Plan.EnvironmentID, l.Operation.ID).Scan(&retained, &receipt); err != nil || !retained || receipt {
					t.Fatal("cache borrower was cascaded or cleanup recorded", retained, receipt, err)
				}
				after, err := s.ProjectEnvironmentCloneEnvironmentBorrowersForLease(t.Context(), l)
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatal("rejected cleanup changed cache records", after, err)
				}
			})
		}
	}
}
