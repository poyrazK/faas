package appaccess

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestCompanyReceiptReadsFollowAppReadPermission(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		path    string
	}{
		{"GET /v1/apps/{slug}/deployment-promotions/{attempt_id}", "/v1/apps/team-api/deployment-promotions/example"},
		{"GET /v1/apps/{slug}/service-aborts/{attempt_id}", "/v1/apps/team-api/service-aborts/example"},
		{"GET /v1/apps/{slug}/route-budgets/receipts/{request_id}", "/v1/apps/team-api/route-budgets/receipts/example"},
	} {
		for _, role := range []state.OrgRole{state.OrgRoleViewer, state.OrgRoleBilling} {
			t.Run(string(role)+"/"+tc.pattern, func(t *testing.T) {
				store, owner, actor, org, app := fixture(t, role)
				r := request(actor, &state.APIKey{OrgID: org.ID}, tc.pattern, tc.path, app.Slug, "")
				w := httptest.NewRecorder()
				Wrap(store, nil, func(w http.ResponseWriter, r *http.Request, subject state.Account) {
					grant, ok := From(r)
					if !ok || grant.App.ID != app.ID || grant.Actor.ID != actor.ID || subject.ID != owner.ID {
						t.Fatal("receipt read reached handler without its app grant")
					}
					w.WriteHeader(http.StatusNoContent)
				})(w, r, actor)
				want := http.StatusNoContent
				if role == state.OrgRoleBilling {
					want = http.StatusForbidden
				}
				if w.Code != want {
					t.Fatalf("status=%d body=%s; want %d", w.Code, w.Body, want)
				}
			})
		}
	}
}
