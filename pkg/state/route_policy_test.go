package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/routerequirements"
	"github.com/onebox-faas/faas/pkg/state"
)

func routePolicyPgStore(t *testing.T) *state.PgStore {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return state.NewPgStore(pool)
}

func routePolicyFixture(t *testing.T, store state.Store) (state.Account, state.App, api.RoutePolicyApplyRequest, state.RoutePolicyPlanner) {
	t.Helper()
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "route-policy@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "route-policy", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	config, err := routerequirements.Parse([]byte(`{"version":1,"routes":[{"method":"POST","path":"/checkout","require":{"throttle":{"key_by":"none","max_rps":1},"budget":{"max_ms":500}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	request := api.RoutePolicyApplyRequest{RoutePolicyPlanRequest: api.RoutePolicyPlanRequest{Requirements: config, ThrottleBurst: 10}, Confirm: true}
	planner := func(snapshot state.RoutePolicySnapshot) (api.RoutePolicyPlan, error) {
		limits, _ := api.LimitsFor(snapshot.Account.Plan)
		context := routerequirements.Context{Host: snapshot.App.Slug + ".gregale.dev", Rules: snapshot.Rules,
			App: api.AppResponse{ID: snapshot.App.ID, Slug: snapshot.App.Slug, ConsumerAuthMode: string(snapshot.App.ConsumerAuthMode),
				RequestTimeoutS: snapshot.App.Manifest.RequestTimeoutS, MaintenanceMode: snapshot.App.MaintenanceMode,
				EffectiveLimits: api.AppEffectiveLimits{RequestBudgetMS: limits.RequestBudgetForType(string(snapshot.App.Type)).Milliseconds(),
					RequestBudgetMaxMS: limits.RequestBudgetMaxDuration().Milliseconds(), AppRequestRateRPS: limits.RateLimitRPS, AppRequestBurst: limits.RateLimitBurst}}}
		return routerequirements.BuildServerPlan(config, context, routerequirements.PlanOptions{PlanName: string(snapshot.Account.Plan), ThrottleBurst: 10})
	}
	plan, err := store.(state.RoutePolicyStore).PlanRoutePolicy(ctx, acct.ID, app.ID, api.RoutePolicyPlanRequest{}, planner)
	if err != nil || plan.Status != "ready" || len(plan.Changes) != 2 {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	request.ExpectedPlanSHA256 = plan.SHA256
	return acct, app, request, planner
}

func TestRoutePolicyTransactions(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		for _, scenario := range []string{"concurrent retries", "updates", "stale rule", "stale account", "verification rollback", "ownership", "unresolved"} {
			t.Run(backend+"/"+scenario, func(t *testing.T) {
				var store state.Store = state.NewMemStore()
				if backend == "pg" {
					store = routePolicyPgStore(t)
				}
				acct, app, request, planner := routePolicyFixture(t, store)
				tx := store.(state.RoutePolicyStore)
				ctx := t.Context()
				switch scenario {
				case "updates":
					for _, kind := range []state.EdgeRuleKind{state.EdgeRuleKindThrottle, state.EdgeRuleKindBudget} {
						action := state.EdgeRuleAction{Budget: &state.EdgeRuleBudgetAction{BudgetMs: 2000}}
						if kind == state.EdgeRuleKindThrottle {
							action = state.EdgeRuleAction{Throttle: &state.EdgeRuleThrottleAction{RequestsPerSecond: 3, Burst: 7}}
						}
						if _, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: acct.ID, AppID: app.ID, MatchHost: app.Slug + ".gregale.dev", MatchPath: "/checkout", MatchMethods: []string{"POST"}, Priority: 0, Kind: kind, Enabled: true, Action: action}); err != nil {
							t.Fatal(err)
						}
					}
					plan, err := tx.PlanRoutePolicy(ctx, acct.ID, app.ID, api.RoutePolicyPlanRequest{}, planner)
					if err != nil {
						t.Fatal(err)
					}
					if plan.Status != "ready" {
						t.Fatalf("update plan: %+v", plan)
					}
					request.ExpectedPlanSHA256 = plan.SHA256
					receipt, _, err := tx.ApplyRoutePolicy(ctx, acct.ID, app.ID, "updates", request, planner)
					if err != nil {
						t.Fatal(err)
					}
					for _, change := range receipt.Changes {
						if change.Operation != "update" {
							t.Fatal("exact rule replaced instead of updated")
						}
						rule, err := store.GetEdgeRuleByID(ctx, change.RuleID)
						if err != nil || rule.Priority != 0 || rule.MatchPath != "/checkout" {
							t.Fatalf("selector changed: %+v %v", rule, err)
						}
						if rule.Kind == state.EdgeRuleKindThrottle && (rule.Action.Throttle.Burst != 7 || rule.Action.Throttle.RequestsPerSecond != 1) {
							t.Fatal("throttle update lost burst or rate")
						}
					}
					rules, _ := store.ListEdgeRulesForApp(ctx, app.ID)
					if len(rules) != 2 {
						t.Fatal("update created additional rules")
					}
				case "concurrent retries":
					var wg sync.WaitGroup
					receipts := make(chan api.RoutePolicyReceipt, 8)
					for range 8 {
						wg.Go(func() {
							receipt, _, err := tx.ApplyRoutePolicy(ctx, acct.ID, app.ID, "retry", request, planner)
							if err != nil {
								t.Errorf("apply: %v", err)
								return
							}
							receipts <- receipt
						})
					}
					wg.Wait()
					close(receipts)
					id := ""
					for receipt := range receipts {
						if id == "" {
							id = receipt.ID
						}
						if receipt.ID != id || len(receipt.Changes) != 2 || receipt.Verification.Status != "satisfied" || receipt.Verification.PolicyScope != "committed_app" {
							t.Fatalf("receipt=%+v", receipt)
						}
						for _, change := range receipt.Changes {
							rule, err := store.GetEdgeRuleByID(ctx, change.RuleID)
							if err != nil || rule.AppID != app.ID {
								t.Fatalf("actual rule missing: %+v %v", change, err)
							}
						}
					}
					rules, _ := store.ListEdgeRulesForApp(ctx, app.ID)
					if len(rules) != 2 {
						t.Fatalf("duplicated batch: %d", len(rules))
					}
					recovered, err := tx.GetRoutePolicyReceipt(ctx, acct.ID, app.ID, id)
					if err != nil || recovered.ID != id {
						t.Fatalf("recover: %v", err)
					}
					request.ThrottleBurst++
					if _, _, err := tx.ApplyRoutePolicy(ctx, acct.ID, app.ID, "retry", request, planner); !errors.Is(err, state.ErrRoutePolicyKeyReused) {
						t.Fatalf("key reuse: %v", err)
					}
				case "stale rule":
					_, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: acct.ID, AppID: app.ID, MatchHost: "route-policy.gregale.dev", MatchPath: "/other", Kind: state.EdgeRuleKindBudget, Enabled: true, Action: state.EdgeRuleAction{Budget: &state.EdgeRuleBudgetAction{BudgetMs: 2000}}})
					if err != nil {
						t.Fatal(err)
					}
					if _, _, err := tx.ApplyRoutePolicy(ctx, acct.ID, app.ID, "stale", request, planner); !errors.Is(err, state.ErrRoutePolicyStale) {
						t.Fatalf("stale: %v", err)
					}
					rules, _ := store.ListEdgeRulesForApp(ctx, app.ID)
					if len(rules) != 1 {
						t.Fatal("stale apply wrote rules")
					}
				case "stale account":
					if err := store.UpdateAccountPlan(ctx, acct.ID, api.PlanHobby); err != nil {
						t.Fatal(err)
					}
					if _, _, err := tx.ApplyRoutePolicy(ctx, acct.ID, app.ID, "stale", request, planner); !errors.Is(err, state.ErrRoutePolicyStale) {
						t.Fatalf("stale plan limits: %v", err)
					}
				case "verification rollback":
					failing := func(snapshot state.RoutePolicySnapshot) (api.RoutePolicyPlan, error) {
						if len(snapshot.Rules) > 0 {
							return api.RoutePolicyPlan{}, errors.New("forced verification failure")
						}
						return planner(snapshot)
					}
					if _, _, err := tx.ApplyRoutePolicy(ctx, acct.ID, app.ID, "rollback", request, failing); err == nil {
						t.Fatal("verification failure accepted")
					}
					rules, _ := store.ListEdgeRulesForApp(ctx, app.ID)
					if len(rules) != 0 {
						t.Fatal("partial changes escaped rollback")
					}
					if _, err := tx.FindRoutePolicyReceipt(ctx, acct.ID, app.ID, "rollback", request); !errors.Is(err, state.ErrNotFound) {
						t.Fatalf("failed apply left receipt: %v", err)
					}
					if _, _, err := tx.ApplyRoutePolicy(ctx, acct.ID, app.ID, "rollback", request, planner); err != nil {
						t.Fatalf("retry after rollback: %v", err)
					}
				case "ownership":
					other, err := store.CreateAccount(ctx, "other-route@example.com", api.PlanPro)
					if err != nil {
						t.Fatal(err)
					}
					if _, _, err := tx.ApplyRoutePolicy(ctx, other.ID, app.ID, "foreign", request, planner); !errors.Is(err, state.ErrNotFound) {
						t.Fatalf("cross-account: %v", err)
					}
				case "unresolved":
					blocked := func(snapshot state.RoutePolicySnapshot) (api.RoutePolicyPlan, error) {
						plan, err := planner(snapshot)
						plan.Status = "partial"
						return plan, err
					}
					if _, _, err := tx.ApplyRoutePolicy(ctx, acct.ID, app.ID, "partial", request, blocked); !errors.Is(err, state.ErrRoutePolicyUnresolved) {
						t.Fatalf("partial plan: %v", err)
					}
				}
			})
		}
	}
}

