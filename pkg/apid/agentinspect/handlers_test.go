// adr: 20261103211147
package agentinspect

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

func previewFixture(t *testing.T) (Deps, state.Account, state.App) {
	t.Helper()
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "preview@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "alpha", Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	return Deps{Store: store, Resolve: func(http.ResponseWriter, *http.Request, state.Account) (state.App, state.ProjectEnvironment, bool) {
		return app, state.ProjectEnvironment{}, true
	}}, account, app
}

func TestPreviewExplainsScopeWithoutWriting(t *testing.T) {
	d, acct, app := previewFixture(t)
	for _, write := range []bool{false, true} {
		scopes := []string{api.ScopeAppsRead}
		if write {
			scopes = append(scopes, api.ScopeEnvWrite)
		}
		r := httptest.NewRequest("GET", "/v1/apps/alpha/operation-preview?operation=env_change", nil)
		r = r.WithContext(authmw.WithPrincipal(r.Context(), acct, &state.APIKey{Scopes: scopes}, nil))
		w := httptest.NewRecorder()
		Preview(d)(w, r, acct)
		var out api.AppOperationPreview
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("preview: %d %s", w.Code, w.Body)
		}
		want := "blocked"
		if write {
			want = "allowed"
		}
		if out.Authorization != want || out.Admission == "allowed" || out.AppID != app.ID || out.Checks["quota"].Status != "unknown" {
			t.Fatalf("misleading preview: %+v", out)
		}
		rows, err := d.Store.(state.ConfigRevisionStore).ListConfigRevisions(t.Context(), app.ID, "default", 20)
		if err != nil || len(rows) != 0 {
			t.Fatal("authorization preview wrote configuration history")
		}
	}
}

func TestPreviewUsesCurrentOrganizationRole(t *testing.T) {
	d, owner, app := previewFixture(t)
	store := d.Store.(*state.MemStore)
	actor, err := store.CreateAccount(t.Context(), "member@example.test", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(t.Context(), state.Org{Slug: "team", Name: "Team", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(t.Context(), org.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(t.Context(), org.ID, actor.ID, state.OrgRoleViewer, nil); err != nil {
		t.Fatal(err)
	}
	app.OrgID = org.ID
	// The read resolver is pinned; native authorization must still consult the
	// current membership instead of treating app inspection as write access.
	for _, role := range []state.OrgRole{state.OrgRoleViewer, state.OrgRoleDeveloper} {
		if role == state.OrgRoleDeveloper {
			if err := store.UpdateOrgMemberRole(t.Context(), org.ID, actor.ID, role); err != nil {
				t.Fatal(err)
			}
		}
		r := httptest.NewRequest("GET", "/v1/apps/alpha/operation-preview?operation=env_change", nil)
		r = r.WithContext(authmw.WithPrincipal(r.Context(), actor, &state.APIKey{OrgID: org.ID, Scopes: []string{api.ScopeAppsRead, api.ScopeEnvWrite}}, nil))
		out := preview(d, r, owner, app, state.ProjectEnvironment{}, "env_change")
		want := "blocked"
		if role == state.OrgRoleDeveloper {
			want = "allowed"
		}
		if out.Authorization != want {
			t.Fatalf("role %s: %+v", role, out)
		}
	}
}

func TestAppCostProjectionWithholdsAccountFinancialData(t *testing.T) {
	const exact int64 = 9007199254740993
	report := api.FinancialCostsResponse{AccountID: "private-account", Currency: "USD", KnownUsageMillicents: 7777777, MissingBillComponents: []string{"tax"}, Meters: []api.FinancialMeterCosts{{Meter: "compute",
		Coverage: api.FinancialMeterCoverage{UnpricedQuantity: 8888888, NonBillableQuantity: 9999999},
		Accrued: financial.ContractCosts{Contracts: []financial.MeterCost{{Price: financial.Price{Unit: "MB-second"}, Allocations: []financial.Allocation{
			{Attribution: financial.Attribution{AppID: "app-own", Name: "own"}, Quantity: exact, NetMillicents: exact},
			{Attribution: financial.Attribution{AppID: "app-private", Name: "private-neighbor"}, Quantity: 1, NetMillicents: 1},
		}}}},
	}}}
	scoped, err := billing.ScopeFinancialCostsToApp(report, "app-own", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	out := appCosts(scoped)
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"private-account", "private-neighbor", "7777777", "8888888", "9999999", "unpriced_quantity", "non_billable_quantity", "invoices", "forecast"} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("app costs exposed %s", private)
		}
	}
	if out.KnownUsageMillicents != exact || out.Meters[0].Coverage.QuantityCoverageKnown || out.BillEstimateAvailable || !strings.Contains(string(raw), "9007199254740993") {
		t.Fatal("app costs lost exact amounts or fabricated coverage")
	}
}
