package toolregistry

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// Reload role, revocation and project facts even when authentication was cached.
func (e *PrivateExecutor) freshPublicationRequest(r *http.Request, acct state.Account, owner bool) (*http.Request, error) {
	p, ok := httpkit.PrincipalFrom(r)
	if !ok || p.Acct.ID != acct.ID {
		return r, state.ErrNotFound
	}
	if p.Key == nil {
		if owner {
			return r, nil
		}
		return r, state.ErrNotFound
	}
	if p.Key.AppID != "" || p.Key.HostingSiteID != "" || p.Key.PlatformTenantID != "" || p.Key.ProjectLimited {
		return r, state.ErrNotFound
	}
	originalAdmin := httpkit.APIKeyCarriesScope(*p.Key, api.ScopeAdmin)
	if owner && !originalAdmin || !owner && !originalAdmin && !httpkit.APIKeyCarriesScope(*p.Key, api.ScopeDeployWrite) {
		return r, state.ErrNotFound
	}
	k, err := e.store.GetAPIKey(r.Context(), acct.ID, p.Key.ID)
	if err != nil || k.ID != p.Key.ID || k.AccountID != acct.ID || k.Status != "active" && k.Status != "grace" || k.RevokedAt != nil || k.ExpiresAt != nil && !time.Now().Before(*k.ExpiresAt) || k.AppID != "" || k.HostingSiteID != "" || k.PlatformTenantID != "" {
		return r, state.ErrNotFound
	}
	admin := httpkit.APIKeyCarriesScope(k, api.ScopeAdmin)
	if owner && !admin || !owner && !admin && !httpkit.APIKeyCarriesScope(k, api.ScopeDeployWrite) {
		return r, state.ErrNotFound
	}
	limiter, ok := e.store.(middleware.KeyProjectLimiter)
	if !ok {
		return r, state.ErrNotFound
	}
	_, limited, err := limiter.APIKeyProjectLimit(r.Context(), k.ID)
	if err != nil || limited {
		return r, state.ErrNotFound
	}
	return r.WithContext(middleware.WithPrincipal(r.Context(), acct, &k, nil)), nil
}