func TestRoutePolicyPostgresSQLFailureRollsBackBatch(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	ctx := t.Context()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	acct, app, request, planner := routePolicyFixture(t, store)
	// Reject the second insertion after the throttle has been written.
	if _, err := pool.Exec(ctx, "ALTER TABLE edge_rules ADD CONSTRAINT test_reject_budget CHECK (kind <> 'budget')"); err != nil {
		t.Fatal(err)
	}
	tx := state.RoutePolicyStore(store)
	if _, _, err := tx.ApplyRoutePolicy(ctx, acct.ID, app.ID, "sql-failure", request, planner); err == nil {
		t.Fatal("SQL failure accepted")
	}
	rules, _ := store.ListEdgeRulesForApp(ctx, app.ID)
	if len(rules) != 0 {
		t.Fatal("SQL batch was not atomic")
	}
	if _, err := tx.FindRoutePolicyReceipt(ctx, acct.ID, app.ID, "sql-failure", request); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("failed SQL left receipt: %v", err)
	}
}

func TestRoutePolicyReceiptSurvivesLaterPolicyChanges(t *testing.T) {
	store := state.NewMemStore()
	acct, app, request, planner := routePolicyFixture(t, store)
	receipt, _, err := store.ApplyRoutePolicy(context.Background(), acct.ID, app.ID, "durable", request, planner)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteEdgeRule(t.Context(), receipt.Changes[0].RuleID); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(receipt)
	replay, replayed, err := store.ApplyRoutePolicy(t.Context(), acct.ID, app.ID, "durable", request, planner)
	if err != nil || !replayed {
		t.Fatalf("replay: %v", err)
	}
	after, _ := json.Marshal(replay)
	if string(before) != string(after) {
		t.Fatal("receipt changed with live policy")
	}
}

