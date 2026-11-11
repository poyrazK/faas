package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// OrgByIDResolver is the optional store surface the organization-key fence
// reads to tell a company key from a personal one.
type OrgByIDResolver interface {
	OrgByID(ctx context.Context, id string) (state.Org, error)
}

// personalIdentityPrefixes are the routes that act on the key holder's own
// identity, credentials, account lifecycle or personal billing. A key bound
// to a company organization was issued for that organization's work, so it
// must not reach them: otherwise a leaked company CI key (or one obtained
// through the organization) mints unrestricted personal keys, exports the
// personal account or deletes it (account-flow audit 2026-11-10, ADR
// 20261110190000).
var personalIdentityPrefixes = []string{
	"/v1/keys",
	"/v1/auth/sessions",
	"/v1/account/export",
	"/v1/account/restore",
	"/v1/account/email",
	"/v1/account/mfa",
	"/v1/account/keys",
	"/v1/account/sign-in-methods",
	"/v1/account/security-activity",
	"/v1/account/billing",
	"/v1/account/plan",
}

// personalIdentityRoute reports whether r addresses one of those routes.
// GET /v1/account (who am I) stays open: the CLI reads it with every key.
func personalIdentityRoute(r *http.Request) bool {
	path := r.URL.Path
	if path == "/v1/account" {
		return r.Method != http.MethodGet && r.Method != http.MethodHead
	}
	for _, prefix := range personalIdentityPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

// refuseOrgKeyOnPersonalRoute answers 403 when a key bound to a company
// organization calls a personal identity route. Keys bound to the holder's
// personal organization (every key minted by POST /v1/keys) pass.
func (m *Middleware) refuseOrgKeyOnPersonalRoute(w http.ResponseWriter, r *http.Request, acct state.Account, key state.APIKey) bool {
	if key.OrgID == "" || key.AppID != "" || !personalIdentityRoute(r) {
		return false
	}
	orgs, ok := m.Authn.(OrgByIDResolver)
	if !ok {
		return false
	}
	org, err := orgs.OrgByID(r.Context(), key.OrgID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		m.storeUnavailable(w, r, err, "could not check the key's organization; retry shortly")
		return true
	}
	if err == nil && org.Personal && org.PersonalOwnerAccountID != nil && *org.PersonalOwnerAccountID == acct.ID {
		return false
	}
	if m.Audit != nil {
		m.Audit.Emit(r.Context(), "key.org_key_personal_route_refused", &acct.ID, map[string]any{
			"key_id": key.ID, "org_id": key.OrgID, "method": r.Method, "path": r.URL.Path,
		})
	}
	api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Organization key",
		"this key is bound to an organization and cannot manage your account, keys or sessions; use a personal key or the dashboard"))
	return true
}
