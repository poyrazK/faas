// adr: 20261023130958
package mcpmanagement

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBudgetActionHistoryStorageFailure(t *testing.T) {
	for _, afterWrite := range []bool{false, true} {
		t.Run(fmt.Sprintf("after-write=%t", afterWrite), func(t *testing.T) {
			f, _, opts := newBudgetFixture(t)
			store, path, key := testStateStore(t)
			dir := filepath.Dir(path)
			t.Cleanup(func() { _ = os.Rename(dir+"-moved", dir) })
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if afterWrite && r.Method == http.MethodPut {
					if err := os.Rename(dir, dir+"-moved"); err != nil {
						t.Error(err)
					}
				}
				f.serve(w, r)
			}))
			t.Cleanup(backend.Close)
			opts.APIBase, opts.planStore, opts.planNamespace = backend.URL, store, "failure-budget"
			client := api.NewClient(backend.URL, testToken)
			_, m, err := newManagementServer(client, opts)
			if err != nil {
				t.Fatal(err)
			}
			id := budgetHistoryPlan(t, m)
			if !afterWrite {
				if err := os.Rename(dir, dir+"-moved"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := m.applyRouteBudgets(t.Context(), applyArgs{PlanID: id}); err == nil || store.available() {
				t.Fatal("failed atomic persistence was reported as success")
			}
			if _, err := m.getAction(t.Context(), budgetHistorySelector(id)); err == nil {
				t.Fatal("uncommitted in-memory history escaped a poisoned store")
			}
			f.mu.Lock()
			writes := f.writes
			f.mu.Unlock()
			if (writes == 1) != afterWrite || writes > 1 {
				t.Fatal("a failed pre-write save allowed native mutation")
			}
			if err := os.Rename(dir+"-moved", dir); err != nil {
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
			opts.planStore = next
			_, restored, err := newManagementServer(client, opts)
			if err != nil {
				t.Fatal(err)
			}
			expected := "not_attempted"
			if afterWrite {
				expected = "outcome_unknown"
			}
			if actionDetail(t, restored, budgetHistorySelector(id)).Action.Apply.State != expected {
				t.Fatal("restart fabricated a receipt that never committed locally")
			}
		})
	}
}

func TestBudgetActionHistoryRejectsUnboundNativeReceipts(t *testing.T) {
	f, backend, opts := newBudgetFixture(t)
	store, _, _ := testStateStore(t)
	opts.planStore, opts.planNamespace = store, "invalid-budget-receipt"
	_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	id := budgetHistoryPlan(t, m)
	if _, err := m.applyRouteBudgets(t.Context(), applyArgs{PlanID: id}); err != nil {
		t.Fatal(err)
	}
	p := m.plans[id]
	original, err := f.store.GetEnvironmentRouteBudgets(t.Context(), f.account.ID, f.app.ID, "staging")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*api.EnvironmentRouteBudgets)
	}{
		{name: "app", mutate: func(o *api.EnvironmentRouteBudgets) { o.AppID, o.Config.AppID = "other", "other" }},
		{name: "environment", mutate: func(o *api.EnvironmentRouteBudgets) { o.EnvironmentID = "other" }},
		{name: "revision", mutate: func(o *api.EnvironmentRouteBudgets) { o.Revision++; o.Config.Revision++ }},
		{name: "parent-revision", mutate: func(o *api.EnvironmentRouteBudgets) { o.AppRevision++ }},
		{name: "source", mutate: func(o *api.EnvironmentRouteBudgets) { o.BudgetSource = "app_policy"; o.Config.Revision = o.AppRevision }},
		{name: "enabled", mutate: func(o *api.EnvironmentRouteBudgets) { o.Config.Enabled = false }},
		{name: "routes", mutate: func(o *api.EnvironmentRouteBudgets) { o.Config.Routes[0].MaxP95MS++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, _ := json.Marshal(original)
			var altered api.EnvironmentRouteBudgets
			if err := json.Unmarshal(encoded, &altered); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&altered)
			p.budgetResult = &altered
			_, phase := budgetActionPhase(p)
			if phase.State != "outcome_unknown" || phase.Receipt != nil {
				t.Fatal("an unbound native response was projected as a committed policy")
			}
			var saved map[string]storedPlan
			if err := store.read("plans/"+opts.planNamespace, &saved); err != nil {
				t.Fatal(err)
			}
			record := saved[id]
			record.BudgetResult = &altered
			saved[id] = record
			if err := store.update("plans/"+opts.planNamespace, saved); err != nil {
				t.Fatal(err)
			}
			if _, err := NewServer(api.NewClient(backend.URL, testToken), opts); err == nil {
				t.Fatal("restart exposed a cached receipt that differs from the reviewed request")
			}
		})
	}
}
