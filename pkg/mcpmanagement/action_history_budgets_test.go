// adr: 20261023130958
package mcpmanagement

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func budgetHistoryPlan(t *testing.T, m *management) string {
	t.Helper()
	value, err := m.planRouteBudgets(t.Context(), budgetPlanArgs{App: "alpha", Project: "shop", Environment: "staging", Reason: "PRIVATE_BUDGET_REASON", Enabled: true, Routes: []api.RouteMonitorRoute{{Method: "GET", Path: "/checkout", MaxP95MS: 500}}})
	if err != nil {
		t.Fatal(err)
	}
	return value.(releasePlan).ID
}

func budgetHistorySelector(id string) getActionArgs {
	return getActionArgs{App: "alpha", Project: "shop", Environment: "staging", PlanID: id}
}

func TestBudgetActionHistoryMCPReceipts(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(protocol, func(t *testing.T) {
			f, backend, opts := newBudgetFixture(t)
			cs, ctx := connectConfiguredTestServer(t, backend, opts, protocol)
			for index, inherit := range []bool{false, true} {
				args := budgetArgs()
				args["reason"] = "PRIVATE_BUDGET_REASON"
				change, source := "set_override", "environment_policy"
				if inherit {
					args["inherit"], args["enabled"], args["routes"] = true, false, []any{}
					change, source = "inherit_app_policy", "app_policy"
				}
				id := planID(t, callTool(t, ctx, cs, "gregale_plan_route_budgets", args, false))
				out := callTool(t, ctx, cs, "gregale_get_action", budgetHistorySelector(id), false)
				var pending actionView
				if json.Unmarshal(out["action"], &pending) != nil || pending.Apply.State != "not_attempted" || pending.Budget == nil || pending.Budget.Change != change || pending.Budget.ExpectedRevision != int64(index) || pending.Budget.ExpectedAppRevision != 0 {
					t.Fatalf("budget review metadata missing: %s", out)
				}
				f.mu.Lock()
				f.ambiguous = true
				f.mu.Unlock()
				callTool(t, ctx, cs, "gregale_apply_route_budgets", applyArgs{PlanID: id}, true)
				out = callTool(t, ctx, cs, "gregale_get_action", budgetHistorySelector(id), false)
				var unknown actionView
				if json.Unmarshal(out["action"], &unknown) != nil || unknown.Apply.State != "outcome_unknown" || unknown.Apply.Receipt != nil || unknown.RetryTool != "gregale_apply_route_budgets" {
					t.Fatalf("lost budget response fabricated a committed revision: %s", out)
				}
				callTool(t, ctx, cs, "gregale_apply_route_budgets", applyArgs{PlanID: id}, false)
				out = callTool(t, ctx, cs, "gregale_get_action", budgetHistorySelector(id), false)
				var committed actionView
				if json.Unmarshal(out["action"], &committed) != nil || committed.Apply.State != "committed" || committed.Apply.Receipt == nil {
					t.Fatalf("committed receipt missing: %s", out)
				}
				r := committed.Apply.Receipt
				if r.BudgetRevision == nil || *r.BudgetRevision != int64(index+1) || r.AppPolicyRevision == nil || *r.AppPolicyRevision != 0 || r.BudgetSource != source || committed.NextStep != "inspect_current_route_budgets" || committed.RetryTool != "" || string(out["native_completion"]) != `"not_observed"` {
					t.Fatalf("budget commit overstated observation or lost zero revision: %s", out)
				}
				observation := committed.Observation
				if observation == nil || observation.Tool != "gregale_get_route_budgets" || observation.Arguments.App != "alpha" || observation.Arguments.Project != "shop" || observation.Arguments.Environment != "staging" {
					t.Fatal("budget observation lost the original target")
				}
				callTool(t, ctx, cs, observation.Tool, observation.Arguments, false)
				page := callTool(t, ctx, cs, "gregale_list_actions", listActionsArgs{App: "alpha", Project: "shop", Environment: "staging"}, false)
				var items []actionView
				if json.Unmarshal(page["items"], &items) != nil || len(items) != index+1 {
					t.Fatal("budget plan is not discoverable")
				}
				encoded, _ := json.Marshal(out)
				for _, private := range []string{"PRIVATE_BUDGET_REASON", "/checkout", `"routes"`, `"enabled"`, `"config"`, `"request_id"`, `"deployment_id"`} {
					if strings.Contains(string(encoded), private) {
						t.Fatalf("history exposed excluded budget data: %s", private)
					}
				}
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			current, err := f.store.GetEnvironmentRouteBudgets(ctx, f.account.ID, f.app.ID, "staging")
			if err != nil || current.Revision != 2 || f.writes != 4 {
				t.Fatal("inspection or replay changed budget policy again")
			}
		})
	}
}

