package appaccess

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// A company app's credentials outlive the member who minted them, so its
// owners and admins list, rotate and revoke them whoever created the app;
// developers and viewers do not. They used to fall back to the creator alone
// (ADR-20261026151103 amendment 1).
func TestCompanyAppCredentialRoutesBelongToOwnersAndAdmins(t *testing.T) {
	patterns := []string{
		"GET /v1/apps/{slug}/consumers",
		"GET /v1/apps/{slug}/consumers/{consumer_id}",
		"DELETE /v1/apps/{slug}/consumers/{consumer_id}",
		"GET /v1/apps/{slug}/consumers/{consumer_id}/keys",
		"POST /v1/apps/{slug}/consumers/{consumer_id}/keys",
		"DELETE /v1/apps/{slug}/consumers/{consumer_id}/keys/{key_id}",
		"GET /v1/apps/{slug}/inbound-webhooks",
		"GET /v1/apps/{slug}/inbound-webhooks/{id}",
		"PATCH /v1/apps/{slug}/inbound-webhooks/{id}",
		"DELETE /v1/apps/{slug}/inbound-webhooks/{id}",
		"GET /v1/apps/{slug}/issue-ingest-tokens",
		"POST /v1/apps/{slug}/issue-ingest-tokens",
		"DELETE /v1/apps/{slug}/issue-ingest-tokens/{token_id}",
		"GET /v1/apps/{slug}/realtime/endpoints",
		"GET /v1/apps/{slug}/realtime/endpoints/{id}",
		"PATCH /v1/apps/{slug}/realtime/endpoints/{id}",
		"POST /v1/apps/{slug}/realtime/endpoints/{id}/auth/rotate",
		"POST /v1/apps/{slug}/realtime/endpoints/{id}/auth/rotate/finalize",
		"GET /v1/apps/{slug}/realtime/endpoints/{id}/publish-credentials",
		"POST /v1/apps/{slug}/realtime/endpoints/{id}/publish-credentials",
		"POST /v1/apps/{slug}/realtime/endpoints/{id}/publish-credentials/{credential_id}/rotate",
		"POST /v1/apps/{slug}/realtime/endpoints/{id}/publish-credentials/{credential_id}/revoke",
		"GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/credentials",
		"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/credentials",
		"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/credentials/{credential_id}/rotate",
		"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/credentials/{credential_id}/revoke",
		"GET /v1/apps/{slug}/ai-access-tokens",
		"DELETE /v1/apps/{slug}/ai-access-tokens/{token}",
	}
	roles := map[state.OrgRole]int{
		state.OrgRoleOwner:     http.StatusNoContent,
		state.OrgRoleAdmin:     http.StatusNoContent,
		state.OrgRoleDeveloper: http.StatusForbidden,
		state.OrgRoleViewer:    http.StatusForbidden,
	}
	for _, pattern := range patterns {
		for role, want := range roles {
			t.Run(string(role)+"/"+pattern, func(t *testing.T) {
				store, owner, actor, org, app := fixture(t, role)
				_, path, _ := strings.Cut(strings.ReplaceAll(pattern, "{slug}", app.Slug), " ")
				r := request(actor, &state.APIKey{OrgID: org.ID}, pattern, path, app.Slug, "")
				w := httptest.NewRecorder()
				Wrap(store, nil, func(w http.ResponseWriter, r *http.Request, subject state.Account) {
					if grant, ok := From(r); !ok || grant.App.ID != app.ID || grant.Actor.ID != actor.ID || subject.ID != owner.ID {
						t.Fatal("credential route reached its handler without the app's grant")
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
