package financialtest

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

type financialBudgetTestStore interface {
	state.Store
	state.FinancialBudgetStore
}

// adr: 431 — customer intent must be atomic, revisioned and account owned.
func TestFinancialBudgetStores(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store financialBudgetTestStore = state.NewMemStore()
			if backend == "postgres" {
				pg, _, _ := financialPostgres(t)
				store = pg
			}
			t.Run("revision_and_ownership", func(t *testing.T) { financialBudgetRevisionSuite(t, store) })
			t.Run("scope_and_action", func(t *testing.T) { financialBudgetScopeSuite(t, store) })
			t.Run("concurrent_capacity", func(t *testing.T) { financialBudgetCapacitySuite(t, store) })
		})
	}
}

func financialBudgetSpec() financial.BudgetSpec {
	return financial.BudgetSpec{Name: "monthly usage", Scope: financial.BudgetScope{Kind: "account"}, Currency: "EUR", Meters: []string{"compute"}, Basis: "net_usage", LimitMillicents: 1000, NotifyMillicents: []int64{500}, Mode: "monitored", Action: "notify", ResumeRule: "manual", Enabled: true}
}

func financialBudgetRevisionSuite(t *testing.T, store financialBudgetTestStore) {
	a := financialAccount(t, store)
	other := financialAccount(t, store)
	spec := financialBudgetSpec()
	id := uuid.NewString()
	first, err := store.CreateFinancialBudget(t.Context(), a.ID, id, "account:"+a.ID, spec)
	if err != nil || first.Revision != 1 || first.CreatedAt.IsZero() {
		t.Fatalf("create: %+v, %v", first, err)
	}
	spec.Meters[0], spec.NotifyMillicents[0] = "egress", 600
	read, err := store.GetFinancialBudget(t.Context(), a.ID, id)
	if err != nil || !reflect.DeepEqual(read, first) {
		t.Fatalf("input mutated stored intent: %+v, %v", read, err)
	}
	if _, err := store.GetFinancialBudget(t.Context(), other.ID, id); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign read: %v", err)
	}
	if _, err := store.UpdateFinancialBudget(t.Context(), other.ID, id, 1, "foreign", spec); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign update: %v", err)
	}
	if rows, err := store.ListFinancialBudgetRevisions(t.Context(), other.ID, id, 0, 10); err != nil || len(rows) != 0 {
		t.Fatalf("foreign audit: %+v, %v", rows, err)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, name := range []string{"one", "two"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			update := financialBudgetSpec()
			update.Name = name
			_, err := store.UpdateFinancialBudget(t.Context(), a.ID, id, 1, "editor", update)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	var committed, conflicted int
	for err := range results {
		switch {
		case err == nil:
			committed++
		case errors.Is(err, state.ErrConflict):
			conflicted++
		default:
			t.Fatal(err)
		}
	}
	if committed != 1 || conflicted != 1 {
		t.Fatalf("lost update: committed=%d conflicted=%d", committed, conflicted)
	}
	if _, err := store.DeleteFinancialBudget(t.Context(), a.ID, id, 1, "editor"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale deletion: %v", err)
	}
	deleted, err := store.DeleteFinancialBudget(t.Context(), a.ID, id, 2, "editor")
	if err != nil || deleted.Revision != 3 || deleted.DeletedAt == nil {
		t.Fatalf("delete: %+v, %v", deleted, err)
	}
	if rows, err := store.ListFinancialBudgets(t.Context(), a.ID); err != nil || len(rows) != 0 {
		t.Fatalf("deleted policy listed: %+v, %v", rows, err)
	}
	if _, err := store.CreateFinancialBudget(t.Context(), a.ID, id, "editor", financialBudgetSpec()); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("deleted identity reused: %v", err)
	}
	var audit []state.FinancialBudgetRevision
	for after := int64(0); after < 3; after++ {
		rows, err := store.ListFinancialBudgetRevisions(t.Context(), a.ID, id, after, 1)
		if err != nil || len(rows) != 1 || rows[0].Revision != after+1 {
			t.Fatalf("revision page: %+v, %v", rows, err)
		}
		audit = append(audit, rows[0])
	}
	if audit[0].Spec.Meters[0] != "compute" || audit[0].Spec.NotifyMillicents[0] != 500 || audit[0].Mutation != "created" || audit[1].Mutation != "updated" || audit[2].Mutation != "deleted" {
		t.Fatalf("audit history changed: %+v", audit)
	}
}

func financialBudgetScopeSuite(t *testing.T, store financialBudgetTestStore) {
	a, other := financialAccount(t, store), financialAccount(t, store)
	createApp := func(account, slug string, class state.WorkloadClass, preview string) state.App {
		t.Helper()
		app, err := store.CreateApp(t.Context(), state.App{AccountID: account, Slug: slug, Type: state.AppTypeApp, Runtime: "node22", RAMMB: 256, MaxConcurrency: 1, WorkloadClass: class, PreviewOfSlug: preview})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	production := createApp(a.ID, "production", state.WorkloadClassHTTP, "")
	preview := createApp(a.ID, "arbitrary-name", state.WorkloadClassHTTP, "production")
	worker := createApp(a.ID, "worker", state.WorkloadClassWorker, "")
	foreign := createApp(other.ID, "foreign", state.WorkloadClassHTTP, "")
	for _, tc := range []struct {
		name   string
		app    state.App
		action string
		want   error
	}{
		{"owned", production, "notify", nil},
		{"foreign", foreign, "notify", state.ErrNotFound},
		{"production_is_not_preview", production, "stop_previews", state.ErrInvalidArgument},
		{"explicit_preview", preview, "stop_previews", nil},
		{"http_is_not_background", production, "suspend_background", state.ErrInvalidArgument},
		{"worker", worker, "suspend_background", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := financialBudgetSpec()
			spec.Scope, spec.Action = financial.BudgetScope{Kind: "app", ID: tc.app.ID}, tc.action
			_, err := store.CreateFinancialBudget(t.Context(), a.ID, uuid.NewString(), "editor", spec)
			if !errors.Is(err, tc.want) {
				t.Fatalf("scope/action validation: %v, want %v", err, tc.want)
			}
		})
	}
	spec := financialBudgetSpec()
	spec.Scope = financial.BudgetScope{Kind: "environment", ID: uuid.NewString()}
	if _, err := store.CreateFinancialBudget(t.Context(), a.ID, uuid.NewString(), "editor", spec); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("fabricated scope: %v", err)
	}
}

func financialBudgetCapacitySuite(t *testing.T, store financialBudgetTestStore) {
	a := financialAccount(t, store)
	for range api.FinancialBudgetsPerAccount - 1 {
		if _, err := store.CreateFinancialBudget(t.Context(), a.ID, uuid.NewString(), "editor", financialBudgetSpec()); err != nil {
			t.Fatal(err)
		}
	}
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := store.CreateFinancialBudget(t.Context(), a.ID, uuid.NewString(), "editor", financialBudgetSpec())
			results <- err
		}()
	}
	first, second := <-results, <-results
	if !((first == nil && errors.Is(second, state.ErrFinancialBudgetLimit)) || (second == nil && errors.Is(first, state.ErrFinancialBudgetLimit))) {
		t.Fatalf("concurrent creates crossed the cap: %v, %v", first, second)
	}
	rows, err := store.ListFinancialBudgets(t.Context(), a.ID)
	if err != nil || len(rows) != api.FinancialBudgetsPerAccount {
		t.Fatalf("capacity read: %d, %v", len(rows), err)
	}
}
