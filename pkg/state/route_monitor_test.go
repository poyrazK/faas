package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func monitorFixture(t *testing.T, store state.Store) (state.Account, state.App, api.RoutePolicyApplyRequest) {
	t.Helper()
	account, app, request, _, _ := savedCheckFixture(t, store)
	if err := store.MarkDeploymentLive(t.Context(), request.DeploymentID); err != nil {
		t.Fatal(err)
	}
	monitorApply(t, store, account, app, request, "initial")
	monitorComplete(t, store, "satisfied")
	return account, app, request
}

func monitorApply(t *testing.T, store state.Store, account state.Account, app state.App, request api.RoutePolicyApplyRequest, key string) {
	t.Helper()
	policy := store.(state.RoutePolicyStore)
	planner := routeGroupTestPlanner(request.RoutePolicyPlanRequest)
	plan, err := policy.PlanRoutePolicy(t.Context(), account.ID, app.ID, request.RoutePolicyPlanRequest, planner)
	if err != nil || plan.Status != "ready" {
		t.Fatalf("repair plan: %+v %v", plan, err)
	}
	request.ExpectedPlanSHA256 = plan.SHA256
	if _, _, err := policy.ApplyRoutePolicy(t.Context(), account.ID, app.ID, key, request, planner); err != nil {
		t.Fatal(err)
	}
}

func monitorComplete(t *testing.T, store state.Store, status string) state.AutomaticRouteCheckClaim {
	t.Helper()
	queue := store.(state.AutomaticRouteCheckStore)
	claim, err := queue.ClaimAutomaticRouteCheck(t.Context(), api.RouteCheckClaimLease)
	if err != nil || !finishAutomaticCheck(t, store, claim) {
		t.Fatalf("complete queued policy check: %+v %v", claim, err)
	}
	result, err := queue.GetAutomaticRouteCheck(t.Context(), claim.AccountID, claim.AppID, claim.DeploymentID, automaticCheckFingerprint)
	if err != nil || result.State != "complete" || result.Freshness != "current" || result.Check.Report.Status != status {
		t.Fatalf("current result: %+v %v", result, err)
	}
	return claim
}

func monitorBudgetRule(t *testing.T, store state.Store, appID string) state.EdgeRule {
	t.Helper()
	rules, err := store.ListEdgeRulesForApp(t.Context(), appID)
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range rules {
		if rule.Kind == state.EdgeRuleKindBudget {
			return rule
		}
	}
	t.Fatal("missing budget rule")
	return state.EdgeRule{}
}

func monitorHook(t *testing.T, store state.Store, accountID, appID, name string, events []string, enabled bool) state.AppWebhook {
	t.Helper()
	hook, err := store.CreateAppWebhook(t.Context(), state.AppWebhook{ID: uuid.NewString(), AccountID: accountID, AppID: appID,
		TargetURL: "https://example.test/" + name, SecretSealed: []byte("sealed"), Enabled: enabled, EventFilter: events})
	if err != nil {
		t.Fatal(err)
	}
	return hook
}

func monitorPendingEvents(t *testing.T, store state.Store, want int64) {
	t.Helper()
	health, err := store.(state.AppWebhookEventOutboxHealthStore).AppWebhookEventOutboxHealth(t.Context())
	if err != nil || health.PendingCount != want {
		t.Fatalf("pending events: %+v %v, want %d", health, err, want)
	}
}

