package appaccess

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

// A past-due company keeps its apps running and loses only deploys (spec
// §4.7), as a past-due personal account does: its members still revoke
// credentials, rotate secrets and change configuration, and the app's owner
// account carries past_due so deploy admission refuses. A suspended company is
// locked as a suspended account is: billing recovery only.
func TestPastDueCompanyKeepsManagingAndSuspendedIsLocked(t *testing.T) {
	for _, tc := range []struct {
		status        state.OrgStatus
		role          state.OrgRole
		pattern, path string
		want          int
	}{
		{state.OrgStatusPastDue, state.OrgRoleAdmin, "DELETE /v1/apps/{slug}/consumers/{consumer_id}/keys/{key_id}", "/v1/apps/team-api/consumers/c/keys/k", http.StatusNoContent},
		{state.OrgStatusPastDue, state.OrgRoleAdmin, "DELETE /v1/apps/{slug}/log-drains/{id}", "/v1/apps/team-api/log-drains/d", http.StatusNoContent},
		{state.OrgStatusPastDue, state.OrgRoleDeveloper, "POST /v1/apps/{slug}/secrets/{key}/rotate", "/v1/apps/team-api/secrets/TOKEN/rotate", http.StatusNoContent},
		{state.OrgStatusPastDue, state.OrgRoleDeveloper, "POST /v1/apps/{slug}/park", "/v1/apps/team-api/park", http.StatusNoContent},
		{state.OrgStatusPastDue, state.OrgRoleViewer, "PUT /v1/apps/{slug}/env/{key}", "/v1/apps/team-api/env/LOG_LEVEL", http.StatusForbidden},
		{state.OrgStatusSuspended, state.OrgRoleAdmin, "DELETE /v1/apps/{slug}/consumers/{consumer_id}/keys/{key_id}", "/v1/apps/team-api/consumers/c/keys/k", http.StatusForbidden},
		{state.OrgStatusSuspended, state.OrgRoleDeveloper, "POST /v1/apps/{slug}/secrets/{key}/rotate", "/v1/apps/team-api/secrets/TOKEN/rotate", http.StatusForbidden},
		{state.OrgStatusSuspended, state.OrgRoleViewer, "GET /v1/apps/{slug}", "/v1/apps/team-api", http.StatusPaymentRequired},
	} {
		t.Run(string(tc.status)+"/"+string(tc.role)+"/"+tc.pattern, func(t *testing.T) {
			store, owner, actor, org, app := fixture(t, tc.role)
			if err := store.UpdateOrgStatus(t.Context(), org.ID, tc.status); err != nil {
				t.Fatal(err)
			}
			r := request(actor, &state.APIKey{OrgID: org.ID}, tc.pattern, tc.path, app.Slug, "")
			w := httptest.NewRecorder()
			Wrap(store, nil, func(w http.ResponseWriter, r *http.Request, subject state.Account) {
				if subject.ID != owner.ID || tc.status == state.OrgStatusPastDue && (!subject.Active() || subject.MayDeploy()) {
					t.Fatalf("a past-due company's app reached its handler with owner %+v; want an active account that may not deploy", subject)
				}
				w.WriteHeader(http.StatusNoContent)
			})(w, r, actor)
			if w.Code != tc.want {
				t.Fatalf("status=%d body=%s; want %d", w.Code, w.Body, tc.want)
			}
			if tc.want == http.StatusForbidden && tc.status == state.OrgStatusSuspended && !strings.Contains(w.Body.String(), "inactive organization") {
				t.Fatalf("suspended refusal = %s", w.Body)
			}
		})
	}
}

// Standing applies the same rule to credentials checked outside apid: a
// production fork or dev bridge keeps working while its company is past due
// and stops when the company is suspended.
func TestStandingFollowsPastDueAndSuspendedCompanies(t *testing.T) {
	for status, want := range map[state.OrgStatus]bool{
		state.OrgStatusActive:    true,
		state.OrgStatusPastDue:   true,
		state.OrgStatusSuspended: false,
	} {
		t.Run(string(status), func(t *testing.T) {
			store, _, actor, org, app := fixture(t, state.OrgRoleAdmin)
			if err := store.UpdateOrgStatus(t.Context(), org.ID, status); err != nil {
				t.Fatal(err)
			}
			got, err := Standing(t.Context(), store, actor.ID, app, authz.OrgActionManageAppSecurity)
			if err != nil || got != want {
				t.Fatalf("Standing = %v, %v; want %v", got, err, want)
			}
		})
	}
}
