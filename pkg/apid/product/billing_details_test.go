package product

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

type billingDetailsTestHost struct {
	customerTestHost
	audits []map[string]any
}

func (h *billingDetailsTestHost) EmitAudit(_ context.Context, kind string, _ *string, data map[string]any) {
	h.audits = append(h.audits, map[string]any{"kind": kind, "data": data})
}

// adr: 20261020120000 — only a role with manage_billing reads or changes the
// workspace's billing details, they reach the provider customer named by the
// workspace's platform tenant, and the audit names changed parts, never
// their values.
func TestProductCustomerBillingDetails(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "billing-details-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "billed"})
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "ctm_01workspace", "Workspace", 100)
	if err != nil {
		t.Fatal(err)
	}
	host := &billingDetailsTestHost{customerTestHost: customerTestHost{store: store}}
	var customers []string
	var changed []string
	var problem *api.Problem
	stored := api.ProductBillingDetails{Provider: "paddle"}
	s := &Service{Host: host,
		BillingDetails: func(_ context.Context, acct state.Account, customer string) (api.ProductBillingDetails, *api.Problem) {
			customers = append(customers, acct.ID+"/"+customer)
			return stored, problem
		},
		PutBillingDetails: func(_ context.Context, acct state.Account, customer string, req api.PutProductBillingDetailsRequest) (api.ProductBillingDetails, []string, *api.Problem) {
			customers = append(customers, acct.ID+"/"+customer)
			stored.Address = &req.Address
			return stored, changed, problem
		}}
	call := func(handler func(http.ResponseWriter, *http.Request, state.Account), method, body string, permissions api.ProductPermissions, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/", strings.NewReader(body))
		r.SetPathValue("slug", app.Slug)
		r = r.WithContext(authmw.WithProductCustomer(r.Context(), account, authmw.ProductCustomerIdentity{TenantID: tenant.ID, ProductSlug: app.Slug,
			Access: api.ProductCustomerAccess{Role: "owner", Permissions: permissions}}))
		w := httptest.NewRecorder()
		handler(w, r, account)
		if w.Code != status {
			t.Fatalf("%s %s: %d want %d: %s", method, body, w.Code, status, w.Body)
		}
		return w
	}
	readOnly := api.ProductPermissions{ViewWork: true, ViewBilling: true, ManagePlan: true}
	manager := api.ProductPermissions{ViewBilling: true, ManageBilling: true}
	body := `{"address":{"country_code":"GB","postal_code":"SW1A 1AA","city":"London","first_line":"10 Downing St"}}`
	call(s.CustomerBillingDetails, "GET", "", readOnly, http.StatusForbidden)
	call(s.PutCustomerBillingDetails, "PUT", body, readOnly, http.StatusForbidden)
	if len(customers) != 0 {
		t.Fatalf("a role without manage_billing reached the provider: %v", customers)
	}

	call(s.CustomerBillingDetails, "GET", "", manager, http.StatusOK)
	changed = []string{"address"}
	w := call(s.PutCustomerBillingDetails, "PUT", body, manager, http.StatusOK)
	var saved api.ProductBillingDetails
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil || saved.Address == nil || saved.Address.PostalCode != "SW1A 1AA" {
		t.Fatalf("saved = %+v %v", saved, err)
	}
	want := account.ID + "/ctm_01workspace"
	if len(customers) != 2 || customers[0] != want || customers[1] != want {
		t.Fatalf("provider customers = %v", customers)
	}
	if len(host.audits) != 1 || host.audits[0]["kind"] != "product.customer.billing_details_updated" {
		t.Fatalf("audits = %v", host.audits)
	}
	data := host.audits[0]["data"].(map[string]any)
	if data["app_id"] != app.ID || data["tenant_id"] != tenant.ID || fmt.Sprint(data["changed"]) != "[address]" ||
		strings.Contains(fmt.Sprint(data), "Downing") || strings.Contains(fmt.Sprint(data), "SW1A") {
		t.Fatalf("audit data = %v", data)
	}

	changed = nil
	call(s.PutCustomerBillingDetails, "PUT", body, manager, http.StatusOK)
	if len(host.audits) != 1 {
		t.Fatalf("an unchanged save was audited: %v", host.audits)
	}
	problem = api.NewProblem(http.StatusConflict, api.CodeProductBillingDetailsUnavailable, "Billing details unavailable", "not billed through Paddle")
	if w := call(s.CustomerBillingDetails, "GET", "", manager, http.StatusConflict); !strings.Contains(w.Body.String(), api.CodeProductBillingDetailsUnavailable) {
		t.Fatalf("unavailable = %s", w.Body)
	}
}