func TestRoutePolicyMonitoringMutationQueues(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		for _, mutation := range []string{"rule_create", "quota_create", "rule_update", "rule_delete", "authentication", "budget", "rate_limit", "plan", "same_plan", "same_rule", "unrelated_manifest", "unrelated_app", "retired"} {
			t.Run(backend+"/"+mutation, func(t *testing.T) {
				var store state.Store = state.NewMemStore()
				if backend == "pg" {
					store = routePolicyPgStore(t)
				}
				account, app, request := monitorFixture(t, store)
				rule := monitorBudgetRule(t, store, app.ID)
				queue := store.(state.AutomaticRouteCheckStore)
				if _, err := queue.ClaimAutomaticRouteCheck(t.Context(), api.RouteCheckClaimLease); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("fixture has extra queued work")
				}
				var err error
				switch mutation {
				case "rule_create", "quota_create":
					in := state.CreateEdgeRuleParams{AccountID: account.ID, AppID: app.ID, MatchHost: app.Slug + ".gregale.dev", MatchPath: "/health", MatchMethods: []string{"GET"}, Kind: state.EdgeRuleKindBudget, Enabled: true, Action: state.EdgeRuleAction{Budget: &state.EdgeRuleBudgetAction{BudgetMs: 100}}}
					if mutation == "quota_create" {
						limits, _ := api.LimitsFor(account.Plan)
						_, err = store.CreateEdgeRuleIfUnderQuota(t.Context(), in, limits)
					} else {
						_, err = store.CreateEdgeRule(t.Context(), in)
					}
				case "rule_update":
					action := state.EdgeRuleAction{Budget: &state.EdgeRuleBudgetAction{BudgetMs: 900}}
					_, err = store.UpdateEdgeRule(t.Context(), rule.ID, state.UpdateEdgeRuleParams{Action: &action})
				case "rule_delete":
					err = store.DeleteEdgeRule(t.Context(), rule.ID)
				case "authentication":
					mode := api.ConsumerAuthModeRequired
					_, err = store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{SetConsumerAuthMode: true, ConsumerAuthMode: &mode})
				case "budget", "unrelated_manifest":
					manifest := app.Manifest
					if mutation == "budget" {
						manifest.RequestTimeoutS = 2
					} else {
						manifest.StartupDeadlineS = 100
					}
					_, err = store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &manifest})
				case "rate_limit":
					rps := 10
					_, err = store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{SetRequestRateLimitRPS: true, RequestRateLimitRPS: &rps})
				case "plan":
					err = store.UpdateAccountPlan(t.Context(), account.ID, api.PlanHobby)
				case "same_plan":
					err = store.UpdateAccountPlan(t.Context(), account.ID, account.Plan)
				case "same_rule":
					_, err = store.UpdateEdgeRule(t.Context(), rule.ID, state.UpdateEdgeRuleParams{Action: &rule.Action})
				case "unrelated_app":
					other, createErr := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "monitor-other", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1})
					if createErr != nil {
						t.Fatal(createErr)
					}
					mode := api.ConsumerAuthModeRequired
					_, err = store.UpdateApp(t.Context(), other.ID, state.UpdateAppParams{SetConsumerAuthMode: true, ConsumerAuthMode: &mode})
				case "retired":
					if err := store.MarkDeploymentSuperseded(t.Context(), request.DeploymentID); err != nil {
						t.Fatal(err)
					}
					err = store.DeleteEdgeRule(t.Context(), rule.ID)
				}
				if err != nil {
					t.Fatal(err)
				}
				claim, err := queue.ClaimAutomaticRouteCheck(t.Context(), api.RouteCheckClaimLease)
				if mutation == "same_plan" || mutation == "same_rule" || mutation == "unrelated_manifest" || mutation == "unrelated_app" || mutation == "retired" {
					if !errors.Is(err, state.ErrNotFound) {
						t.Fatalf("no-op/historical mutation queued work: %+v %v", claim, err)
					}
				} else if err != nil || claim.AppID != app.ID || claim.DeploymentID != request.DeploymentID {
					t.Fatalf("policy mutation lost its handoff: %+v %v", claim, err)
				}
			})
		}
	}
}

