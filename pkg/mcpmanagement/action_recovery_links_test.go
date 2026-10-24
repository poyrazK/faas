// adr: 20261024180000
package mcpmanagement

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestActionRecoveryDiscoveryMCP(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		for _, kind := range []string{"default", "named", "environment", "environment-rollback"} {
			t.Run(protocol+"/"+kind, func(t *testing.T) {
				var cs *mcp.ClientSession
				var ctx context.Context
				var id string
				var selector any
				var page map[string]json.RawMessage
				var detail map[string]json.RawMessage
				var assertPassive func()
				switch kind {
				case "default":
					f, _, _ := newReleaseFixture(t, false)
					b := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
					t.Cleanup(b.Close)
					cs, ctx = connectConfiguredTestServer(t, b, Options{APIBase: b.URL, Token: testToken}, protocol)
					id = planID(t, callTool(t, ctx, cs, "gregale_plan_source_deploy", sourcePlanArgs(), false))
					selector = getActionArgs{App: "alpha", PlanID: id}
					page = callTool(t, ctx, cs, "gregale_list_actions", listActionsArgs{App: "alpha"}, false)
					detail = callTool(t, ctx, cs, "gregale_get_action", selector, false)
					assertPassive = func() {
						f.mu.Lock()
						defer f.mu.Unlock()
						if f.writes != 0 {
							t.Fatal("discovery performed native release")
						}
					}
				case "named":
					f, b, opts := newBudgetFixture(t)
					opts.AllowWrites = false
					cs, ctx = connectConfiguredTestServer(t, b, opts, protocol)
					id = planID(t, callTool(t, ctx, cs, "gregale_plan_route_budgets", budgetArgs(), false))
					selector = budgetHistorySelector(id)
					page = callTool(t, ctx, cs, "gregale_list_actions", listActionsArgs{App: "alpha", Project: "shop", Environment: "staging"}, false)
					detail = callTool(t, ctx, cs, "gregale_get_action", selector, false)
					assertPassive = func() {
						f.mu.Lock()
						defer f.mu.Unlock()
						if f.writes != 0 {
							t.Fatal("discovery changed budgets")
						}
					}
				default:
					f, b := newEnvironmentFixture(t)
					f.protected = true
					cs, ctx = connectConfiguredTestServer(t, b, environmentTestOptions(b, false), protocol)
					tool, args := "gregale_plan_environment_promotion", environmentPlanArgs()
					if kind == "environment-rollback" {
						tool, args = "gregale_plan_environment_rollback", map[string]any{"project": "shop", "environment": "production", "promotion_id": "promotion-one", "reason": "PRIVATE_ENV_REASON"}
					}
					id = planID(t, callTool(t, ctx, cs, tool, args, false))
					selector = getEnvironmentActionArgs{Project: "shop", Environment: "production", PlanID: id}
					page = callTool(t, ctx, cs, "gregale_list_environment_actions", listEnvironmentActionsArgs{Project: "shop", Environment: "production"}, false)
					detail = callTool(t, ctx, cs, "gregale_get_environment_action", selector, false)
					assertPassive = func() {
						f.mu.Lock()
						defer f.mu.Unlock()
						if f.writes != 0 || f.reviewRequests != 0 || f.approvalPolls != 0 {
							t.Fatal("discovery started native/approval actions")
						}
					}
				}
				var item struct {
					Recovery *actionRecoveryLinks `json:"recovery"`
					Review   *savedPlanReference  `json:"review"`
				}
				if json.Unmarshal(detail["action"], &item) != nil || item.Recovery == nil || item.Review == nil {
					t.Fatal("history omitted recovery discovery")
				}
				var rows []struct {
					Recovery *actionRecoveryLinks `json:"recovery"`
				}
				if json.Unmarshal(page["items"], &rows) != nil || len(rows) != 1 || !reflect.DeepEqual(rows[0].Recovery, item.Recovery) {
					t.Fatal("page/detail recovery links differ")
				}
				resource, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: item.Recovery.ResourceURI})
				if err != nil {
					t.Fatal(err)
				}
				recipe := recoveryResourceView(t, resource, item.Recovery.ResourceURI)
				if !reflect.DeepEqual(recipe.Workflow.Check, item.Recovery.Check) || !reflect.DeepEqual(recipe.Workflow.Review, *item.Review) {
					t.Fatal("discovered resource changed original calls")
				}
				encoded, _ := json.Marshal(item.Recovery.Prompt.Arguments)
				var promptArgs map[string]string
				if json.Unmarshal(encoded, &promptArgs) != nil {
					t.Fatal("prompt arguments are not exact string selectors")
				}
				prompt, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: item.Recovery.Prompt.Name, Arguments: promptArgs})
				if err != nil {
					t.Fatal(err)
				}
				savedPromptCalls(t, prompt, selector, kind == "environment" || kind == "environment-rollback")
				checked := callTool(t, ctx, cs, item.Recovery.Check.Tool, item.Recovery.Check.Arguments, false)
				if string(checked["plan_id"]) != `"`+id+`"` {
					t.Fatal("discovered check changed plan ID")
				}
				assertPassive()
			})
		}
	}
}

