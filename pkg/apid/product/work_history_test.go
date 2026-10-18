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

func TestProductControlWorkHistoryService(t *testing.T) {
	f := newEmailFixture(t)
	call := func(query string, want int) api.ProductWorkHistoryPage {
		t.Helper()
		r := httptest.NewRequest("GET", "https://portal.example/v1/product-control/customer/"+f.app.Slug+"/work-history"+query, nil)
		r.SetPathValue("slug", f.app.Slug)
		r.Header.Set("X-Product-Organization", f.key.OrganizationID)
		r.AddCookie(&http.Cookie{Name: (customerRealm{authApp: f.authApp}).cookieName("access"), Value: "customer.access.jwt"})
		w := httptest.NewRecorder()
		f.service.Gate(f.service.Customer(api.ProductPermissionViewWork, f.service.CustomerWorkHistory))(w, r)
		if w.Code != want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d want %d: %s", query, w.Code, want, w.Body.String())
		}
		var page api.ProductWorkHistoryPage
		if want == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
		}
		return page
	}
	if p := call("", 200); p.Entries == nil || p.Filter.State != "all" || p.NextCursor != "" {
		t.Fatal(p)
	}
	for _, q := range []string{"?tenant_id=" + f.key.TenantID, "?role=owner", "?limit=100", "?state=all&state=failed", "?cursor=a&cursor=b", "?cursor=bad", "?state=expired", "?feature=../other", "?operation_id=bad", "?from=2026-02-30", "?from=2026-10-02&through=2026-10-01"} {
		call(q, 400)
	}
	if p := call("?feature=retired&state=succeeded&from=2024-02-29&through=2024-02-29", 200); len(p.Entries) != 0 {
		t.Fatal(p)
	}
	f.role.Store("unknown")
	call("", 403)
	f.service.Enabled = false
	call("", 404)
}

func TestProductControlWorkHistoryUnsafeProjection(t *testing.T) {
	s := state.ProductWorkHistoryScope{ProductPlanScope: state.ProductPlanScope{AccountID: uuid.NewString(), AppID: uuid.NewString(), TenantID: uuid.NewString()}}
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(authmw.WithProductCustomer(r.Context(), state.Account{ID: s.AccountID}, authmw.ProductCustomerIdentity{TenantID: s.TenantID, Access: api.ProductCustomerAccess{Permissions: api.ProductPermissions{ViewWork: true}}}))
	f := api.ProductWorkHistoryFilter{State: "all"}
	e := api.ProductWorkHistoryEntry{OperationID: uuid.NewString(), Feature: "export", Plan: "starter", Period: "2026-09", AdmittedAt: time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond), Outcome: api.ProductRunOutcome{State: "unavailable"}}
	p := api.ProductWorkHistoryPage{AccountID: s.AccountID, AppID: s.AppID, TenantID: s.TenantID, Filter: f, ObservedAt: time.Now().UTC(), HistoryScope: "retained_product_control_receipts", Entries: []api.ProductWorkHistoryEntry{e}}
	if err := workHistoryScoped(r, s, f, "", p); err != nil {
		t.Fatal(err)
	}
	for _, what := range []string{"tenant", "app", "account", "filter", "outcome", "duplicate", "cursor", "null"} {
		t.Run(what, func(t *testing.T) {
			b := p
			b.Entries = append([]api.ProductWorkHistoryEntry{}, p.Entries...)
			switch what {
			case "tenant":
				b.TenantID = uuid.NewString()
			case "app":
				b.AppID = uuid.NewString()
			case "account":
				b.AccountID = uuid.NewString()
			case "filter":
				b.Filter.Feature = "other"
			case "outcome":
				b.Entries[0].Outcome.State = "failed"
			case "duplicate":
				b.Entries = append(b.Entries, e)
			case "cursor":
				b.NextCursor = "owner-cursor"
			case "null":
				b.Entries = nil
			}
			if workHistoryScoped(r, s, f, "", b) == nil {
				t.Fatal("unsafe projection accepted")
			}
		})
	}
	w := httptest.NewRecorder()
	workHistoryRespond(w, p, nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	p.Entries[0].Feature = strings.Repeat("x", api.ProductControlWorkHistoryMaxResponseBytes)
	w = httptest.NewRecorder()
	workHistoryRespond(w, p, nil)
	if w.Code == 200 || strings.Contains(w.Body.String(), p.Entries[0].Feature) {
		t.Fatal("response bound failed")
	}
}
