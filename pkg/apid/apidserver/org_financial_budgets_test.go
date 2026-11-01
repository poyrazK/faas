// adr: 20261031211500 — an organization that owns its resources holds the
// budgets for what it pays for.
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// The organization's budgets cover its apps, the member's budgets cannot name
// them, any member reads them, only the owner or billing role writes them,
// and strict budgets stay with accounts.
func TestOrgFinancialBudgetRoutes(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	org := apidserver.SeedSharedOrgWithOwner(t, e, "budget-company", "Budget Company", api.PlanPro)
	app, err := store.CreateApp(t.Context(), state.App{AccountID: e.Acct().ID, OrgID: org.ID, Slug: "company-preview", PreviewOfSlug: "production", RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	spec := financialDraftSpec()
	spec.Scope = financial.BudgetScope{Kind: "app", ID: app.ID}
	spec.Enabled, spec.Action, spec.DrainSeconds = true, "notify", 0
	orgBudgets := "/v1/orgs/" + org.Slug + "/billing/budgets"
	key := func(k string) map[string]string { return map[string]string{"Idempotency-Key": k} }

	if rec := e.Do(t, "POST", "/v1/billing/budgets", api.CreateFinancialBudgetRequest{Spec: spec}, key("member-on-company-app")); rec.Code != http.StatusNotFound {
		t.Fatalf("member budget on the organization's app: %d %s, want 404", rec.Code, rec.Body)
	}
	p := decodeFinancialBudget(t, e.Do(t, "POST", orgBudgets, api.CreateFinancialBudgetRequest{Spec: spec}, key("company-notify")), http.StatusCreated)
	if p.AccountID != org.ID || p.Status != "active" {
		t.Fatalf("organization budget = %+v, want active under %s", p, org.ID)
	}

	list := func(path string, headers map[string]string) api.FinancialBudgetListResponse {
		t.Helper()
		rec := e.Do(t, "GET", path, nil, headers)
		var out api.FinancialBudgetListResponse
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatalf("list %s: %d %s", path, rec.Code, rec.Body)
		}
		return out
	}
	if got := list(orgBudgets, nil); len(got.Budgets) != 1 || got.Budgets[0].ID != p.ID {
		t.Fatalf("organization budgets = %+v", got.Budgets)
	}
	if got := list("/v1/billing/budgets", nil); len(got.Budgets) != 0 {
		t.Fatalf("member budgets include the organization's: %+v", got.Budgets)
	}
	if rec := e.Do(t, "GET", "/v1/billing/budgets/"+p.ID, nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("member read of the organization's budget: %d, want 404", rec.Code)
	}
	if rec := e.Do(t, "GET", "/v1/orgs/"+org.Slug+"/billing/costs", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("organization costs: %d %s", rec.Code, rec.Body)
	}

	strict := spec
	strict.Mode, strict.Action, strict.Basis, strict.NotifyMillicents = "strict", "suspend_workloads", "gross_usage", nil
	if rec := e.Do(t, "POST", orgBudgets, api.CreateFinancialBudgetRequest{Spec: strict}, key("company-strict")); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("organization strict budget: %d %s, want 422", rec.Code, rec.Body)
	}

	_, viewer := sharedAppMember(t, store, org, state.OrgRoleViewer, api.ScopesAdminOnly)
	if got := list(orgBudgets, viewer); len(got.Budgets) != 1 {
		t.Fatalf("viewer read = %+v", got.Budgets)
	}
	headers := key("viewer-delete")
	headers["Authorization"] = viewer["Authorization"]
	if rec := e.Do(t, "DELETE", orgBudgets+"/"+p.ID, api.DeleteFinancialBudgetRequest{ExpectedRevision: p.Revision}, headers); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer deleted the organization's budget: %d %s", rec.Code, rec.Body)
	}
	deleted := decodeFinancialBudget(t, e.Do(t, "DELETE", orgBudgets+"/"+p.ID, api.DeleteFinancialBudgetRequest{ExpectedRevision: p.Revision}, key("owner-delete")), http.StatusOK)
	if deleted.DeletedAt == nil {
		t.Fatalf("owner delete = %+v, want a tombstone", deleted)
	}
}