func TestBudgetActionHistoryRestartExpiryAndConsent(t *testing.T) {
	for _, mode := range []string{"recover", "expired", "read_only"} {
		t.Run(mode, func(t *testing.T) {
			f, backend, opts := newBudgetFixture(t)
			store, path, key := testStateStore(t)
			opts.planStore, opts.planNamespace = store, "budget-history"
			client := api.NewClient(backend.URL, testToken)
			_, m, err := newManagementServer(client, opts)
			if err != nil {
				t.Fatal(err)
			}
			id := budgetHistoryPlan(t, m)
			original := m.plans[id].Budgets.Proposed
			f.mu.Lock()
			f.ambiguous = true
			f.mu.Unlock()
			if _, err := m.applyRouteBudgets(t.Context(), applyArgs{PlanID: id}); err == nil {
				t.Fatal("lost-response fixture succeeded")
			}
			if mode == "expired" {
				m.plans[id].ExpiresAt = time.Now().Add(-time.Second)
				if err := m.persistPlans(); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			next, err := OpenStateStore(path, key)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = next.Close() })
			opts.planStore, opts.AllowWrites = next, mode != "read_only"
			_, restored, err := newManagementServer(client, opts)
			if err != nil {
				t.Fatal(err)
			}
			args := budgetHistorySelector(id)
			detail := actionDetail(t, restored, args)
			if detail.Action.Apply.State != "outcome_unknown" || detail.Action.PlanAvailable != (mode != "expired") || (detail.Action.RetryTool != "") != (mode == "recover") {
				t.Fatal("restart changed uncertainty, consent or expiry")
			}
			if mode == "recover" {
				if !reflect.DeepEqual(original, restored.plans[id].Budgets.Proposed) {
					t.Fatal("restart changed the native request or replay key")
				}
				for range 2 {
					if _, err := restored.applyRouteBudgets(t.Context(), applyArgs{PlanID: id}); err != nil {
						t.Fatal(err)
					}
				}
				if actionDetail(t, restored, args).Action.Apply.State != "committed" {
					t.Fatal("original replay receipt was not recovered")
				}
			} else if _, err := restored.applyRouteBudgets(t.Context(), applyArgs{PlanID: id}); err == nil {
				t.Fatal("metadata restored write authority")
			}
			// Read consent and immutable identities still apply to archived records.
			if _, err := restored.getAction(t.Context(), getActionArgs{App: "alpha", PlanID: id}); err == nil {
				t.Fatal("named history leaked into default environment")
			}
			result, err := restored.getAction(t.Context(), args)
			if err != nil {
				t.Fatal(err)
			}
			grant := restored.opts.Environments.Named["shop/staging"]
			delete(restored.opts.Environments.Named, "shop/staging")
			if result.(*guardedRead).check(t.Context()) == nil {
				t.Fatal("in-flight consent removal released history")
			}
			restored.opts.Environments.Named["shop/staging"] = grant
			f.mu.Lock()
			f.env.ID = "recreated-environment"
			f.mu.Unlock()
			if _, err := restored.getAction(t.Context(), args); err == nil {
				t.Fatal("recreated environment inherited history")
			}
			opts.planNamespace = "other-grant"
			_, other, err := newManagementServer(client, opts)
			if err != nil || len(other.actions) != 0 {
				t.Fatal("another grant inherited budget history")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			current, err := f.store.GetEnvironmentRouteBudgets(t.Context(), f.account.ID, f.app.ID, "staging")
			if err != nil || current.Revision != 1 || f.writes != map[string]int{"recover": 2, "expired": 1, "read_only": 1}[mode] {
				t.Fatal("recovery changed committed policy or an unauthorized write ran")
			}
		})
	}
}

