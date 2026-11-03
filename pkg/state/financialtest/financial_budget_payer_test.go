package financialtest

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261031211500 — a budget, its evidence and its preview stops belong
// to the subject that pays: an organization that owns its resources pays for
// its apps, and the member who created one does not.
func TestFinancialBudgetsFollowThePayer(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store budgetStopStore = state.NewMemStore()
			adopt := func(string) {} // MemStore organizations own their resources.
			if backend == "postgres" {
				pg, pool, ctx := financialPostgres(t)
				store = pg
				adopt = func(org string) {
					if _, err := pool.Exec(ctx, `update orgs set resource_ownership_enabled = true where id = $1`, org); err != nil {
						t.Fatal(err)
					}
				}
			}
			financialBudgetPayerSuite(t, store, adopt)
		})
	}
}

func financialBudgetPayerSuite(t *testing.T, store budgetStopStore, adopt func(string)) {
	t.Helper()
	ctx, suffix := t.Context(), uuid.NewString()[:8]
	member := financialAccount(t, store)
	org, err := store.CreateOrg(ctx, state.Org{Slug: "payer-" + suffix, Name: "Payer Co", Plan: api.PlanFree})
	if err != nil {
		t.Fatal(err)
	}
	adopt(org.ID)
	if err := store.UpdateOrgPlan(ctx, org.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, member.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	personal, err := store.CreateApp(ctx, state.App{AccountID: member.ID, Slug: "personal-" + suffix, PreviewOfSlug: "production", RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	company, err := store.CreateApp(ctx, state.App{AccountID: member.ID, OrgID: org.ID, Slug: "company-" + suffix, PreviewOfSlug: "production", RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		app  state.App
		want string
	}{{personal, member.ID}, {company, org.ID}} {
		if got, err := state.FinancialBudgetPayer(ctx, store, tc.app); err != nil || got != tc.want {
			t.Fatalf("payer of %s = %q, %v; want %q", tc.app.Slug, got, err, tc.want)
		}
	}

	appSpec := func(app state.App) financial.BudgetSpec {
		spec := financialBudgetSpec()
		spec.Scope = financial.BudgetScope{Kind: "app", ID: app.ID}
		spec.Action, spec.DrainSeconds = "stop_previews", 0
		return spec
	}
	// The member does not pay for the company's app, and the company does not
	// pay for the member's own app.
	if _, err := store.CreateFinancialBudget(ctx, member.ID, uuid.NewString(), "editor", appSpec(company)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("member budget on an organization's app: %v, want ErrNotFound", err)
	}
	if _, err := store.CreateFinancialBudget(ctx, org.ID, uuid.NewString(), "editor", appSpec(personal)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("organization budget on a member's app: %v, want ErrNotFound", err)
	}
	memberBudget, err := store.CreateFinancialBudget(ctx, member.ID, uuid.NewString(), "editor", appSpec(personal))
	if err != nil {
		t.Fatalf("member budget on its own app: %v", err)
	}
	if _, err := store.CreateFinancialBudget(ctx, org.ID, uuid.NewString(), "editor", appSpec(company)); err != nil {
		t.Fatalf("organization budget on its app: %v", err)
	}
	accountWide := financialBudgetSpec()
	accountWide.Action, accountWide.DrainSeconds = "stop_previews", 0
	orgAccount, err := store.CreateFinancialBudget(ctx, org.ID, uuid.NewString(), "editor", accountWide)
	if err != nil {
		t.Fatalf("organization account budget: %v", err)
	}

	lister, ok := store.(state.FinancialBudgetSubjectLister)
	if !ok {
		t.Fatal("store does not list monitored budget subjects")
	}
	subjects, err := lister.ListFinancialBudgetMonitoredSubjects(ctx)
	if err != nil || !slices.Contains(subjects, member.ID) || !slices.Contains(subjects, org.ID) {
		t.Fatalf("monitored subjects = %v, %v; want the member and the organization", subjects, err)
	}

	// The organization's account-wide stop holds its preview, never the
	// member's own preview, which only the member's stops can hold.
	month := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	now, amount := month.Add(time.Hour), int64(1000)
	e := financial.BudgetEvaluation{PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), AsOf: now, EvaluatedAt: now, KnownMillicents: &amount, Fresh: true, Reasons: []string{}}
	if err := store.RecordFinancialBudgetEvaluation(ctx, org.ID, orgAccount.ID, orgAccount.Revision, e); err != nil {
		t.Fatal(err)
	}
	if err := state.CheckFinancialBudgetAdmission(ctx, store, company, "", now); !errors.Is(err, state.ErrFinancialBudgetHeld) {
		t.Fatalf("organization preview under the organization's stop: %v, want held", err)
	}
	if err := state.CheckFinancialBudgetAdmission(ctx, store, personal, "", now); err != nil {
		t.Fatalf("member's own preview under the organization's stop: %v, want admitted", err)
	}
	stops, err := store.ActiveFinancialBudgetStops(ctx, org.ID, now)
	if err != nil || len(stops) != 1 {
		t.Fatalf("organization stops = %+v, %v", stops, err)
	}
	target := financial.BudgetStopTarget{AppID: company.ID, State: "enforced", ObservedAt: now}
	if err := store.RecordFinancialBudgetStopTarget(ctx, org.ID, stops[0].ID, target); err != nil {
		t.Fatalf("record the organization's stop target: %v", err)
	}
	personalTarget := financial.BudgetStopTarget{AppID: personal.ID, State: "enforced", ObservedAt: now}
	if err := store.RecordFinancialBudgetStopTarget(ctx, org.ID, stops[0].ID, personalTarget); !errors.Is(err, state.ErrConflict) && err != nil {
		t.Fatalf("record a member's app under the organization's stop: %v", err)
	}
	if targets, err := store.FinancialBudgetStopTargets(ctx, org.ID, stops[0].ID); err != nil || len(targets) != 1 || targets[0].AppID != company.ID {
		t.Fatalf("organization stop targets = %+v, %v; want only the organization's preview", targets, err)
	}

	// A member's stop never reaches the organization's app.
	if err := store.RecordFinancialBudgetEvaluation(ctx, member.ID, memberBudget.ID, memberBudget.Revision, e); err != nil {
		t.Fatal(err)
	}
	if err := state.CheckFinancialBudgetAdmission(ctx, store, personal, "", now); !errors.Is(err, state.ErrFinancialBudgetHeld) {
		t.Fatalf("member preview under the member's stop: %v, want held", err)
	}
}
