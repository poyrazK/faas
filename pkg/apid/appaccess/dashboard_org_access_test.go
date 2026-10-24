package appaccess

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// The session-only dashboard app routes take the same decision as /v1: a
// creator who was removed from the company or demoted loses the app there
// too (the handlers alone only checked that the caller created it).
func TestDashboardAppRoutesFollowOrganizationAccess(t *testing.T) {
	for _, tc := range []struct {
		name          string
		role          state.OrgRole
		pattern, path string
		removed       bool
		want          int
	}{
		{"viewer reads a page", state.OrgRoleViewer, "GET /dashboard/apps/{slug}/changes", "/dashboard/apps/team-api/changes", false, 0},
		{"viewer cannot set env", state.OrgRoleViewer, "POST /dashboard/apps/{slug}/env", "/dashboard/apps/team-api/env", false, http.StatusForbidden},
		{"developer sets env", state.OrgRoleDeveloper, "POST /dashboard/apps/{slug}/env", "/dashboard/apps/team-api/env", false, 0},
		{"developer rotates a secret", state.OrgRoleDeveloper, "POST /dashboard/apps/{slug}/secrets/{key}/rotate", "/dashboard/apps/team-api/secrets/TOKEN/rotate", false, 0},
		{"developer cannot use an unreviewed write", state.OrgRoleDeveloper, "POST /dashboard/apps/{slug}/webhooks", "/dashboard/apps/team-api/webhooks", false, http.StatusForbidden},
		{"admin creator uses an unreviewed write", state.OrgRoleAdmin, "POST /dashboard/apps/{slug}/webhooks", "/dashboard/apps/team-api/webhooks", false, 0},
		{"developer cannot bind GitHub", state.OrgRoleDeveloper, "POST /v1/apps/{slug}/install/bind", "/v1/apps/team-api/install/bind", false, http.StatusForbidden},
		{"billing cannot read", state.OrgRoleBilling, "GET /dashboard/apps/{slug}/changes", "/dashboard/apps/team-api/changes", false, http.StatusForbidden},
		{"removed creator", state.OrgRoleAdmin, "POST /dashboard/apps/{slug}/env", "/dashboard/apps/team-api/env", true, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, creator, org := creatorFixture(t, tc.role)
			if tc.removed {
				if err := store.RemoveOrgMember(t.Context(), org.ID, creator.ID); err != nil {
					t.Fatal(err)
				}
			}
			app, err := store.AppBySlug(t.Context(), "team-api")
			if err != nil {
				t.Fatal(err)
			}
			r := request(creator, nil, tc.pattern, tc.path, app.Slug, "")
			_, problem := Authorize(r, store, nil, creator, app)
			switch {
			case tc.want == 0 && problem != nil:
				t.Fatalf("refused: %v", problem)
			case tc.want != 0 && (problem == nil || problem.Status != tc.want):
				t.Fatalf("problem = %v, want status %d", problem, tc.want)
			}
		})
	}
}

// creatorFixture makes the app's creator a member of a company org with role,
// next to a separate owner, as after a demotion.
func creatorFixture(t *testing.T, role state.OrgRole) (*state.MemStore, state.Account, state.Org) {
	t.Helper()
	store := state.NewMemStore()
	owner, err := store.CreateAccount(t.Context(), "owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	creator, err := store.CreateAccount(t.Context(), "creator@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(t.Context(), state.Org{Slug: "acme", Name: "Acme", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(t.Context(), org.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(t.Context(), org.ID, creator.ID, role, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(t.Context(), state.App{AccountID: creator.ID, OrgID: org.ID, Slug: "team-api", Type: state.AppTypeApp, Runtime: "node22"}); err != nil {
		t.Fatal(err)
	}
	return store, creator, org
}

// A key bound to an app, or to another company, never acts on an
// organization's routes; account keys and the holder's personal (CLI) key
// still do, limited by the route's scope and the holder's role.
func TestWrapOrganizationConfinesBoundKeys(t *testing.T) {
	store, _, actor, org, _ := fixture(t, state.OrgRoleAdmin)
	actorID := actor.ID
	personal, err := store.CreateOrg(t.Context(), state.Org{Slug: "personal-member", Name: "Personal", Plan: api.PlanFree, Personal: true, PersonalOwnerAccountID: &actorID})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateOrg(t.Context(), state.Org{Slug: "other-co", Name: "Other", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	mem, err := store.OrgMemberByAccount(t.Context(), org.ID, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		key  *state.APIKey
		want int
	}{
		{"session", nil, http.StatusNoContent},
		{"key bound to this org", &state.APIKey{ID: "k1", OrgID: org.ID}, http.StatusNoContent},
		{"account key", &state.APIKey{ID: "k2"}, http.StatusNoContent},
		{"holder's personal key", &state.APIKey{ID: "k3", OrgID: personal.ID}, http.StatusNoContent},
		{"key bound to another company", &state.APIKey{ID: "k4", OrgID: other.ID}, http.StatusForbidden},
		{"app-bound key", &state.APIKey{ID: "k5", OrgID: org.ID, AppID: "app_1"}, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/v1/orgs/acme/members", nil)
			r.Pattern = "POST /v1/orgs/{slug}/members"
			r.SetPathValue("slug", "acme")
			r = r.WithContext(authmw.WithPrincipal(r.Context(), actor, tc.key, &mem))
			w := httptest.NewRecorder()
			WrapOrganization(store, func(w http.ResponseWriter, _ *http.Request, _ state.Account) {
				w.WriteHeader(http.StatusNoContent)
			})(w, r, actor)
			if w.Code != tc.want {
				t.Fatalf("status=%d body=%s; want %d", w.Code, w.Body, tc.want)
			}
		})
	}
}
