package mcpcatalog

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ReadinessOAuthCredential keeps the connection lease until the caller has
// recorded its probe. It never exposes a refresh token or sealed grant data.
type ReadinessOAuthCredential struct {
	Token string
	store state.MCPReadinessOAuthStore
	job   state.MCPCatalogObserverJob
}

func (c *ReadinessOAuthCredential) Release(ctx context.Context) error {
	return c.store.ReleaseMCPReadinessOAuth(ctx, c.job)
}
func (r *ObserverRunner) AcquireReadinessOAuth(ctx context.Context, account, app string, ref api.MCPReadinessOAuthConnection, endpoint string) (*ReadinessOAuthCredential, error) {
	owner, err := r.Store.AccountByID(ctx, account)
	if err != nil || !owner.Plan.CustomMetricsAllowed() {
		return nil, state.ErrMCPReadinessOAuthChanged
	}
	application, err := r.Store.AppByID(ctx, app)
	if err != nil || application.AccountID != account || !application.AcceptsRequestInvocations() || application.Status != state.AppActive || application.PublicAuthMode == state.AppPublicAuthModeInternalOnly {
		return nil, state.ErrMCPReadinessOAuthChanged
	}
	source, ok := r.Store.(state.MCPReadinessOAuthStore)
	if !ok {
		return nil, errors.New("OAuth connection store unavailable")
	}
	job, err := source.ClaimMCPReadinessOAuth(ctx, account, app, ref, endpoint)
	if err != nil {
		return nil, err
	}
	credential := &ReadinessOAuthCredential{store: source, job: job}
	token, err := r.oauthToken(ctx, job)
	if err != nil {
		// Keep renewal state intact, including saved responses and uncertain exchanges.
		release, stop := context.WithTimeout(context.WithoutCancel(ctx), api.MCPPolicyConvergenceReadTimeout)
		_ = credential.Release(release)
		stop()
		if errors.Is(err, state.ErrMCPCatalogMonitorChanged) {
			return nil, state.ErrMCPReadinessOAuthChanged
		}
		if errors.Is(err, state.ErrConflict) {
			return nil, state.ErrMCPReadinessOAuthBusy
		}
		if store, ok := r.Store.(state.MCPCatalogMonitorStore); ok {
			root, e := store.GetMCPCatalogMonitor(ctx, account, app)
			if e == nil {
				view, found := api.CatalogMonitorRoleSnapshot(root, ref.Role)
				if found && view.ObserverStatus != nil {
					st := view.ObserverStatus
					if st.OAuth != nil && st.OAuth.State == "reauthorization_required" || st.Renewal != nil && st.Renewal.State == "reauthorization_required" {
						return nil, state.ErrMCPReadinessOAuthReconnect
					}
				}
			}
		}
		return nil, errors.New("OAuth credential unavailable")
	}
	credential.Token = token
	return credential, nil
}
