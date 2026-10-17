package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
	"github.com/onebox-faas/faas/pkg/state"
)

type HostingAutomationAuthenticator interface {
	AuthenticateHostingAutomationToken(context.Context, []byte) (state.Account, state.APIKey, error)
}

// HostingAutomationRequestAllowed is an explicit endpoint allowlist, not a
// prefix grant. New hosting endpoints gain no automation authority by default.
func HostingAutomationRequestAllowed(r *http.Request, key state.APIKey) bool {
	if uuid.Validate(key.HostingSiteID) != nil || !hosting.ValidHostingAutomationOperations(key.HostingOperations) || r.URL.EscapedPath() != r.URL.Path {
		return false
	}
	ops := map[string]bool{}
	for _, op := range key.HostingOperations {
		ops[op] = true
	}
	if r.Method == http.MethodGet && r.URL.Path == "/v1/internal/hosting/capabilities" {
		return true
	}
	root := "/v1/internal/hosting/sites/" + key.HostingSiteID
	if r.Method == http.MethodGet && (r.URL.Path == root || r.URL.Path == root+"/status") {
		return true
	}
	suffix, ok := strings.CutPrefix(r.URL.Path, root+"/")
	if !ok {
		return false
	}
	if r.Method == http.MethodGet && suffix == "diagnostics" && ops["stage"] && ops["preview"] {
		return true
	}
	if r.Method == http.MethodGet && (suffix == "releases" && ops["stage"] || suffix == "previews" && ops["preview"]) {
		return true
	}
	if r.Method == http.MethodPost && (suffix == "releases" && ops["stage"] || suffix == "previews" && ops["preview"]) {
		return true
	}
	parts := strings.Split(suffix, "/")
	if len(parts) == 3 && parts[0] == "releases" && uuid.Validate(parts[1]) == nil && r.Method == http.MethodPost && ops["stage"] && (parts[2] == "uploads" || parts[2] == "finalize") {
		return true
	}
	if len(parts) >= 2 && parts[0] == "ci" && uuid.Validate(parts[1]) == nil {
		return len(parts) == 2 && (r.Method == http.MethodGet || r.Method == http.MethodPut && ops["stage"]) || len(parts) == 3 && parts[2] == "preview" && r.Method == http.MethodPost && ops["preview"]
	}
	return false
}
func (m *Middleware) hostingAutomation(w http.ResponseWriter, r *http.Request, token string, next AccountHandler) bool {
	if !strings.HasPrefix(token, api.HostingAutomationTokenPrefix) {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	if m.HostingAutomationEnabled == nil || !m.HostingAutomationEnabled() {
		api.WriteProblem(w, api.NewProblem(503, "frontend_hosting_automation_disabled", "Site automation unavailable", "internal site automation is disabled"))
		return true
	}
	auth, ok := m.Authn.(HostingAutomationAuthenticator)
	if !ok || !api.ValidHostingAutomationToken(token) {
		api.WriteProblem(w, api.NewProblem(401, api.CodeUnauthorized, "Unauthorized", "provide a valid site automation credential"))
		return true
	}
	account, key, err := auth.AuthenticateHostingAutomationToken(r.Context(), api.HashAPIKey(token))
	if err != nil {
		if errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrAPIKeyExpired) || errors.Is(err, state.ErrAPIKeyRevoked) {
			api.WriteProblem(w, api.NewProblem(401, api.CodeUnauthorized, "Unauthorized", "provide a valid unexpired site automation credential"))
		} else {
			m.credentialStoreUnavailable(w, r, err)
		}
		return true
	}
	if key.HostingOIDCTrustID != "" && (m.HostingOIDCEnabled == nil || !m.HostingOIDCEnabled()) {
		api.WriteProblem(w, api.NewProblem(503, "frontend_hosting_oidc_disabled", "GitHub site identity unavailable", "internal hosting OIDC is disabled"))
		return true
	}
	if key.AccountID != account.ID || key.ID == "" || !HostingAutomationRequestAllowed(r, key) {
		api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Site credential scope is limited", "this credential cannot access this site or operation"))
		return true
	}
	if !account.Active() {
		api.WriteProblem(w, api.NewProblem(402, api.CodeBillingPastDue, "Account suspended", "resolve account status before deploying"))
		return true
	}
	*r = *r.WithContext(withPrincipal(r.Context(), principal{Acct: account, Key: &key, Membership: nil}))
	next(w, r, account)
	return true
}
