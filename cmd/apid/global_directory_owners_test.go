package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func directoryOrgKey(t *testing.T, e testEnv, org state.Org, actor state.Account, scopes []string) testEnv {
	t.Helper()
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.CreateOrgAPIKey(t.Context(), org.ID, actor.ID, hash, "directory-org", scopes, nil); err != nil {
		t.Fatal(err)
	}
	e.key = token
	e.acct = actor
	return e
}
func TestGlobalDirectorySharedOwnerMembershipAndQuota(t *testing.T) {
	e := globalDirectoryHarness(t)
	org := seedSharedOrgWithOwner(t, e, "global-team", "Global Team", api.PlanFree)
	orgEnv := directoryOrgKey(t, e, org, e.acct, api.ScopesAdminOnly)
	rec := orgEnv.do(t, http.MethodPost, "/v1/global-apps", api.CreateGlobalAppRequest{Slug: "team-global"}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var app api.GlobalAppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &app); err != nil {
		t.Fatal(err)
	}
	if app.Owner.Kind != "organization" || app.Owner.OrgID == nil || *app.Owner.OrgID != canonicalGlobalOwnerID(org.ID) || app.Owner.AccountID != nil || app.Owner.ID == canonicalGlobalOwnerID(e.acct.ID) || app.Owner.ID == canonicalGlobalOwnerID(org.ID) {
		t.Fatalf("owner: %+v", app.Owner)
	}
	for _, tc := range []struct {
		role  state.OrgRole
		write int
	}{{state.OrgRoleDeveloper, 403}, {state.OrgRoleViewer, 403}, {state.OrgRoleBilling, 403}} {
		t.Run(string(tc.role), func(t *testing.T) {
			actor, err := e.store.CreateAccount(t.Context(), string(tc.role)+"@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			if err = e.store.AddOrgMember(t.Context(), org.ID, actor.ID, tc.role, nil); err != nil {
				t.Fatal(err)
			}
			member := directoryOrgKey(t, e, org, actor, api.ScopesAdminOnly)
			for _, path := range []string{"/v1/global-apps", "/v1/global-apps/team-global", "/v1/global-apps/team-global?org=" + org.Slug} {
				got := member.do(t, http.MethodGet, path, nil, nil)
				if got.Code != 200 {
					t.Fatalf("read: %d %s", got.Code, got.Body)
				}
			}
			got := member.do(t, http.MethodPost, "/v1/global-apps", api.CreateGlobalAppRequest{Slug: "another-" + string(tc.role)}, nil)
			if got.Code != tc.write {
				t.Fatalf("write: %d %s", got.Code, got.Body)
			}
			if tc.role == state.OrgRoleDeveloper {
				assertProblem(t, got, http.StatusForbidden, api.CodePlanLimitApps)
			} else {
				assertProblem(t, got, http.StatusForbidden, api.CodeOrgRoleForbidden)
			}
			if err = e.store.RemoveOrgMember(t.Context(), org.ID, actor.ID); err != nil {
				t.Fatal(err)
			}
			got = member.do(t, http.MethodGet, "/v1/global-apps/team-global", nil, nil)
			if got.Code != 403 && got.Code != 401 {
				t.Fatalf("removed member: %d %s", got.Code, got.Body)
			}
		})
	}
	if got := e.do(t, http.MethodGet, "/v1/global-apps/team-global", nil, nil); got.Code != 404 {
		t.Fatalf("personal isolation: %d %s", got.Code, got.Body)
	}
	got := orgEnv.do(t, http.MethodGet, "/v1/global-apps/team-global", nil, map[string]string{"X-Active-Org": org.Slug})
	if got.Code != 200 {
		t.Fatalf("header: %d %s", got.Code, got.Body)
	}
	other := seedSharedOrgWithOwner(t, e, "global-other", "Other Team", api.PlanPro)
	got = orgEnv.do(t, http.MethodGet, "/v1/global-apps", nil, map[string]string{"X-Active-Org": other.Slug})
	assertProblem(t, got, http.StatusForbidden, api.CodeOrgRoleForbidden)
	for _, path := range []string{"/v1/global-apps/team-global/image-releases/00000000-0000-0000-0000-000000000001"} {
		got = orgEnv.do(t, http.MethodPut, path, map[string]string{}, nil)
		assertProblem(t, got, http.StatusForbidden, api.CodeForbidden)
	}
	if err := e.store.UpdateOrgStatus(t.Context(), org.ID, state.OrgStatusSuspended); err != nil {
		t.Fatal(err)
	}
	got = orgEnv.do(t, http.MethodGet, "/v1/global-apps/team-global", nil, nil)
	assertProblem(t, got, http.StatusForbidden, api.CodeForbidden)
}

func TestGlobalDirectorySessionOrgSelectionDoesNotUseAppSlug(t *testing.T) {
	e := globalDirectoryHarness(t)
	org := seedSharedOrgWithOwner(t, e, "session-org", "Session Org", api.PlanPro)
	for _, tc := range []struct {
		name, url, header string
		shared            bool
	}{{"header", "/v1/global-apps", "session-org", true}, {"query", "/v1/global-apps?org=session-org", "", true}, {"personal", "/v1/global-apps/session-org", "", false}} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.url, nil)
			req.SetPathValue("slug", org.Slug)
			req.Header.Set("X-Active-Org", tc.header)
			req = req.WithContext(middleware.WithPrincipal(req.Context(), e.acct, nil, nil))
			called := false
			wrapped := e.s.requireGlobalDirectoryOwner(false, func(_ http.ResponseWriter, r *http.Request, acct state.Account) {
				called = true
				p := globalOwner(r, acct)
				if p.Owner.Shared() != tc.shared {
					t.Fatalf("wrong tenant: %+v", p)
				}
				if r.PathValue("slug") != org.Slug {
					t.Fatal("adapter lost app slug")
				}
			})
			rec := httptest.NewRecorder()
			wrapped(rec, req, e.acct)
			if !called {
				t.Fatalf("session selection: %d %s", rec.Code, rec.Body)
			}
		})
	}
}