func TestBudgetActionHistoryRevisionConflicts(t *testing.T) {
	for _, policy := range []string{"environment", "app"} {
		t.Run(policy, func(t *testing.T) {
			f, backend, opts := newBudgetFixture(t)
			_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			id := budgetHistoryPlan(t, m)
			if policy == "environment" {
				newer := budgetHistoryPlan(t, m)
				_, err = m.applyRouteBudgets(t.Context(), applyArgs{PlanID: newer})
			} else {
				revision := int64(0)
				_, err = f.store.SetRouteMonitor(t.Context(), f.account.ID, f.app.ID, api.SetRouteMonitorRequest{ExpectedRevision: &revision, Enabled: true, Routes: []api.RouteMonitorRoute{{Method: "GET", Path: "/checkout", MaxP95MS: 750}}})
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.applyRouteBudgets(t.Context(), applyArgs{PlanID: id}); err == nil {
				t.Fatal("stale plan overwrote newer policy")
			}
			detail := actionDetail(t, m, budgetHistorySelector(id))
			if detail.Action.Apply.State != "outcome_unknown" || detail.Action.Apply.Receipt != nil {
				t.Fatal("a revision conflict was reported as a committed budget")
			}
		})
	}
}

func TestBudgetActionHistoryCompatibilityCapacityAndRevocation(t *testing.T) {
	_, backend, opts := newBudgetFixture(t)
	store, _, _ := testStateStore(t)
	opts.planStore, opts.planNamespace = store, "compat-budget"
	client := api.NewClient(backend.URL, testToken)
	_, m, err := newManagementServer(client, opts)
	if err != nil {
		t.Fatal(err)
	}
	id := budgetHistoryPlan(t, m)
	var before actionHistorySnapshot
	if err := store.read(budgetActionsNamespace+opts.planNamespace, &before); err != nil || len(before.Items) != 1 {
		t.Fatal("budget history missing from companion namespace")
	}
	// N-1 writes known formats and ignores the new companion namespace.
	now := time.Now().UTC()
	legacy := actionHistorySnapshot{Version: 1, Items: make(map[string]actionRecord)}
	for i := range api.ManagementMCPActionHistoryMax {
		p := &releasePlan{ID: fmt.Sprintf("%064x", i+1), Action: "rollback", APIBase: backend.URL, AccountID: "account-one", App: "alpha", AppID: "app-one", Scope: "default", ExpiresAt: now.Add(-time.Minute)}
		e := recordAction(p)
		e.RecordedAt, e.UpdatedAt = now.Add(-time.Hour-time.Duration(i)*time.Second), now.Add(-time.Minute)
		legacy.Items[p.ID] = e
	}
	if err := store.update("actions/"+opts.planNamespace, legacy); err != nil {
		t.Fatal(err)
	}
	_, m, err = newManagementServer(client, opts)
	if err != nil || len(m.actions) != api.ManagementMCPActionHistoryMax || m.actions[id].PlanID != id {
		t.Fatal("combined bound lost the live budget plan")
	}
	if _, err := m.applyRouteBudgets(t.Context(), applyArgs{PlanID: id}); err != nil {
		t.Fatal(err)
	}
	// An older bridge can update a live replay receipt but leave history stale.
	if err := store.update(budgetActionsNamespace+opts.planNamespace, before); err != nil {
		t.Fatal(err)
	}
	_, upgraded, err := newManagementServer(client, opts)
	if err != nil {
		t.Fatal(err)
	}
	detail := actionDetail(t, upgraded, budgetHistorySelector(id))
	if detail.Action.Apply.State != "committed" || !detail.Action.RecordedAt.Equal(before.Items[id].RecordedAt) {
		t.Fatal("upgrade lost original record or native receipt")
	}
	if err := store.update("oauth", oauthSnapshot{}); err != nil {
		t.Fatal(err)
	}
	var removed actionHistorySnapshot
	if err := store.read(budgetActionsNamespace+opts.planNamespace, &removed); err != nil || len(removed.Items) != 0 {
		t.Fatal("revocation retained budget history")
	}
}

func TestBudgetActionHistoryRejectsMalformedSnapshots(t *testing.T) {
	now := time.Now().UTC()
	entry := actionRecord{PlanID: strings.Repeat("a", 64), Action: "route_budgets", APIBase: "https://api.example", AccountID: "account-one", App: "alpha", AppID: "app-one", Scope: "default", RecordedAt: now, UpdatedAt: now, PlanExpiresAt: now.Add(time.Minute), RetainedUntil: now.Add(time.Minute + api.ManagementMCPActionHistoryRetention), Budget: &actionBudget{Change: "set_override"}, Apply: actionPhase{State: "committed", Receipt: &actionReceipt{BudgetRevision: new(int64(1)), AppPolicyRevision: new(int64(0)), BudgetSource: "environment_policy"}}}
	for _, tc := range []struct {
		name      string
		namespace string
		version   int
		mutate    func(*actionRecord)
	}{
		{name: "future-version", namespace: budgetActionsNamespace, version: 2},
		{name: "legacy-namespace", namespace: "actions/", version: 1},
		{name: "workload-namespace", namespace: workloadActionsNamespace, version: 1},
		{name: "missing-review", mutate: func(e *actionRecord) { e.Budget = nil }},
		{name: "wrong-revision", mutate: func(e *actionRecord) { e.Apply.Receipt.BudgetRevision = new(int64(2)) }},
		{name: "wrong-app-revision", mutate: func(e *actionRecord) { e.Apply.Receipt.AppPolicyRevision = new(int64(1)) }},
		{name: "wrong-source", mutate: func(e *actionRecord) { e.Apply.Receipt.BudgetSource = "app_policy" }},
		{name: "deployment-receipt", mutate: func(e *actionRecord) { e.Apply.Receipt.DeploymentID = "deployment-one" }},
		{name: "accepted", mutate: func(e *actionRecord) { e.Apply.State = "accepted" }},
		{name: "unknown-with-receipt", mutate: func(e *actionRecord) { e.Apply.State = "outcome_unknown" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _, _ := testStateStore(t)
			encoded, _ := json.Marshal(entry)
			var altered actionRecord
			if err := json.Unmarshal(encoded, &altered); err != nil {
				t.Fatal(err)
			}
			if tc.mutate != nil {
				tc.mutate(&altered)
			}
			namespace, version := tc.namespace, tc.version
			if namespace == "" {
				namespace, version = budgetActionsNamespace, 1
			}
			if err := store.update(namespace+"invalid", actionHistorySnapshot{Version: version, Items: map[string]actionRecord{altered.PlanID: altered}}); err != nil {
				t.Fatal(err)
			}
			if _, err := NewServer(api.NewClient(entry.APIBase, testToken), Options{APIBase: entry.APIBase, planStore: store, planNamespace: "invalid"}); err == nil {
				t.Fatal("invalid budget history was exposed")
			}
		})
	}
}