func TestRoutePolicyMonitoringDurableTransitions(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if backend == "pg" {
				store = routePolicyPgStore(t)
			}
			account, app, request := monitorFixture(t, store)
			queue := store.(state.AutomaticRouteCheckStore)
			events := []string{string(state.AppWebhookEventRouteRequirementsViolated), string(state.AppWebhookEventRouteRequirementsRecovered)}
			hook := monitorHook(t, store, account.ID, app.ID, "monitor", events, true)
			wildcard := monitorHook(t, store, account.ID, app.ID, "wildcard", nil, true)
			disabled := monitorHook(t, store, account.ID, app.ID, "disabled", events, false)
			filtered := monitorHook(t, store, account.ID, app.ID, "filtered", []string{"debug.regression.detected"}, true)
			other, _ := store.CreateAccount(t.Context(), "foreign-monitor@example.test", api.PlanPro)
			foreign := monitorHook(t, store, other.ID, app.ID, "foreign", events, true)
			monitorPendingEvents(t, store, 0)
			// Fence an already leased pass when a rule is deleted.
			if err := queue.QueueAutomaticRouteCheck(t.Context(), account.ID, app.ID, request.DeploymentID); err != nil {
				t.Fatal(err)
			}
			old, _ := queue.ClaimAutomaticRouteCheck(t.Context(), api.RouteCheckClaimLease)
			oldCheck, err := store.(state.RouteRequirementsStore).CheckRouteRequirements(t.Context(), account.ID, app.ID, api.CheckRouteRequirementsRequest{DeploymentID: request.DeploymentID}, testSavedRequirementsChecker(request.DeploymentID))
			if err != nil {
				t.Fatal(err)
			}
			rule := monitorBudgetRule(t, store, app.ID)
			if err := store.DeleteEdgeRule(t.Context(), rule.ID); err != nil {
				t.Fatal(err)
			}
			if done, err := queue.CompleteAutomaticRouteCheck(t.Context(), old, oldCheck, oldCheck.Report.Coverage.SHA256, false); err != nil || done {
				t.Fatalf("stale pass completed: %v %v", done, err)
			}
			violation := monitorComplete(t, store, "violated")
			monitorPendingEvents(t, store, 1)
			late := monitorHook(t, store, account.ID, app.ID, "late", events, true)
			if n, err := store.(state.AppWebhookEventOutboxStore).DrainAppWebhookEventOutbox(t.Context(), 32); err != nil || n != 1 {
				t.Fatalf("durable relay: %d %v", n, err)
			}
			monitorPendingEvents(t, store, 0)
			for _, excluded := range []state.AppWebhook{disabled, filtered, foreign, late} {
				rows, _, err := store.ListAppWebhookDeliveries(t.Context(), app.ID, excluded.ID, 100, "")
				if err != nil || len(rows) != 0 {
					t.Fatalf("wrong recipient %s: %+v %v", excluded.TargetURL, rows, err)
				}
			}
			for _, recipient := range []state.AppWebhook{hook, wildcard} {
				rows, _, err := store.ListAppWebhookDeliveries(t.Context(), app.ID, recipient.ID, 100, "")
				if err != nil || len(rows) != 1 || rows[0].Event != state.AppWebhookEventRouteRequirementsViolated {
					t.Fatalf("violation delivery: %+v %v", rows, err)
				}
				var payload map[string]any
				if json.Unmarshal(rows[0].Payload, &payload) != nil || payload["transition_id"] != violation.RequestID || payload["previous_status"] != "satisfied" || payload["status"] != "violated" || payload["result_path"] != "/v1/apps/"+app.Slug+"/route-requirements/checks/"+request.DeploymentID || strings.Contains(string(rows[0].Payload), "private") || strings.Contains(string(rows[0].Payload), "/checkout") {
					t.Fatalf("metadata/provenance: %s", rows[0].Payload)
				}
			}
			if err := queue.QueueAutomaticRouteCheck(t.Context(), account.ID, app.ID, request.DeploymentID); err != nil {
				t.Fatal(err)
			}
			monitorComplete(t, store, "violated")
			monitorPendingEvents(t, store, 0)
			if err := store.DeleteDeploymentOpenAPIDoc(t.Context(), request.DeploymentID, account.ID); err != nil {
				t.Fatal(err)
			}
			monitorComplete(t, store, "unknown")
			monitorPendingEvents(t, store, 0)
			if err := store.UpsertDeploymentOpenAPIDoc(t.Context(), request.DeploymentID, account.ID, app.ID, []byte(routeGroupContract), "manual_upload", false); err != nil {
				t.Fatal(err)
			}
			monitorComplete(t, store, "violated")
			monitorPendingEvents(t, store, 0)
			monitorApply(t, store, account, app, request, "repair")
			recovery := monitorComplete(t, store, "satisfied")
			monitorPendingEvents(t, store, 1)
			if n, err := store.(state.AppWebhookEventOutboxStore).DrainAppWebhookEventOutbox(t.Context(), 32); err != nil || n != 1 {
				t.Fatalf("recovery relay: %d %v", n, err)
			}
			rows, _, _ := store.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 100, "")
			if len(rows) != 2 {
				t.Fatalf("recovery after unknown lost: %+v", rows)
			}
			found := false
			for _, row := range rows {
				found = found || row.Event == state.AppWebhookEventRouteRequirementsRecovered && strings.Contains(string(row.Payload), recovery.RequestID)
			}
			if !found {
				t.Fatal("missing recovery transition identity")
			}
			if n, err := store.(state.AppWebhookEventOutboxStore).DrainAppWebhookEventOutbox(t.Context(), 32); err != nil || n != 0 {
				t.Fatal("repeated relay duplicated event")
			}
			rule = monitorBudgetRule(t, store, app.ID)
			if err := store.DeleteEdgeRule(t.Context(), rule.ID); err != nil {
				t.Fatal(err)
			}
			again := monitorComplete(t, store, "violated")
			if again.RequestID == violation.RequestID {
				t.Fatal("recurrence reused transition ID")
			}
			monitorPendingEvents(t, store, 1)
		})
	}
}