func TestRoutePolicyPostgresApplyLocksConcurrentQuotaWriter(t *testing.T) {
	store := routePolicyPgStore(t)
	acct, app, request, planner := routePolicyFixture(t, store)
	locked, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	blocking := func(snapshot state.RoutePolicySnapshot) (api.RoutePolicyPlan, error) {
		once.Do(func() { close(locked); <-release })
		return planner(snapshot)
	}
	applied := make(chan error, 1)
	go func() {
		_, _, err := store.ApplyRoutePolicy(t.Context(), acct.ID, app.ID, "locked", request, blocking)
		applied <- err
	}()
	<-locked
	created := make(chan error, 1)
	limits, _ := api.LimitsFor(api.PlanPro)
	go func() {
		_, err := store.CreateEdgeRuleIfUnderQuota(t.Context(), state.CreateEdgeRuleParams{AccountID: acct.ID, AppID: app.ID,
			MatchHost: app.Slug + ".gregale.dev", MatchPath: "/other", Enabled: true, Kind: state.EdgeRuleKindBudget,
			Action: state.EdgeRuleAction{Budget: &state.EdgeRuleBudgetAction{BudgetMs: 2000}}}, limits)
		created <- err
	}()
	select {
	case err := <-created:
		close(release)
		t.Fatalf("quota writer bypassed transaction lock: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	if err := <-applied; err != nil {
		t.Fatal(err)
	}
	if err := <-created; err != nil {
		t.Fatal(err)
	}
}
