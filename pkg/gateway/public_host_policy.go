// adr: 570
package gateway

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// PublicAppPolicySource is trusted resolver metadata, never a request header.
// Slug is used only for an edge-rule target; Host identifies ordinary routes.
type PublicAppPolicySource struct {
	Host, Slug, AppsSuffix, DeploySuffix, Revision string
	TenantSurfaces                                 bool
	CanSubstitute                                  bool // The lookup verified this host can use an edge route target.
}

// A negative claim permits a synthetic hostname only with CanSubstitute.
// Source carries only trusted resolver inputs and its content fingerprint.
type PublicRouteSourcePolicy struct {
	Source           *PublicAppPolicySource
	AppID, AccountID string
	Found            bool
}

type publicRouteSourcePolicyKey struct{}

func PublicRouteSourceClaim(ctx context.Context) *PublicRouteSourcePolicy {
	claim, found := ctx.Value(publicRouteSourcePolicyKey{}).(PublicRouteSourcePolicy)
	if !found {
		return nil
	}
	return &claim
}

func rememberPublicRouteSourcePolicy(r *http.Request, app App, found bool) {
	if app.PublicPolicySource == nil {
		return // Legacy in-process backends have no authoritative claim carrier.
	}
	source := *app.PublicPolicySource
	claim := PublicRouteSourcePolicy{Source: &source, AppID: app.ID, AccountID: app.AccountID, Found: found}
	*r = *r.WithContext(context.WithValue(r.Context(), publicRouteSourcePolicyKey{}, claim))
}

func (h *Handler) attachPublicRouteSourcePolicy(r *http.Request, target *App) bool {
	claim, found := r.Context().Value(publicRouteSourcePolicyKey{}).(PublicRouteSourcePolicy)
	if !found {
		if target.PublicPolicySource != nil {
			*r = *r.WithContext(context.WithValue(r.Context(), hostPolicyLookupFailureKey{}, errors.New("route source policy is unavailable")))
			return false
		}
		return true // Preserve fixtures without production host policy wiring.
	}
	target.PublicRouteSource = &claim
	return true
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
	defer measureTrafficPhase(r.Context(), trafficPolicy)()
	if failure, ok := r.Context().Value(hostPolicyLookupFailureKey{}).(error); ok {
		return App{}, false, failure
	}
	backend, ok := h.backend.(HostPolicyBackend)
	if !ok {
		app, found := h.backend.Lookup(r.Context(), host)
		return app, found, nil
	}
	// The first attempt keeps the host read bound. A failed one is retried
	// once within the cold bound: a host that misses the route cache reads
	// the store, and a cold backend or a short control-plane stall refused
	// a new app's first request (ADR-20261027085556 amendment 2). A store
	// answer, an unknown host included, is never retried.
	cold, cancel := context.WithTimeout(r.Context(), api.TrafficPublicHostColdReadTimeout)
	defer cancel()
	app, found, err := lookupHostPolicyAttempt(cold, backend, host, api.TrafficPublicHostReadTimeout)
	if err != nil && cold.Err() == nil {
		app, found, err = lookupHostPolicyAttempt(cold, backend, host, api.TrafficPublicHostColdReadTimeout)
	}
	return app, found && err == nil, err
}

func lookupHostPolicyAttempt(ctx context.Context, backend HostPolicyBackend, host string, bound time.Duration) (App, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	app, found, err := backend.LookupHostPolicy(ctx, host)
	if err == nil {
		err = ctx.Err()
	}
	return app, found, err
}