func TestActionRecoveryLinksExpireDuringHistoryRead(t *testing.T) {
	for _, family := range []string{"app", "environment"} {
		for _, page := range []bool{false, true} {
			for _, change := range []string{"expired", "removed", "binding"} {
				t.Run(family+map[bool]string{false: "/detail/", true: "/page/"}[page]+change, func(t *testing.T) {
					var m *management
					var id string
					var statePath string
					if family == "app" {
						f, b, opts := newBudgetFixture(t)
						store, path, _ := testStateStore(t)
						statePath = path
						opts.planStore, opts.planNamespace = store, "discovery-race"
						var err error
						_, m, err = newManagementServer(api.NewClient(b.URL, testToken), opts)
						if err != nil {
							t.Fatal(err)
						}
						id = budgetHistoryPlan(t, m)
						f.mu.Lock()
						f.ambiguous = true
						f.mu.Unlock()
						if _, err := m.applyRouteBudgets(t.Context(), applyArgs{PlanID: id}); err == nil {
							t.Fatal("expected ambiguous native outcome")
						}
					} else {
						f, b := newEnvironmentFixture(t)
						store, path, _ := testStateStore(t)
						statePath = path
						opts := environmentTestOptions(b, true)
						opts.planStore, opts.planNamespace = store, "discovery-race-env"
						m = newHistoryEnvironment(t, b, opts)
						id = planHistoryEnvironment(t, m, false)
						f.mu.Lock()
						f.ambiguous = true
						f.mu.Unlock()
						if applyHistoryEnvironment(t, m, id, false) == nil {
							t.Fatal("expected ambiguous native outcome")
						}
					}
					var value any
					var err error
					if family == "app" {
						if page {
							value, err = m.listActions(t.Context(), listActionsArgs{App: "alpha", Project: "shop", Environment: "staging"})
						} else {
							value, err = m.getAction(t.Context(), budgetHistorySelector(id))
						}
					} else {
						if page {
							value, err = m.listEnvironmentActions(t.Context(), listEnvironmentActionsArgs{Project: "shop", Environment: "production"})
						} else {
							value, err = m.getEnvironmentAction(t.Context(), getEnvironmentActionArgs{Project: "shop", Environment: "production", PlanID: id})
						}
					}
					if err != nil {
						t.Fatal(err)
					}
					guard := value.(*guardedRead)
					before, err := os.ReadFile(statePath)
					if err != nil {
						t.Fatal(err)
					}
					m.mu.Lock()
					if family == "app" {
						switch change {
						case "expired":
							m.plans[id].ExpiresAt = time.Now().Add(-time.Second)
						case "removed":
							delete(m.plans, id)
						case "binding":
							m.plans[id].Scope = "another-scope"
						}
					} else {
						switch change {
						case "expired":
							m.environmentPlans[id].ExpiresAt = time.Now().Add(-time.Second)
						case "removed":
							delete(m.environmentPlans, id)
						case "binding":
							m.environmentPlans[id].FromEnvironment = "another-source"
						}
					}
					m.mu.Unlock()
					if err := guard.check(t.Context()); err != nil {
						t.Fatal(err)
					}
					encoded, err := json.Marshal(guard.value)
					if err != nil {
						t.Fatal(err)
					}
					var payload struct {
						Action json.RawMessage   `json:"action"`
						Items  []json.RawMessage `json:"items"`
					}
					if json.Unmarshal(encoded, &payload) != nil {
						t.Fatal("invalid history snapshot")
					}
					row := payload.Action
					if page {
						if len(payload.Items) != 1 {
							t.Fatal("retained history disappeared")
						}
						row = payload.Items[0]
					}
					var actual struct {
						PlanAvailable bool        `json:"plan_available"`
						RetryTool     string      `json:"retry_tool"`
						NextStep      string      `json:"next_step"`
						Apply         actionPhase `json:"apply"`
					}
					if json.Unmarshal(row, &actual) != nil || actual.PlanAvailable || actual.RetryTool != "" || actual.Apply.State != "outcome_unknown" || actual.NextStep != "inspect_native_state_before_replanning" {
						t.Fatalf("expired discovery retained retry authority or lost uncertainty: %s", row)
					}
					if bytes.Contains(row, []byte(`"recovery"`)) || bytes.Contains(row, []byte(`"review"`)) {
						t.Fatal("unavailable plan advertised discovery links")
					}
					after, err := os.ReadFile(statePath)
					if err != nil || !bytes.Equal(before, after) {
						t.Fatal("history read persisted availability changes")
					}
				})
			}
		}
	}
}

