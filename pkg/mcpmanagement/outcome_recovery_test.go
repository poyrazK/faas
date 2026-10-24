// adr: 20261024223000
package mcpmanagement

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestOutcomeRecoveryDiscoveryExpiredRestartMCP(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		for _, scope := range []string{"default", "named"} {
			t.Run(protocol+"/"+scope, func(t *testing.T) {
				f, b, opts := newBudgetFixture(t)
				store, path, key := testStateStore(t)
				opts.planStore, opts.planNamespace = store, "outcome-discovery"
				opts.Environments.DefaultWrite = true
				_, m, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
				if err != nil {
					t.Fatal(err)
				}
				args := budgetPlanArgs{App: "alpha", Reason: "PRIVATE_OUTCOME_REASON", Inherit: true, Routes: []api.RouteMonitorRoute{}}
				selector := getActionArgs{App: "alpha"}
				if scope == "named" {
					args.Project, args.Environment = "shop", "staging"
					selector.Project, selector.Environment = "shop", "staging"
				}
				plan, err := m.planRouteBudgets(t.Context(), args)
				if err != nil {
					t.Fatal(err)
				}
				id := plan.(releasePlan).ID
				selector.PlanID = id
				f.mu.Lock()
				f.ambiguous = true
				f.mu.Unlock()
				if _, err := m.applyRouteBudgets(t.Context(), applyArgs{PlanID: id}); err == nil {
					t.Fatal("lost reply succeeded")
				}
				later := m.plans[id].Budgets.Proposed
				revision := int64(1)
				later.RequestID, later.ExpectedRevision, later.Inherit, later.Enabled, later.Routes = uuid.NewString(), &revision, false, true, []api.RouteMonitorRoute{{Method: "GET", Path: "/later-policy", MaxP95MS: 750}}
				if _, err := f.store.SetEnvironmentRouteBudgets(t.Context(), f.account.ID, f.app.ID, m.plans[id].Scope, later); err != nil {
					t.Fatal(err)
				}
				m.plans[id].ExpiresAt = time.Now().Add(-time.Minute)
				if err := m.persistPlans(); err != nil {
					t.Fatal(err)
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
				cs, ctx := connectConfiguredTestServer(t, b, opts, protocol)
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				detail := callTool(t, ctx, cs, "gregale_get_action", selector, false)
				var action actionView
				if json.Unmarshal(detail["action"], &action) != nil || action.PlanAvailable || action.Review != nil || action.RetryTool != "" || action.Apply.State != "outcome_unknown" || action.Recovery == nil || action.Recovery.Outcome == nil || action.Recovery.Check.Tool != "" {
					t.Fatalf("invalid expired recovery: %s", detail)
				}
				page := callTool(t, ctx, cs, "gregale_list_actions", listActionsArgs{App: selector.App, Project: selector.Project, Environment: selector.Environment}, false)
				var items []actionView
				if json.Unmarshal(page["items"], &items) != nil || len(items) != 1 || !reflect.DeepEqual(items[0].Recovery, action.Recovery) {
					t.Fatal("list/detail discovery differs")
				}
				resource, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: action.Recovery.ResourceURI})
				if err != nil {
					t.Fatal(err)
				}
				recipe := recoveryResourceView(t, resource, action.Recovery.ResourceURI)
				workflow := recipe.Workflow
				if workflow.Outcome == nil || workflow.Observation == nil || !reflect.DeepEqual(workflow.Outcome, action.Recovery.Outcome) || workflow.Review.Tool != "" || workflow.Check.Tool != "" || strings.Contains(resource.Contents[0].Text, `"review":`) || strings.Contains(resource.Contents[0].Text, `"check":`) {
					t.Fatal("expired recipe offered live-plan calls")
				}
				encoded, _ := json.Marshal(action.Recovery.Prompt.Arguments)
				var promptArgs map[string]string
				if json.Unmarshal(encoded, &promptArgs) != nil {
					t.Fatal("invalid selectors")
				}
				prompt, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: action.Recovery.Prompt.Name, Arguments: promptArgs})
				if err != nil {
					t.Fatal(err)
				}
				text := prompt.Messages[0].Content.(*mcp.TextContent).Text
				for _, rule := range []string{"gregale_get_action_outcome", "never proves non-commit", "inspect current policy", "untrusted data"} {
					if !strings.Contains(text, rule) {
						t.Fatalf("prompt lost %s", rule)
					}
				}
				after, _ := os.ReadFile(path)
				if !bytes.Equal(before, after) || f.receiptReads != 0 || f.writes != 1 {
					t.Fatal("recipe retrieval fetched receipts, persisted state or replayed write")
				}
				outcome := callTool(t, ctx, cs, workflow.Outcome.Tool, workflow.Outcome.Arguments, false)
				if string(outcome["outcome"]) != `"verified_receipt"` || string(outcome["native_completion"]) != `"not_observed"` {
					t.Fatal("discovered lookup lost historical receipt")
				}
				observed := callTool(t, ctx, cs, workflow.Observation.Tool, workflow.Observation.Arguments, false)
				if string(observed["revision"]) != "2" || f.receiptReads != 1 || f.writes != 1 {
					t.Fatal("observation or outcome performed another write")
				}
				final, _ := os.ReadFile(path)
				if !bytes.Equal(before, final) {
					t.Fatal("following reads changed state")
				}
				callTool(t, ctx, cs, "gregale_get_plan", selector, true)
				callTool(t, ctx, cs, "gregale_check_plan", selector, true)
			})
		}
	}
}

