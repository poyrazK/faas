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

func TestProductControlWorkspaceActivityService(t *testing.T) {
	f := newEmailFixture(t)
	f.reply(t)
	call := func(query string, want int) api.ProductWorkspaceActivityPage {
		t.Helper()
		r := httptest.NewRequest("GET", "https://portal.example/v1/product-control/customer/"+f.app.Slug+"/activity"+query, nil)
		r.SetPathValue("slug", f.app.Slug)
		r.Header.Set("X-Product-Organization", f.key.OrganizationID)
		r.AddCookie(&http.Cookie{Name: (customerRealm{authApp: f.authApp}).cookieName("access"), Value: "customer.access.jwt"})
		w := httptest.NewRecorder()
		f.service.Gate(f.service.Customer("", f.service.CustomerWorkspaceActivity))(w, r)
		if w.Code != want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d %s", query, w.Code, w.Body.String())
		}
		var p api.ProductWorkspaceActivityPage
		if want == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	p := call("", 200)
	if len(p.Events) != 1 || p.Events[0].Kind != "help_resolved" || p.Filter != "all" {
		t.Fatal(p)
	}
	for _, q := range []string{"?tenant=" + uuid.NewString(), "?role=owner", "?filter=all&filter=work", "?filter=wrong", "?cursor=bad", "?filter=help&cursor=a&cursor=b"} {
		call(q, 400)
	}
	if p = call("?filter=billing", 200); len(p.Events) != 0 || p.Events == nil {
		t.Fatal("empty page should be an array")
	}
	f.role.Store("unknown")
	call("", 403)
	f.service.Enabled = false
	call("", 404)
}

func TestProductControlWorkspaceActivityRejectsUnsafeProjection(t *testing.T) {
	s := state.ProductWorkspaceActivityScope{ProductPlanScope: state.ProductPlanScope{AccountID: uuid.NewString(), AppID: uuid.NewString(), TenantID: uuid.NewString(), AuthAppID: uuid.NewString(), OrganizationID: uuid.NewString(), Role: "viewer"}}
	permissions := api.ProductPermissions{ViewWork: true}
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(authmw.WithProductCustomer(r.Context(), state.Account{ID: s.AccountID}, authmw.ProductCustomerIdentity{TenantID: s.TenantID, Access: api.ProductCustomerAccess{Role: s.Role, Permissions: permissions}}))
	id := uuid.NewString()
	e := api.ProductWorkspaceActivityEvent{ID: "work_succeeded/" + id, ReferenceID: id, Kind: "work_succeeded", Feature: "export", OccurredAt: time.Now().UTC()}
	p := api.ProductWorkspaceActivityPage{AccountID: s.AccountID, AppID: s.AppID, TenantID: s.TenantID, Filter: "all", HistoryScope: "retained_product_control_records", ObservedAt: time.Now().UTC(), Events: []api.ProductWorkspaceActivityEvent{e}}
	if err := workspaceActivityScoped(r, s, "all", "", p); err != nil {
		t.Fatal(err)
	}
	for _, what := range []string{"scope", "filter", "permission", "owner field", "duplicate", "continuation", "null events"} {
		t.Run(what, func(t *testing.T) {
			bad := p
			bad.Events = append([]api.ProductWorkspaceActivityEvent{}, p.Events...)
			switch what {
			case "scope":
				bad.TenantID = uuid.NewString()
			case "filter":
				bad.Filter = "billing"
			case "permission":
				bad.Events[0] = api.ProductWorkspaceActivityEvent{ID: "plan_changed/" + id, Kind: "plan_changed", ReferenceID: id, OccurredAt: e.OccurredAt, Plan: &api.ProductWorkspaceActivityPlan{Before: "starter", After: "pro"}}
			case "owner field":
				bad.Events[0].Billing = &api.ProductWorkspaceActivityBill{Period: "2026-09", Currency: "EUR", AmountMillicents: 25000}
			case "duplicate":
				bad.Events = append(bad.Events, e)
			case "continuation":
				bad.NextCursor = "private-owner-cursor"
			case "null events":
				bad.Events = nil
			}
			if err := workspaceActivityScoped(r, s, "all", "", bad); err == nil {
				t.Fatal("unsafe projection accepted")
			}
		})
	}
	p.Events[0].Feature = strings.Repeat("x", api.ProductControlWorkspaceActivityMaxResponseBytes)
	w := httptest.NewRecorder()
	workspaceActivityRespond(w, p, nil)
	if w.Code == 200 || strings.Contains(w.Body.String(), p.Events[0].Feature) {
		t.Fatal("oversized response escaped limit")
	}
}
