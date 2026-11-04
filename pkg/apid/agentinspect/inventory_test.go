// adr: 20261103222927
package agentinspect

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestInventoryFiltersBeforeCountingAndPagination(t *testing.T) {
	d, acct, app := previewFixture(t)
	neighbor, err := d.Store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "neighbor", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	for _, domain := range []string{"c.example.test", "a.example.test", "b.example.test"} {
		if _, err := d.Store.CreateCustomDomain(t.Context(), domain, app.ID, "private-challenge-token"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.Store.CreateCustomDomain(t.Context(), "aaa-neighbor.example.test", neighbor.ID, "private-neighbor-token"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		offset int
		count  int
		more   bool
	}{{0, 2, true}, {2, 1, false}, {9, 0, false}} {
		r := httptest.NewRequest("GET", "/v1/apps/alpha/resource-inventory?kind=domains&limit=2&offset="+strconv.Itoa(tc.offset), nil)
		r = r.WithContext(authmw.WithPrincipal(r.Context(), acct, &state.APIKey{Scopes: []string{api.ScopeAppsRead}}, nil))
		w := httptest.NewRecorder()
		Inventory(d)(w, r, acct)
		var out api.AppResourceInventoryResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.AppID != app.ID || out.Total != 3 || len(out.Items) != tc.count || out.HasMore != tc.more {
			t.Fatalf("page offset %d: %d %s", tc.offset, w.Code, w.Body)
		}
		if tc.more && (out.NextOffset == nil || *out.NextOffset != 2) || !tc.more && out.NextOffset != nil {
			t.Fatal("invalid continuation")
		}
		if strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "neighbor") {
			t.Fatal("metadata or counts crossed the app boundary")
		}
		if w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("metadata may be cached across principals")
		}
	}
}

func TestInventoryRejectsSelectorsAndWithholdsChangedIdentity(t *testing.T) {
	d, acct, app := previewFixture(t)
	for _, query := range []string{"", "?kind=all", "?kind=domains&kind=crons", "?kind=domains&limit=0", "?kind=domains&limit=101", "?kind=domains&offset=-1", "?kind=domains&offset=1000001", "?kind=domains&offset=", "?kind=domains&account_id=other", "?kind=domains&environment=staging"} {
		r := httptest.NewRequest("GET", "/v1/apps/alpha/resource-inventory"+query, nil)
		w := httptest.NewRecorder()
		Inventory(d)(w, r, acct)
		if w.Code != 400 {
			t.Fatalf("accepted invalid query %q: %d", query, w.Code)
		}
	}
	d.Inventory = func(r *http.Request, q state.AppResourceInventoryRequest) (state.AppResourceInventoryPage, error) {
		return state.AppResourceInventoryPage{Items: []api.AppResourceInventoryItem{{ID: "foreign", AppID: "other-app", Scope: q.Scope}}, Total: 1}, nil
	}
	r := httptest.NewRequest("GET", "/v1/apps/alpha/resource-inventory?kind=domains", nil)
	r = r.WithContext(authmw.WithPrincipal(r.Context(), acct, &state.APIKey{Scopes: []string{api.ScopeAppsRead}}, nil))
	w := httptest.NewRecorder()
	Inventory(d)(w, r, acct)
	if w.Code != 503 || strings.Contains(w.Body.String(), "other-app") {
		t.Fatal("foreign adapter metadata was exposed")
	}
	d.Inventory = func(r *http.Request, q state.AppResourceInventoryRequest) (state.AppResourceInventoryPage, error) {
		if err := d.Store.DeleteApp(r.Context(), app.ID); err != nil {
			t.Fatal(err)
		}
		return state.AppResourceInventoryPage{Items: []api.AppResourceInventoryItem{}, Total: 0}, nil
	}
	w = httptest.NewRecorder()
	Inventory(d)(w, r, acct)
	if w.Code != 404 {
		t.Fatalf("deleted app metadata was returned: %d %s", w.Code, w.Body)
	}
}

func TestInventoryRechecksOrganizationMembershipAndRelease(t *testing.T) {
	d, owner, _ := previewFixture(t)
	actor, err := d.Store.CreateAccount(t.Context(), "inventory-member@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := d.Store.CreateOrg(t.Context(), state.Org{Slug: "inventory-team", Name: "Team", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range []struct {
		id   string
		role state.OrgRole
	}{{owner.ID, state.OrgRoleOwner}, {actor.ID, state.OrgRoleViewer}} {
		if err := d.Store.AddOrgMember(t.Context(), org.ID, member.id, member.role, nil); err != nil {
			t.Fatal(err)
		}
	}
	app, err := d.Store.CreateApp(t.Context(), state.App{AccountID: owner.ID, OrgID: org.ID, Slug: "shared-inventory", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	d.Resolve = func(http.ResponseWriter, *http.Request, state.Account) (state.App, state.ProjectEnvironment, bool) {
		return app, state.ProjectEnvironment{}, true
	}
	calls := 0
	d.Inventory = func(r *http.Request, q state.AppResourceInventoryRequest) (state.AppResourceInventoryPage, error) {
		calls++
		return state.AppResourceInventoryPage{Items: []api.AppResourceInventoryItem{}}, nil
	}
	d.Released = func(*http.Request, state.Account, state.App, string) bool { return false }
	r := httptest.NewRequest("GET", "/v1/apps/shared-inventory/resource-inventory?kind=buckets", nil)
	r = r.WithContext(authmw.WithPrincipal(r.Context(), actor, &state.APIKey{OrgID: org.ID, Scopes: []string{api.ScopeAppsRead}}, nil))
	w := httptest.NewRecorder()
	Inventory(d)(w, r, owner)
	if w.Code != 404 || calls != 0 {
		t.Fatal("metadata bypassed the native resource release flag")
	}
	d.Released = nil
	d.Inventory = func(r *http.Request, q state.AppResourceInventoryRequest) (state.AppResourceInventoryPage, error) {
		if err := d.Store.RemoveOrgMember(r.Context(), org.ID, actor.ID); err != nil {
			t.Fatal(err)
		}
		return state.AppResourceInventoryPage{Items: []api.AppResourceInventoryItem{}}, nil
	}
	w = httptest.NewRecorder()
	Inventory(d)(w, r, owner)
	if w.Code == 200 {
		t.Fatal("metadata survived organization access revocation during the read")
	}
}
