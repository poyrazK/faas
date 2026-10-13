package pgintegration_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

// Equal acknowledgement counts must not turn a replaced gateway roster into
// serving evidence. Exercise the SQL predicate directly so the database still
// rejects an authorized writer that forgets to reset the old acknowledgements.
func TestPgEnvironmentGitOpsServingRejectsStaleGatewayRoster(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	lease, requests := preparedRequestModeQualificationFixture(t, store)
	var definition api.EnvironmentDefinition
	if err := json.Unmarshal(lease.Revision.Definition, &definition); err != nil {
		t.Fatal(err)
	}
	desired, err := environmentsync.Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	plan := claimedIntentPlan(t, store, lease, desired)
	for _, request := range requests {
		completeRequestQualificationReceipts(t, store, request)
	}
	if _, activated, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan); err != nil || !activated {
		t.Fatalf("activate graph: activated=%t err=%v", activated, err)
	}
	for _, gateway := range []string{"edge-a", "edge-b"} {
		registerServingGateway(t, store, gateway)
	}
	receipt, serving, err := store.PrepareEnvironmentGitOpsWorkloadServing(t.Context(), lease, plan, []string{"edge-a", "edge-b"})
	if err != nil || serving || len(receipt.Routes) == 0 {
		t.Fatalf("prepare serving: receipt=%+v serving=%t err=%v", receipt, serving, err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	for _, setting := range []string{"gregale.gitops_lease", "gregale.gitops_serving"} {
		if _, err := tx.Exec(t.Context(), `SELECT set_config($1,$2,true)`, setting, lease.LeaseToken); err != nil {
			t.Fatal(err)
		}
	}
	for _, route := range receipt.Routes {
		for _, gateway := range []string{"edge-a", "edge-b"} {
			if _, err := tx.Exec(t.Context(), `INSERT INTO environment_workload_serving_route_acks
				(graph_id,route_generation,node_name) VALUES($1::uuid,$2,$3)`, receipt.GraphID, route.Generation, gateway); err != nil {
				t.Fatalf("insert complete original roster: %v", err)
			}
		}
	}
	var ready bool
	if err := tx.QueryRow(t.Context(), `SELECT environment_workload_serving_ready($1::uuid)`, receipt.GraphID).Scan(&ready); err != nil || !ready {
		t.Fatalf("complete original roster was not ready: ready=%t err=%v", ready, err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, setting := range []string{"gregale.gitops_lease", "gregale.gitops_serving"} {
		if _, err := tx.Exec(t.Context(), `SELECT set_config($1,$2,true)`, setting, lease.LeaseToken); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(t.Context(), `UPDATE environment_workload_serving_receipts
		SET expected_gateways=ARRAY['edge-a','edge-c']::text[] WHERE graph_id=$1::uuid`, receipt.GraphID); err != nil {
		t.Fatalf("replace roster under the current serving lease: %v", err)
	}
	if err := tx.QueryRow(t.Context(), `SELECT environment_workload_serving_ready($1::uuid)`, receipt.GraphID).Scan(&ready); err != nil || ready {
		t.Fatalf("old gateway counted for replacement roster: ready=%t err=%v", ready, err)
	}
	if _, err := tx.Exec(t.Context(), `UPDATE environment_workload_serving_receipts
		SET phase='served',served_at=clock_timestamp() WHERE graph_id=$1::uuid`, receipt.GraphID); err == nil {
		t.Fatal("database accepted serving with a stale gateway acknowledgement")
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}

	// The stored roster and its acknowledgements are still intact. A newly
	// registered gateway must nevertheless invalidate readiness at the SQL
	// boundary before the reconciler has reset route generations.
	registerServingGateway(t, store, "edge-c")
	if _, _, err := store.PrepareEnvironmentGitOpsWorkloadServing(t.Context(), lease, plan, []string{"edge-a", "edge-b"}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("preparation accepted a stale gateway roster: %v", err)
	}
	tx, err = pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, setting := range []string{"gregale.gitops_lease", "gregale.gitops_serving"} {
		if _, err := tx.Exec(t.Context(), `SELECT set_config($1,$2,true)`, setting, lease.LeaseToken); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.QueryRow(t.Context(), `SELECT environment_workload_serving_ready($1::uuid)`, receipt.GraphID).Scan(&ready); err != nil || ready {
		t.Fatalf("stored gateway roster survived a live roster change: ready=%t err=%v", ready, err)
	}
	if _, err := tx.Exec(t.Context(), `UPDATE environment_workload_serving_receipts
		SET phase='served',served_at=clock_timestamp() WHERE graph_id=$1::uuid`, receipt.GraphID); err == nil {
		t.Fatal("database accepted serving after the gateway roster changed")
	}
}
