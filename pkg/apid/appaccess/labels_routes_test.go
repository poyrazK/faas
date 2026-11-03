package appaccess

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// Labeling a company app is configuring it: owners, admins and developers
// may, viewers may not. The route was unreviewed, so only the creator could
// label a company app, and only while an owner or admin (bug hunt 36).
func TestCompanyAppLabelsFollowConfigurePermission(t *testing.T) {
	const pattern = "PATCH /v1/apps/{slug}/labels"
	for role, want := range map[state.OrgRole]int{
		state.OrgRoleOwner:     http.StatusNoContent,
		state.OrgRoleAdmin:     http.StatusNoContent,
		state.OrgRoleDeveloper: http.StatusNoContent,
		state.OrgRoleViewer:    http.StatusForbidden,
	} {
		t.Run(string(role), func(t *testing.T) {
			store, owner, actor, org, app := fixture(t, role)
			_, path, _ := strings.Cut(strings.ReplaceAll(pattern, "{slug}", app.Slug), " ")
			r := request(actor, &state.APIKey{OrgID: org.ID}, pattern, path, app.Slug, "")
			w := httptest.NewRecorder()
			Wrap(store, nil, func(w http.ResponseWriter, r *http.Request, subject state.Account) {
				if grant, ok := From(r); !ok || grant.App.ID != app.ID || grant.Actor.ID != actor.ID || subject.ID != owner.ID {
					t.Fatal("labels route reached its handler without the app's grant")
				}
				w.WriteHeader(http.StatusNoContent)
			})(w, r, actor)
			if w.Code != want {
				t.Fatalf("status=%d body=%s; want %d", w.Code, w.Body, want)
			}
		})
	}
}
