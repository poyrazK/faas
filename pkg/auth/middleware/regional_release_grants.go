package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type RegionalReleaseGrantAuthenticator interface {
	AuthenticateRegionalReleaseGrant(context.Context, []byte) (state.Account, state.APIKey, error)
}

func (m *Middleware) regionalReleaseBearer(w http.ResponseWriter, r *http.Request, next AccountHandler, token string) bool {
	if !strings.HasPrefix(token, api.RegionalReleaseTokenPrefix) {
		return false
	}
	auth, ok := m.Authn.(RegionalReleaseGrantAuthenticator)
	if !ok || !api.ValidRegionalReleaseTokenFormat(token) {
		regionalReleaseUnauthorized(w, nil)
		return true
	}
	acct, key, err := auth.AuthenticateRegionalReleaseGrant(r.Context(), api.HashAPIKey(token))
	if err != nil {
		regionalReleaseUnauthorized(w, err)
		return true
	}
	if key.AccountID != acct.ID || key.RegionalReleaseGrantID == "" || key.AppID == "" || key.RegionalReleaseCellID == "" || key.RegionalReleaseGlobalAppID == "" || len(key.Scopes) != 1 || key.Scopes[0] != api.ScopeRegionalImageRelease || !regionalReleaseRoute(r, key) {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Regional release delegation only", "This credential is restricted to its regional app, ordered release stream and grant."))
		return true
	}
	if !acct.Active() {
		api.WriteProblem(w, api.NewProblem(http.StatusPaymentRequired, api.CodeBillingPastDue, "Account suspended", "Resolve billing before deploying."))
		return true
	}
	*r = *r.WithContext(withPrincipal(r.Context(), principal{Acct: acct, Key: &key}))
	next(w, r, acct)
	return true
}
func regionalReleaseRoute(r *http.Request, key state.APIKey) bool {
	switch r.Pattern {
	case "GET /v1/cell/apps/{slug}/binding":
		return true
	case "PUT /v1/cells/{cell_id}/apps/{app_id}/image-releases/{release_id}", "GET /v1/cells/{cell_id}/apps/{app_id}/image-releases/{release_id}":
		return r.PathValue("cell_id") == key.RegionalReleaseCellID && r.PathValue("app_id") == key.AppID
	case "GET /v1/cells/{cell_id}/apps/{app_id}/release-grants/{grant_id}", "DELETE /v1/cells/{cell_id}/apps/{app_id}/release-grants/{grant_id}":
		return r.PathValue("cell_id") == key.RegionalReleaseCellID && r.PathValue("app_id") == key.AppID && r.PathValue("grant_id") == key.RegionalReleaseGrantID
	default:
		return false
	}
}
func regionalReleaseUnauthorized(w http.ResponseWriter, err error) {
	setBearerChallenge(w, "invalid_token")
	switch {
	case errors.Is(err, state.ErrAPIKeyExpired):
		api.WriteProblem(w, api.ErrAPIKeyExpired())
	case errors.Is(err, state.ErrAPIKeyRevoked):
		api.WriteProblem(w, api.ErrAPIKeyRevoked())
	default:
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Invalid regional release credential", "Supply an active regional release credential."))
	}
}
