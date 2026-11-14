package appaccess

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestIssueRouteActionsPreserveEvidenceAndIntegrationPolicy(t *testing.T) {
	for pattern, want := range map[string]authz.OrgAction{
		"GET /v1/apps/{slug}/issues":                                   authz.OrgActionReadAppLogs,
		"GET /v1/apps/{slug}/issues/{issue_id}":                        authz.OrgActionReadAppLogs,
		"POST /v1/apps/{slug}/issues/{issue_id}/actions":               authz.OrgActionConfigureApp,
		"POST /v1/apps/{slug}/issues/{issue_id}/native-tickets":        authz.OrgActionManageAppSecurity,
		"POST /v1/apps/{slug}/issues/{issue_id}/native-linear-tickets": authz.OrgActionManageAppSecurity,
		"PUT /v1/apps/{slug}/issue-frequency-alert-policy":             authz.OrgActionManageAppSecurity,
		"PUT /v1/apps/{slug}/issue-tracker-linear-connection":          authz.OrgActionManageAppSecurity,
		"POST /dashboard/apps/{slug}/issues/tracker-notifications":     authz.OrgActionManageAppSecurity,
		"DELETE /v1/apps/{slug}/issues/{issue_id}":                     authz.OrgActionDeployApp,
	} {
		if got := routeActions[pattern]; got != want {
			t.Errorf("%s: got %q, want %q", pattern, got, want)
		}
	}
}

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
		{state.OrgRoleDeveloper, "POST /v1/apps/{slug}/issues/{issue_id}/native-tickets", "/v1/apps/team-api/issues/a/native-tickets", 403},
		{state.OrgRoleAdmin, "POST /v1/apps/{slug}/issues/{issue_id}/native-tickets", "/v1/apps/team-api/issues/a/native-tickets", 204},
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

func TestDashboardIssueNotificationSettingsRequireAdmin(t *testing.T) {
	const pattern = "POST /dashboard/apps/{slug}/issues/tracker-notifications"
	for _, tc := range []struct {
		role state.OrgRole
		want int
	}{
		{state.OrgRoleDeveloper, http.StatusForbidden},
		{state.OrgRoleAdmin, http.StatusNoContent},
	} {
		t.Run(string(tc.role), func(t *testing.T) {
			store, owner, actor, org, app := fixture(t, tc.role)
			r := request(actor, &state.APIKey{OrgID: org.ID}, pattern, "/dashboard/apps/team-api/issues/tracker-notifications", app.Slug, "")
			subject, problem := Authorize(r, store, nil, actor, app)
			if tc.want == http.StatusForbidden {
				if problem == nil || problem.Status != tc.want {
					t.Fatalf("authorization = %v, want forbidden", problem)
				}
				return
			}
			if problem != nil || subject.ID != owner.ID {
				t.Fatalf("authorization = %v, subject = %q", problem, subject.ID)
			}
		})
	}
}
