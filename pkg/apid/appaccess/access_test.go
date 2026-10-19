package appaccess

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func fixture(t *testing.T, role state.OrgRole) (*state.MemStore, state.Account, state.Account, state.Org, state.App) {
	t.Helper()
	store := state.NewMemStore()
	owner, err := store.CreateAccount(t.Context(), "creator@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := store.CreateAccount(t.Context(), "member@example.com", api.PlanFree)
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
	memberRole := role
	if role == state.OrgRoleOwner {
		memberRole = state.OrgRoleDeveloper
	}
	if err := store.AddOrgMember(t.Context(), org.ID, actor.ID, memberRole, nil); err != nil {
		t.Fatal(err)
	}
	if role == state.OrgRoleOwner {
		if err := store.TransferOrgOwnership(t.Context(), org.ID, owner.ID, actor.ID); err != nil {
			t.Fatal(err)
		}
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: owner.ID, OrgID: org.ID, Slug: "team-api", Type: state.AppTypeApp, Runtime: "node22"})
	if err != nil {
		t.Fatal(err)
	}
	return store, owner, actor, org, app
}

func request(actor state.Account, key *state.APIKey, pattern, path, slug, id string) *http.Request {
	method, _, _ := strings.Cut(pattern, " ")
	r := httptest.NewRequest(method, path, nil)
	r.Pattern = pattern
	r.SetPathValue("slug", slug)
	r.SetPathValue("id", id)
	return r.WithContext(authmw.WithPrincipal(r.Context(), actor, key, nil))
}

func TestSharedAppRoleMatrixAndPrincipal(t *testing.T) {
	for _, tc := range []struct {
		role          state.OrgRole
		pattern, path string
		want          int
	}{
		{state.OrgRoleViewer, "GET /v1/apps/{slug}", "/v1/apps/team-api", 204},
		{state.OrgRoleViewer, "PUT /v1/apps/{slug}/env/{key}", "/v1/apps/team-api/env/LOG_LEVEL", 403},
		{state.OrgRoleDeveloper, "PUT /v1/apps/{slug}/env/{key}", "/v1/apps/team-api/env/LOG_LEVEL", 204},
		{state.OrgRoleDeveloper, "PUT /v1/apps/{slug}/secrets/{key}", "/v1/apps/team-api/secrets/TOKEN", 204},
		{state.OrgRoleDeveloper, "PATCH /v1/apps/{slug}/security", "/v1/apps/team-api/security", 403},
		{state.OrgRoleDeveloper, "DELETE /v1/apps/{slug}", "/v1/apps/team-api", 403},
		{state.OrgRoleAdmin, "DELETE /v1/apps/{slug}", "/v1/apps/team-api", 204},
		{state.OrgRoleBilling, "GET /v1/apps/{slug}", "/v1/apps/team-api", 403},
		{state.OrgRoleOwner, "POST /v1/apps/{slug}/unreviewed", "/v1/apps/team-api/unreviewed", 403},
	} {
		t.Run(string(tc.role)+"/"+tc.pattern, func(t *testing.T) {
			store, owner, actor, org, app := fixture(t, tc.role)
			r := request(actor, &state.APIKey{OrgID: org.ID}, tc.pattern, tc.path, app.Slug, "")
			w := httptest.NewRecorder()
			Wrap(store, nil, func(w http.ResponseWriter, r *http.Request, subject state.Account) {
				actual, _, mem, ok := authmw.PrincipalFrom(r)
				if !ok || actual.ID != actor.ID || mem == nil || mem.OrgID != org.ID || subject.ID != owner.ID || subject.Plan != api.PlanPro {
					t.Fatal("actor, membership or resource account changed")
				}
				grant, ok := From(r)
				if !ok || grant.App.ID != app.ID {
					t.Fatal("missing app grant")
				}
				w.WriteHeader(http.StatusNoContent)
			})(w, r, actor)
			if w.Code != tc.want {
				t.Fatalf("status=%d body=%s; want %d", w.Code, w.Body, tc.want)
			}
		})
	}
}

func TestSharedAppCredentialAndMembershipBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, *state.MemStore, *state.Account, *state.APIKey, state.Org, state.App)
		want   int
	}{
		{"legacy key cannot widen", func(_ *testing.T, _ *state.MemStore, _ *state.Account, k *state.APIKey, _ state.Org, _ state.App) {
			k.OrgID = ""
		}, 409},
		{"foreign org key", func(_ *testing.T, _ *state.MemStore, _ *state.Account, k *state.APIKey, _ state.Org, _ state.App) {
			k.OrgID = "foreign"
		}, 404},
		{"foreign app token", func(_ *testing.T, _ *state.MemStore, _ *state.Account, k *state.APIKey, _ state.Org, _ state.App) {
			k.AppID = "foreign"
		}, 404},
		{"removed member", func(t *testing.T, s *state.MemStore, a *state.Account, _ *state.APIKey, o state.Org, _ state.App) {
			if err := s.RemoveOrgMember(t.Context(), o.ID, a.ID); err != nil {
				t.Fatal(err)
			}
		}, 404},
		{"nonmember creator", func(t *testing.T, s *state.MemStore, a *state.Account, _ *state.APIKey, o state.Org, app state.App) {
			*a, _ = s.AccountByID(t.Context(), app.AccountID)
			if err := s.TransferOrgOwnership(t.Context(), o.ID, a.ID, findOtherMember(t, s, o.ID, a.ID)); err != nil {
				t.Fatal(err)
			}
			if err := s.RemoveOrgMember(t.Context(), o.ID, a.ID); err != nil {
				t.Fatal(err)
			}
		}, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _, actor, org, app := fixture(t, state.OrgRoleDeveloper)
			key := state.APIKey{OrgID: org.ID}
			tc.mutate(t, store, &actor, &key, org, app)
			r := request(actor, &key, "GET /v1/apps/{slug}", "/v1/apps/team-api", app.Slug, "")
			w := httptest.NewRecorder()
			Wrap(store, nil, func(http.ResponseWriter, *http.Request, state.Account) { t.Fatal("unauthorized handler ran") })(w, r, actor)
			if w.Code != tc.want {
				t.Fatalf("status=%d body=%s; want %d", w.Code, w.Body, tc.want)
			}
		})
	}
}

func findOtherMember(t *testing.T, s *state.MemStore, orgID, accountID string) string {
	t.Helper()
	members, err := s.ListOrgMembers(t.Context(), orgID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range members {
		if m.AccountID != accountID {
			return m.AccountID
		}
	}
	t.Fatal("missing member")
	return ""
}

// Persisted account keys carry their owner's personal-org binding (Postgres
// stamps it; MemStore leaves it empty). They remain account keys: the creator
// keeps access to an app a team adopted, and another member needs an org key.
func TestSharedAppPersonalAccountKeys(t *testing.T) {
	for _, tc := range []struct {
		name    string
		creator bool
		want    int
	}{
		{"creator", true, http.StatusNoContent},
		{"member", false, http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, owner, actor, _, app := fixture(t, state.OrgRoleDeveloper)
			if tc.creator {
				actor = owner
			}
			ownerID := actor.ID
			personal, err := store.CreateOrg(t.Context(), state.Org{Slug: "personal-" + tc.name, Name: "Personal", Plan: api.PlanPro, Personal: true, PersonalOwnerAccountID: &ownerID})
			if err != nil {
				t.Fatal(err)
			}
			r := request(actor, &state.APIKey{OrgID: personal.ID}, "GET /v1/apps/{slug}", "/v1/apps/team-api", app.Slug, "")
			w := httptest.NewRecorder()
			Wrap(store, nil, func(w http.ResponseWriter, _ *http.Request, _ state.Account) { w.WriteHeader(http.StatusNoContent) })(w, r, actor)
			if w.Code != tc.want {
				t.Fatalf("status=%d body=%s; want %d", w.Code, w.Body, tc.want)
			}
		})
	}
}
