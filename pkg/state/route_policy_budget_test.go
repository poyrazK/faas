package state_test

// ADR-447: consolidated budget apply, verification rollback and durable replay.

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRouteBudgetConsolidationTransactions(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		for _, scenario := range []string{"apply and replay", "changed option", "verification rollback"} {
			t.Run(backend+"/"+scenario, func(t *testing.T) {
				var store state.Store = state.NewMemStore()
				if backend == "pg" {
					store = routePolicyPgStore(t)
				}
				acct, app, request, _ := routeGroupFixture(t, store)
				doc := []byte(`{"openapi":"3.1.0","paths":{"/checkout/daily":{"post":{}},"/checkout/monthly":{"post":{}},"/health":{"get":{}}}}`)
				if err := store.UpsertDeploymentOpenAPIDoc(t.Context(), request.DeploymentID, acct.ID, app.ID, doc, "manual_upload", false); err != nil {
					t.Fatal(err)
				}
				request.Requirements.Groups[0].Require.Throttle = nil
				request.ConsolidateBudgets = true
				planner := routeGroupTestPlanner(request.RoutePolicyPlanRequest)
				tx := store.(state.RoutePolicyStore)
				plan, err := tx.PlanRoutePolicy(t.Context(), acct.ID, app.ID, request.RoutePolicyPlanRequest, planner)
				if err != nil || plan.Status != "ready" || len(plan.Changes) != 1 || plan.Changes[0].BudgetGroup != "checkout" {
					t.Fatalf("plan: %+v %v", plan, err)
				}
				request.ExpectedPlanSHA256 = plan.SHA256
				if scenario == "changed option" {
					request.ConsolidateBudgets = false
					planner = routeGroupTestPlanner(request.RoutePolicyPlanRequest)
				}
				if scenario == "verification rollback" {
					original, calls := planner, 0
					planner = func(snapshot state.RoutePolicySnapshot) (api.RoutePolicyPlan, error) {
						calls++
						if calls == 2 {
							return api.RoutePolicyPlan{}, errors.New("forced consolidated verification failure")
						}
						return original(snapshot)
					}
				}
				receipt, replayed, err := tx.ApplyRoutePolicy(t.Context(), acct.ID, app.ID, "consolidated", request, planner)
				if scenario != "apply and replay" {
					if err == nil || scenario == "changed option" && !errors.Is(err, state.ErrRoutePolicyStale) {
						t.Fatalf("unreviewed change: %v", err)
					}
					rules, _ := store.ListEdgeRulesForApp(t.Context(), app.ID)
					if len(rules) != 0 {
						t.Fatal("failed apply left rules")
					}
					return
				}
				if err != nil || replayed || len(receipt.Changes) != 1 || receipt.Verification.Status != "satisfied" {
					t.Fatalf("receipt: %+v %t %v", receipt, replayed, err)
				}
				for _, route := range receipt.Verification.Routes {
					if route.Method == "POST" && route.Checks[0].RuleIDs[0] != receipt.Changes[0].RuleID {
						t.Fatal("verification lost committed rule identity")
					}
				}
				if err := store.DeleteDeploymentOpenAPIDoc(t.Context(), request.DeploymentID, acct.ID); err != nil {
					t.Fatal(err)
				}
				second, replayed, err := tx.ApplyRoutePolicy(t.Context(), acct.ID, app.ID, "consolidated", request, planner)
				if err != nil || !replayed || second.ID != receipt.ID {
					t.Fatalf("recovery: %+v %t %v", second, replayed, err)
				}
				request.ConsolidateBudgets = false
				if _, _, err := tx.ApplyRoutePolicy(t.Context(), acct.ID, app.ID, "consolidated", request, planner); !errors.Is(err, state.ErrRoutePolicyKeyReused) {
					t.Fatalf("option missing from idempotency: %v", err)
				}
			})
		}
	}
}
