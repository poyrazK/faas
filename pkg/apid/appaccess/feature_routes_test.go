package appaccess

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// The reviewed feature endpoints reach a company app's members by role, not
// only its creator (bug hunt 37): one endpoint per rule.
func TestFeatureRoutesFollowOrganizationRoles(t *testing.T) {
	const ok, no = http.StatusNoContent, http.StatusForbidden
	for _, c := range []struct {
		pattern                         string
		owner, admin, developer, viewer int
	}{
		{"POST /v1/apps/{slug}/deployments/source-ref", ok, ok, ok, no}, // deploy
		{"POST /v1/apps/{slug}/deployments/dev-source", ok, ok, ok, no}, // deploy
		{"GET /v1/apps/{slug}/instances", ok, ok, ok, ok},               // read
		{"GET /v1/apps/{slug}/debug/requests/{req_id}", ok, ok, ok, ok}, // read logs
		{"PUT /v1/apps/{slug}/work-policies/{name}", ok, ok, ok, no},    // configure
		{"PUT /v1/apps/{slug}/recovery-policy", ok, ok, no, no},         // policy
		{"GET /v1/apps/{slug}/deploy-tokens", ok, ok, no, no},           // credentials
		{"GET /v1/apps/{slug}/product/customers", ok, ok, no, no},       // end-user data
		{"POST /v1/apps/{slug}/mirrors", ok, ok, no, no},                // traffic copy
		{"PUT /v1/apps/{slug}/secret-references/{key}", ok, ok, ok, no}, // secrets
		{"GET /v1/apps/{slug}/entities/connect", ok, ok, no, no},        // live data access
		{"POST /v1/apps/{slug}/github/bind", ok, ok, no, no},            // repository binding
		{"GET /v1/apps/{slug}/outbound-bindings/{integration}/usage", ok, ok, ok, ok},
	} {
		for role, want := range map[state.OrgRole]int{
			state.OrgRoleOwner: c.owner, state.OrgRoleAdmin: c.admin,
			state.OrgRoleDeveloper: c.developer, state.OrgRoleViewer: c.viewer,
		} {
			t.Run(string(role)+"/"+c.pattern, func(t *testing.T) {
				store, owner, actor, org, app := fixture(t, role)
				_, path, _ := strings.Cut(strings.ReplaceAll(c.pattern, "{slug}", app.Slug), " ")
				r := request(actor, &state.APIKey{OrgID: org.ID}, c.pattern, path, app.Slug, "")
				w := httptest.NewRecorder()
				Wrap(store, nil, func(w http.ResponseWriter, r *http.Request, subject state.Account) {
					if grant, ok := From(r); !ok || grant.App.ID != app.ID || grant.Actor.ID != actor.ID || subject.ID != owner.ID {
						t.Fatal("reached the handler without the app's grant")
					}
					w.WriteHeader(http.StatusNoContent)
				})(w, r, actor)
				if w.Code != want {
					t.Fatalf("status=%d body=%s; want %d", w.Code, w.Body, want)
				}
			})
		}
	}
}
