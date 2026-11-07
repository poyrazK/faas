package state_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261028110044 amendment 6: a removed operation's traffic counts under
// every label the gateway can give it, and labels of operations the candidate
// keeps never count against it.
func TestRouteRemovalPGObservedUnderEveryLabel(t *testing.T) {
	store, pool, ctx, account, app, baseline, candidate, policy := routeRemovalFixture(t)
	removed := `[{"method":"GET","path":"/users/{userId}/"},{"method":"DELETE","path":"/items/{id}"},{"method":"GET","path":"/v1.0/x"}]`
	retained := `{"openapi":"3.0.3","paths":{"/users/me":{"get":{}},"/items/bulk":{"delete":{}}}}`
	for _, tc := range []struct {
		method, route string
		want          bool
	}{
		{"GET", "GET /users/{userId}", true},
		{"GET", "GET /users/{id}", true},
		{"GET", "GET /users/alice-smith", true},
		{"GET", "/users/alice", true},
		{"HEAD", "HEAD /users/{userId}", true},
		{"HEAD", "HEAD /users/alice", true},
		{"DELETE", "DELETE /items/{id}", true},
		{"GET", "GET /v1.0/x", true},
		{"GET", "GET /users/me", false},
		{"HEAD", "HEAD /users/me", false},
		{"DELETE", "DELETE /items/bulk", false},
		{"POST", "POST /users/alice", false},
		{"GET", "GET /users/alice/posts", false},
		{"GET", "GET /v1x0/x", false},
		{"GET", "__route_other__", false},
	} {
		if _, err := pool.Exec(ctx, `DELETE FROM request_telemetry WHERE app_id=$1`, app.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO request_telemetry(account_id,app_id,deployment_id,method,route,status,latency_ms) VALUES($1,$2,$3,$4,$5,200,1)`, account.ID, app.ID, baseline.ID, tc.method, tc.route); err != nil {
			t.Fatal(err)
		}
		var observed bool
		if err := pool.QueryRow(ctx, `SELECT route_removal_route_observed($1,$2,$3::jsonb,$4::jsonb,clock_timestamp()-interval '1 hour')`, app.ID, account.ID, removed, retained).Scan(&observed); err != nil {
			t.Fatal(err)
		}
		if observed != tc.want {
			t.Fatalf("%s %q observed=%v, want %v", tc.method, tc.route, observed, tc.want)
		}
	}
	if _, err := pool.Exec(ctx, `DELETE FROM request_telemetry WHERE app_id=$1`, app.ID); err != nil {
		t.Fatal(err)
	}

	// The fixture removes GET /old: a HEAD request to it blocks cutover and a
	// fresh approval alike.
	approval := routeRemovalApprove(t, store, pool, ctx, account, app, baseline, candidate, policy)
	if _, err := pool.Exec(ctx, `INSERT INTO request_telemetry(account_id,app_id,deployment_id,method,route,status,latency_ms) VALUES($1,$2,$3,'HEAD','HEAD /old',200,1)`, account.ID, app.ID, baseline.ID); err != nil {
		t.Fatal(err)
	}
	check, err := store.CheckRouteRemoval(ctx, account.ID, app.ID, candidate.ID)
	if err != nil || check.Status != "blocked" || !strings.Contains(strings.Join(check.Blockers, ","), "old_route_observed") {
		t.Fatalf("cutover missed HEAD traffic to a removed GET route: %+v %v", check, err)
	}
	revision := policy.Revision
	_, err = store.ApproveRouteRemoval(ctx, account.ID, app.ID, "owner:test", api.ApproveRouteRemovalRequest{ExpectedPolicyRevision: &revision, BaselineDeploymentID: baseline.ID, CandidateDeploymentID: candidate.ID, BaselineContractSHA256: approval.BaselineContractSHA256, CandidateContractSHA256: approval.CandidateContractSHA256, Mappings: approval.Mappings, AcknowledgeObservedOnly: true}, func(state.RouteRemovalContract, state.RouteRemovalContract, []api.RouteRemovalMapping) error {
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "old_route_observed") {
		t.Fatalf("approval missed HEAD traffic to a removed GET route: %v", err)
	}
}

func TestRouteRemovalPGBaselineContractForGateway(t *testing.T) {
	store, _, ctx, account, app, _, _, _ := routeRemovalFixture(t)
	doc, err := store.RouteRemovalBaselineOpenAPIDoc(ctx, app.ID, account.ID)
	if err != nil || !strings.Contains(string(doc), `"/old"`) {
		t.Fatalf("baseline contract: %s %v", doc, err)
	}
	if _, err := store.RouteRemovalBaselineOpenAPIDoc(ctx, app.ID, "00000000-0000-0000-0000-000000000000"); err != state.ErrNotFound {
		t.Fatalf("foreign account read a baseline: %v", err)
	}
}
