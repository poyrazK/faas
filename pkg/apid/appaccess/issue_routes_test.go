package appaccess

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestCompanyIssueRoutesFollowRoles(t *testing.T) {
	for _, tc := range []struct {
		role          state.OrgRole
		pattern, path string
		want          int
	}{
		{state.OrgRoleViewer, "GET /v1/apps/{slug}/issues", "/v1/apps/team-api/issues", 204},
		{state.OrgRoleDeveloper, "POST /v1/apps/{slug}/issues/{issue_id}/actions", "/v1/apps/team-api/issues/a/actions", 204},
		{state.OrgRoleViewer, "POST /v1/apps/{slug}/issues/{issue_id}/actions", "/v1/apps/team-api/issues/a/actions", 403},
		{state.OrgRoleDeveloper, "PUT /v1/apps/{slug}/issue-frequency-alert-policy", "/v1/apps/team-api/issue-frequency-alert-policy", 403},
		{state.OrgRoleAdmin, "PUT /v1/apps/{slug}/issue-frequency-alert-policy", "/v1/apps/team-api/issue-frequency-alert-policy", 204},
	} {
		t.Run(string(tc.role)+"/"+tc.pattern, func(t *testing.T) {
			store, owner, actor, org, app := fixture(t, tc.role)
			r := request(actor, &state.APIKey{OrgID: org.ID}, tc.pattern, tc.path, app.Slug, "")
			w := httptest.NewRecorder()
			Wrap(store, nil, func(w http.ResponseWriter, r *http.Request, subject state.Account) {
				grant, ok := From(r)
				if !ok || grant.App.ID != app.ID || subject.ID != owner.ID {
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
