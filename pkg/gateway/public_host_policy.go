// adr: 375
package gateway

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// PublicAppPolicySource is trusted resolver metadata, never a request header.
// Slug is used only for an edge-rule target; Host identifies ordinary routes.
type PublicAppPolicySource struct {
	Host, Slug, AppsSuffix, DeploySuffix, Revision string
	TenantSurfaces                                 bool
}

type FreshHostPolicyRouter interface {
	RequiresFreshHostPolicy() bool
}

type HostPolicyBackend interface {
	LookupHostPolicy(context.Context, string) (App, bool, error)
}

// Policy reads can distinguish an authoritative miss from an unavailable
// store. A source-host failure also refuses route substitution.
type hostPolicyLookupFailureKey struct{}

func (h *Handler) lookupAppPolicy(r *http.Request, host string) (App, bool, error) {
	if failure, ok := r.Context().Value(hostPolicyLookupFailureKey{}).(error); ok {
		return App{}, false, failure
	}
	backend, ok := h.backend.(HostPolicyBackend)
	if !ok {
		app, found := h.backend.Lookup(r.Context(), host)
		return app, found, nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.TrafficPublicHostReadTimeout)
	defer cancel()
	app, found, err := backend.LookupHostPolicy(ctx, host)
	if err == nil {
		err = ctx.Err()
	}
	return app, found && err == nil, err
}