func TestOutcomeRecoveryFinalGuardsAndNoUpgrades(t *testing.T) {
	for _, change := range []string{"lookup_removed", "lookup_changed", "history_removed", "history_expired", "history_binding", "consent", "environment", "storage"} {
		t.Run(change, func(t *testing.T) {
			f, b, opts := newBudgetFixture(t)
			store, _, _ := testStateStore(t)
			opts.planStore, opts.planNamespace = store, "outcome-guards"
			_, m, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			id := budgetHistoryPlan(t, m)
			m.plans[id].attempted = true
			m.plans[id].ExpiresAt = time.Now().Add(-time.Minute)
			if err := m.persistPlans(); err != nil {
				t.Fatal(err)
			}
			history, err := m.getAction(t.Context(), budgetHistorySelector(id))
			if err != nil {
				t.Fatal(err)
			}
			recipe, err := m.savedPlanRecoveryResource(t.Context(), budgetHistorySelector(id), false)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "lookup_removed":
				delete(m.budgetOutcomes, id)
			case "lookup_changed":
				lookup := m.budgetOutcomes[id]
				lookup.RequestID = uuid.NewString()
				m.budgetOutcomes[id] = lookup
			case "history_removed":
				delete(m.actions, id)
			case "history_expired":
				entry := m.actions[id]
				entry.RetainedUntil = time.Now().Add(-time.Minute)
				m.actions[id] = entry
			case "history_binding":
				entry := m.actions[id]
				entry.AppID = uuid.NewString()
				m.actions[id] = entry
			case "consent":
				delete(m.opts.Environments.Named, "shop/staging")
			case "environment":
				f.mu.Lock()
				f.env.ID = uuid.NewString()
				f.mu.Unlock()
			case "storage":
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := checkedResourceValue(t.Context(), recipe); err == nil {
				t.Fatal("final guard released stale outcome recipe")
			}
			guard := history.(*guardedRead)
			if err := guard.check(t.Context()); err == nil && change != "lookup_changed" && guard.value.(actionHistoryDetail).Action.Recovery != nil {
				t.Fatal("history retained unavailable lookup links")
			}
			if f.receiptReads != 0 || f.writes != 0 {
				t.Fatal("discovery accessed native outcome or mutation")
			}
		})
	}
	// A reference appearing after the initial snapshot never upgrades discovery.
	_, b, opts := newBudgetFixture(t)
	_, m, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	id := budgetHistoryPlan(t, m)
	m.plans[id].attempted = true
	if err := m.persistPlans(); err != nil {
		t.Fatal(err)
	}
	lookup := m.budgetOutcomes[id]
	delete(m.budgetOutcomes, id)
	delete(m.plans, id)
	value, err := m.getAction(t.Context(), budgetHistorySelector(id))
	if err != nil {
		t.Fatal(err)
	}
	m.budgetOutcomes[id] = lookup
	guard := value.(*guardedRead)
	if err := guard.check(t.Context()); err != nil {
		t.Fatal(err)
	}
	if guard.value.(actionHistoryDetail).Action.Recovery != nil {
		t.Fatal("snapshot acquired a later lookup")
	}
}

func TestOutcomeRecoveryUnavailablePlanLinksRemainUsable(t *testing.T) {
	for _, change := range []string{"removed", "binding"} {
		t.Run(change, func(t *testing.T) {
			f, b, opts := newBudgetFixture(t)
			_, m, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			id := budgetHistoryPlan(t, m)
			m.plans[id].attempted = true
			if err := m.persistPlans(); err != nil {
				t.Fatal(err)
			}
			if change == "removed" {
				delete(m.plans, id)
			} else {
				m.plans[id].Scope = "different-scope"
			}
			action := actionDetail(t, m, budgetHistorySelector(id)).Action
			if action.PlanAvailable || action.Recovery == nil || action.Recovery.Outcome == nil || action.Recovery.Check.Tool != "" {
				t.Fatal("unavailable plan lost independent receipt lookup")
			}
			uri := action.Recovery.ResourceURI
			resource, err := m.readSavedPlanResource(t.Context(), &mcp.ReadResourceRequest{Params: &mcp.ReadResourceParams{URI: uri}})
			if err != nil {
				t.Fatal(err)
			}
			recipe := recoveryResourceView(t, resource, uri)
			if recipe.Workflow.Outcome == nil || recipe.Workflow.Review.Tool != "" || recipe.Workflow.Check.Tool != "" || f.writes != 0 || f.receiptReads != 0 {
				t.Fatal("discovered recipe restored invalid plan or fetched receipt")
			}
		})
	}
}