func TestActionRecoveryDiscoveryRestartAndUnavailableSnapshot(t *testing.T) {
	f, b, opts := newBudgetFixture(t)
	store, path, key := testStateStore(t)
	opts.planStore, opts.planNamespace = store, "discovery-restart"
	_, m, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	id := budgetHistoryPlan(t, m)
	f.mu.Lock()
	f.ambiguous = true
	f.mu.Unlock()
	if _, err := m.applyRouteBudgets(t.Context(), applyArgs{PlanID: id}); err == nil {
		t.Fatal("expected lost response")
	}
	original := actionDetail(t, m, budgetHistorySelector(id)).Action.Recovery
	if original == nil {
		t.Fatal("ambiguous plan omitted review links")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = next.Close() })
	opts.planStore, opts.AllowWrites = next, false
	_, restored, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	recovered := actionDetail(t, restored, budgetHistorySelector(id)).Action
	if !reflect.DeepEqual(original, recovered.Recovery) || recovered.RetryTool != "" || recovered.Apply.State != "outcome_unknown" {
		t.Fatal("restart changed links or granted write authority")
	}
	restored.mu.Lock()
	p := restored.plans[id]
	delete(restored.plans, id)
	restored.mu.Unlock()
	value, err := restored.getAction(t.Context(), budgetHistorySelector(id))
	if err != nil {
		t.Fatal(err)
	}
	restored.mu.Lock()
	restored.plans[id] = p
	restored.mu.Unlock()
	guard := value.(*guardedRead)
	if err := guard.check(t.Context()); err != nil {
		t.Fatal(err)
	}
	snapshot := guard.value.(actionHistoryDetail).Action
	if snapshot.PlanAvailable || snapshot.Recovery != nil || snapshot.Review != nil {
		t.Fatal("unavailable snapshot silently acquired new discovery links")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writes != 1 {
		t.Fatal("history discovery retried native write")
	}
}

func TestActionRecoveryLinksRequireExactSafeSelectors(t *testing.T) {
	m := &management{opts: Options{Token: testToken}}
	for _, args := range []getActionArgs{
		{App: testToken, PlanID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{App: "alpha", PlanID: "bad"},
		{App: "alpha", Project: "shop", PlanID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
	} {
		if m.recoveryLinks(args, false) != nil {
			t.Fatal("history advertised a recovery selector its resource/prompt would reject")
		}
	}
}
