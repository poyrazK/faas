package toolregistry

import (
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/agentconnections"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolgrant"
)

func agentRegistryRequest(r *http.Request) bool {
	p, ok := httpkit.PrincipalFrom(r)
	return ok && p.Key != nil && agentconnections.Kind(*p.Key) != ""
}

// Reload current key/project facts even if authentication was cached.
// A rotated replacement key needs another explicit grant.
func grantKey(r *http.Request, acct state.Account, store state.Store, id string) (state.APIKey, error) {
	if !toolgrant.ValidResourceID(id) {
		return state.APIKey{}, state.ErrNotFound
	}
	k, err := store.GetAPIKey(r.Context(), acct.ID, id)
	if err != nil {
		return k, err
	}
	if k.ID != id {
		return k, state.ErrNotFound
	}
	kind := agentconnections.Kind(k)
	if (kind != api.AgentConnectionAgentKey && kind != api.AgentConnectionHostedMCP) || !slices.Contains(k.Scopes, api.ScopeAppsRead) || slices.Contains(k.Scopes, api.ScopeAdmin) ||
		(k.Status != "active" && k.Status != "grace") || k.RevokedAt != nil || k.AppID != "" || k.PlatformTenantID != "" || k.HostingSiteID != "" || (k.ExpiresAt != nil && !time.Now().Before(*k.ExpiresAt)) {
		return k, state.ErrNotFound
	}
	if k.OrgID != "" {
		personal, orgErr := store.OrgByPersonalAccount(r.Context(), acct.ID)
		if orgErr != nil {
			return k, orgErr
		}
		if k.OrgID != personal.ID {
			return k, state.ErrNotFound
		}
	}
	limiter, ok := store.(middleware.KeyProjectLimiter)
	if !ok {
		return k, errors.New("connection project limits unavailable")
	}
	k.ProjectIDs, k.ProjectLimited, err = limiter.APIKeyProjectLimit(r.Context(), k.ID)
	return k, err
}

func registryKey(w http.ResponseWriter, r *http.Request, acct state.Account, store state.Store) (state.APIKey, bool) {
	p, ok := httpkit.PrincipalFrom(r)
	if !ok || p.Acct.ID != acct.ID || p.Key == nil || p.Key.AppID != "" || p.Key.PlatformTenantID != "" || p.Key.HostingSiteID != "" || r.Header.Get("X-Active-Org") != "" || r.URL.Query().Get("org") != "" {
		grantRespond(w, nil, state.ErrNotFound)
		return state.APIKey{}, false
	}
	k, err := grantKey(r, acct, store, p.Key.ID)
	if err != nil {
		grantRespond(w, nil, err)
		return k, false
	}
	return k, true
}

func keyAllowsApp(key state.APIKey, app state.App) bool {
	return !middleware.AppOutsideKeyProjects(&key, app)
}

func publicationApp(r *http.Request, acct state.Account, store state.Store, p api.ToolPublicationRecord) (state.App, error) {
	if p.AccountID != acct.ID || p.Status != "completed" || p.Outcome == nil {
		return state.App{}, state.ErrNotFound
	}
	appID := p.Intent.AppID
	if appID == "" {
		dep, err := store.DeploymentByID(r.Context(), p.Outcome.DeploymentID)
		if err != nil {
			return state.App{}, err
		}
		appID = dep.AppID
	}
	app, err := store.AppByID(r.Context(), appID)
	if err != nil {
		return app, err
	}
	if app.Slug != p.Intent.App || !personalApp(r, acct, store, app) {
		return app, state.ErrNotFound
	}
	return app, nil
}

func accessibleGrant(r *http.Request, acct state.Account, store state.Store, key state.APIKey, e state.ToolGrantEntry) (state.App, bool, error) {
	g, p := e.Grant, e.Publication
	if !toolgrant.Active(g, time.Now()) || g.AccountID != acct.ID || g.Intent.KeyID != key.ID || g.Intent.Version != p.ID {
		return state.App{}, false, nil
	}
	if _, ok := p.Intent.ToolScopes[g.Intent.Name]; !ok {
		return state.App{}, false, nil
	}
	app, err := publicationApp(r, acct, store, p)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return app, false, nil
		}
		return app, false, err
	}
	if g.Credential != nil {
		if g.Credential.ConnectionID != "" {
			_, err := connectionForGrant(r.Context(), store, g)
			if errors.Is(err, state.ErrNotFound) {
				return app, false, nil
			}
			if err != nil {
				return app, false, err
			}
			return app, app.ID == g.AppID && app.Slug == g.App && app.ProjectID == g.ProjectID && keyAllowsApp(key, app), nil
		}
		if _, err := credentialSource(r.Context(), store, g); err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return app, false, nil
			}
			return app, false, err
		}
	}
	return app, app.ID == g.AppID && app.Slug == g.App && app.ProjectID == g.ProjectID && keyAllowsApp(key, app), nil
}
