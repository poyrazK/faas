package appaccess

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// A verification contract gates promotion (ADR-20261122220000): any member
// who can read the app can read its contract, but changing or removing one
// changes a release gate, so only owners and admins may, like the release
// approval policy. Two PRs once added these routes with different actions and
// main stopped compiling on the duplicate map keys.
func TestVerificationContractRoutesGateChangesToOwnersAndAdmins(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		want    map[state.OrgRole]int
	}{
		{"GET /v1/apps/{slug}/verification-contract", map[state.OrgRole]int{
			state.OrgRoleOwner: http.StatusNoContent, state.OrgRoleAdmin: http.StatusNoContent,
			state.OrgRoleDeveloper: http.StatusNoContent, state.OrgRoleViewer: http.StatusNoContent,
		}},
		{"PUT /v1/apps/{slug}/verification-contract", map[state.OrgRole]int{
			state.OrgRoleOwner: http.StatusNoContent, state.OrgRoleAdmin: http.StatusNoContent,
			state.OrgRoleDeveloper: http.StatusForbidden, state.OrgRoleViewer: http.StatusForbidden,
		}},
		{"DELETE /v1/apps/{slug}/verification-contract", map[state.OrgRole]int{
			state.OrgRoleOwner: http.StatusNoContent, state.OrgRoleAdmin: http.StatusNoContent,
			state.OrgRoleDeveloper: http.StatusForbidden, state.OrgRoleViewer: http.StatusForbidden,
		}},
	} {
		for role, want := range tc.want {
			t.Run(tc.pattern+"/"+string(role), func(t *testing.T) {
				store, _, actor, org, app := fixture(t, role)
				_, path, _ := strings.Cut(strings.ReplaceAll(tc.pattern, "{slug}", app.Slug), " ")
				r := request(actor, &state.APIKey{OrgID: org.ID}, tc.pattern, path, app.Slug, "")
				w := httptest.NewRecorder()
				Wrap(store, nil, func(w http.ResponseWriter, _ *http.Request, _ state.Account) {
					w.WriteHeader(http.StatusNoContent)
				})(w, r, actor)
				if w.Code != want {
					t.Fatalf("status=%d body=%s; want %d", w.Code, w.Body, want)
				}
			})
		}
	}
}
