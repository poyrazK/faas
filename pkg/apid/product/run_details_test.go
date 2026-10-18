package product

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductControlRunDetailsService(t *testing.T) {
	f := newEmailFixture(t)
	call := func(query, id string, want int) {
		t.Helper()
		r := httptest.NewRequest("GET", "https://portal.example/v1/product-control/customer/"+f.app.Slug+"/operations/"+id+"/details"+query, nil)
		r.SetPathValue("slug", f.app.Slug)
		r.SetPathValue("operation", id)
		r.Header.Set("X-Product-Organization", f.key.OrganizationID)
		r.AddCookie(&http.Cookie{Name: (customerRealm{authApp: f.authApp}).cookieName("access"), Value: "customer.access.jwt"})
		w := httptest.NewRecorder()
		f.service.Gate(f.service.Customer(api.ProductPermissionViewWork, f.service.CustomerRunDetails))(w, r)
		if w.Code != want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d want %d: %s", query, w.Code, want, w.Body.String())
		}
	}
	call("", uuid.NewString(), 404)
	call("", "bad", 400)
	for _, q := range []string{"?tenant_id=" + f.key.TenantID, "?role=owner", "?after=0", "?period=2026-09"} {
		call(q, uuid.NewString(), 400)
	}
	f.role.Store("unknown")
	call("", uuid.NewString(), 403)
	f.service.Enabled = false
	call("", uuid.NewString(), 404)
}

func TestProductControlRunDetailsUnsafeProjection(t *testing.T) {
	scope := state.ProductRunDetailsScope{ProductPlanScope: state.ProductPlanScope{AccountID: uuid.NewString(), AppID: uuid.NewString(), TenantID: uuid.NewString()}}
	id := uuid.NewString()
	r := httptest.NewRequest("GET", "/", nil)
	r.SetPathValue("operation", id)
	r = r.WithContext(authmw.WithProductCustomer(r.Context(), state.Account{ID: scope.AccountID}, authmw.ProductCustomerIdentity{TenantID: scope.TenantID, Access: api.ProductCustomerAccess{Permissions: api.ProductPermissions{ViewWork: true}}}))
	d := api.ProductRunDetails{AccountID: scope.AccountID, AppID: scope.AppID, TenantID: scope.TenantID, OperationID: id, Feature: "export", Plan: "starter", Period: "2026-09", AdmittedAt: time.Now().Add(-time.Hour), ObservedAt: time.Now(), StartsConsumed: 1, Outcome: api.ProductRunOutcome{State: "unavailable"}}
	if err := runDetailsScoped(r, scope, d); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []string{"tenant", "app", "account", "operation", "charge"} {
		t.Run(tc, func(t *testing.T) {
			bad := d
			switch tc {
			case "tenant":
				bad.TenantID = uuid.NewString()
			case "app":
				bad.AppID = uuid.NewString()
			case "account":
				bad.AccountID = uuid.NewString()
			case "operation":
				bad.OperationID = uuid.NewString()
			case "charge":
				bad.Charge = &api.ProductRunCharge{Currency: "EUR", UnitPriceMillicents: 25000}
			}
			if err := runDetailsScoped(r, scope, bad); err == nil {
				t.Fatal("unsafe projection accepted")
			}
		})
	}
	w := httptest.NewRecorder()
	d.Feature = strings.Repeat("x", api.ProductControlRunDetailsMaxResponseBytes)
	runDetailsRespond(w, d, nil)
	if w.Code == 200 || strings.Contains(w.Body.String(), d.Feature) {
		t.Fatal("response bound failed")
	}
	w = httptest.NewRecorder()
	d.Feature = "export"
	runDetailsRespond(w, d, nil)
	var got api.ProductRunDetails
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.OperationID != id {
		t.Fatal("valid projection rejected", w.Body.String())
	}
}