func TestRoutePolicyMonitoringEntitlementLossDoesNotRecover(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if backend == "pg" {
				store = routePolicyPgStore(t)
			}
			account, app, request := monitorFixture(t, store)
			queue := store.(state.AutomaticRouteCheckStore)
			passing, err := queue.GetAutomaticRouteCheck(t.Context(), account.ID, app.ID, request.DeploymentID, automaticCheckFingerprint)
			if err != nil {
				t.Fatal(err)
			}
			monitorHook(t, store, account.ID, app.ID, "entitlement", []string{"routes.requirements.violated", "routes.requirements.recovered"}, true)
			rule := monitorBudgetRule(t, store, app.ID)
			if err := store.DeleteEdgeRule(t.Context(), rule.ID); err != nil {
				t.Fatal(err)
			}
			monitorComplete(t, store, "violated")
			if n, err := store.(state.AppWebhookEventOutboxStore).DrainAppWebhookEventOutbox(t.Context(), 32); err != nil || n != 1 {
				t.Fatalf("initial violation relay: %d %v", n, err)
			}
			if err := store.UpdateAccountPlan(t.Context(), account.ID, api.PlanFree); err != nil {
				t.Fatal(err)
			}
			claim, err := queue.ClaimAutomaticRouteCheck(t.Context(), api.RouteCheckClaimLease)
			if err != nil {
				t.Fatal(err)
			}
			// Even a passing completion supplied to the store cannot notify or
			// clear a confirmed violation without current discovery entitlement.
			if done, err := queue.CompleteAutomaticRouteCheck(t.Context(), claim, *passing.Check, passing.Check.Report.Coverage.SHA256, false); err != nil || !done {
				t.Fatalf("entitlement completion: %v %v", done, err)
			}
			monitorPendingEvents(t, store, 0)
			if err := store.UpdateAccountPlan(t.Context(), account.ID, api.PlanPro); err != nil {
				t.Fatal(err)
			}
			monitorComplete(t, store, "violated")
			monitorPendingEvents(t, store, 0)
			monitorApply(t, store, account, app, request, "entitlement-repair")
			monitorComplete(t, store, "satisfied")
			monitorPendingEvents(t, store, 1)
		})
	}
}
