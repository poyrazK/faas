package mcpcatalog

import (
	"context"
	"net/http"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

// NewOAuthRunner shares the guarded provider transport across API and dashboard.
func NewOAuthRunner(store state.Store, identities func(context.Context) []*age.X25519Identity) (*ObserverRunner, func()) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = oci.EgressDialContext(nil)
	return &ObserverRunner{Store: store, Identities: identities, Client: &http.Client{Transport: transport}}, transport.CloseIdleConnections
}

// BrowserOAuth accepts only the console's trusted callback and authenticated
// session. HTTP callers cannot widen the CLI's loopback redirect contract.
func (r *ObserverRunner) BrowserOAuth(ctx context.Context, account, app string, req api.MCPCatalogOAuthRequest, redirect, session string, providerConfirmed bool) (api.MCPCatalogOAuthResponse, error) {
	var out api.MCPCatalogOAuthResponse
	if session == "" || !api.ValidMCPOAuthBrowserRedirect(redirect) || !api.ValidCatalogMonitorRole(req.Role) || (req.Action != "begin" && req.Action != "complete") {
		return out, state.ErrInvalidArgument
	}
	source, ok := r.Store.(state.MCPCatalogMonitorStore)
	if !ok {
		return out, state.ErrInvalidArgument
	}
	root, err := source.GetMCPCatalogMonitor(ctx, account, app)
	if err != nil {
		return out, err
	}
	view, ok := api.CatalogMonitorRoleSnapshot(root, req.Role)
	if !ok || view.Config.Revision != req.Revision {
		return out, state.ErrMCPCatalogMonitorChanged
	}
	if view.Config.Observer == nil || view.Config.Observer.OAuth == nil || !view.Config.Observer.OAuth.Guided || view.Config.Observer.OAuth.BrowserRedirectURI != redirect {
		return out, state.ErrInvalidArgument
	}
	if req.Action == "complete" && view.ObserverStatus != nil && view.ObserverStatus.OAuthFlow != nil && !view.ObserverStatus.OAuthFlow.ExpiresAt.After(time.Now()) {
		return api.MCPCatalogOAuthResponse{State: "authorization_expired"}, state.ErrMCPCatalogMonitorChanged
	}
	if req.Action == "begin" {
		status := view.ObserverStatus
		if status != nil {
			if status.LeaseUntil != nil && status.LeaseUntil.After(time.Now()) || status.OAuthFlow != nil && status.OAuthFlow.Consumed && status.OAuthFlow.ExpiresAt.After(time.Now()) {
				return out, state.ErrConflict
			}
			if api.MCPOAuthReauthorizationNeedsConfirmation(view) && !providerConfirmed {
				return out, state.ErrConflict
			}
		}
		// Lost exchanges have their own durable grant ledger. Resolve these records
		// at the provider and acknowledge them before issuing another authorization.
		ledger, ok := r.Store.(state.MCPOAuthRetirementStore)
		if !ok {
			return out, state.ErrInvalidArgument
		}
		for offset := 0; ; {
			records, e := ledger.ListMCPOAuthRetirements(ctx, account, offset)
			if e != nil {
				return out, e
			}
			for _, record := range records.Items {
				if record.AppID != app || record.Role != api.CatalogMonitorRole(req.Role) {
					continue
				}
				if record.State == "expired" && record.Reason == "issuance_unknown" {
					if !providerConfirmed {
						return out, state.ErrConflict
					}
					continue
				}
				if record.State == "awaiting_issuance" || record.Reason == "issuance_unknown" && record.State != "manually_acknowledged" && record.State != "provider_acknowledged" {
					return out, state.ErrConflict
				}
			}
			if records.NextOffset == 0 {
				break
			}
			if records.NextOffset <= offset || records.NextOffset > api.MCPOAuthRetirementMaxOffset {
				return out, state.ErrConflict
			}
			offset = records.NextOffset
		}
	}
	return r.connectOAuthBound(ctx, account, app, req, redirect, session)
}
