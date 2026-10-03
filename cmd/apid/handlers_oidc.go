// OIDC / keyless deploy auth handler (issue #270 / ADR-101).
//
// Wires POST /v1/auth/oidc/exchange — the path that mints a
// short-lived opaque bearer (fp_oidc_<48 hex>) in exchange for an
// IdP-issued JWT. The downstream auth chain (Authorization: Bearer
// … on every deploy route) is unchanged; this handler is the ONLY
// mint path.
//
// The body of the handler is pkg/oidc.Handler. The wrapper here
// adapts the (accountHandler, http.HandlerFunc) shape: the routes
// in cmd/apid/server.go use the (accountHandler) chain for the
// authenticated routes, but the OIDC route is anonymous — the
// caller has no session, no bearer. We mount via middleware.AuthLimit
// (the per-IP fail-counter from spec §11) directly, without the
// session-required wrappers. The limit is the sha-red
// apiAuthLimiter so an OIDC brute-force contributes to the same
// 10/min/IP envelope as the rest of the API surface.
//
// The handler is constructed once at boot in newServerWithDeps
// and stamped on the server.oidcHandler field. Unit tests can
// leave it nil and skip the route; the route mount is conditional
// on the field being non-nil.
package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/oidc"
	"github.com/onebox-faas/faas/pkg/state"
)

// oidcHandler is the wiring-side wrapper around pkg/oidc.Handler.
// The http.Handler implementation is the pkg/oidc handler's
// ServeHTTP (state.APIKey-projection), which is anonymous — the
// *server does not need a session or bearer to dispatch it. The
// type is local so the construction site in newServerWithDeps
// reads as a single-typed value.
type oidcHandler struct {
	// inner is the pkg/oidc.Handler. We embed the interface so
	// the wrapper satisfies http.Handler via a single-line
	// delegation. The pkg/oidc.Handler interface is sealed
	// (constructor only, no exported fields).
	inner *oidc.Handler
}

// ServeHTTP forwards to the inner handler. The http.ResponseWriter
// + *http.Request pair is identical to what pkg/oidc.Handler
// expects; no state.Account is stamped here because the OIDC
// exchange route is anonymous.
func (h *oidcHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.inner.ServeHTTP(w, r)
}

// buildOIDCHandler constructs the OIDC handler from the server's
// existing deps. Pulled out of newServerWithDeps so the constructor
// stays readable; the only knob is the JWKS fetch timeout (5s, the
// same handshake-ish number pkg/edgejwks.Cache uses for refresh).
//
// The handler deps are wired from the same store/audit/log the
// server already holds. The OIDC package's narrow interfaces
// (AccountLookup, AuditEmitter) are satisfied by the store + audit
// directly without an adapter — see the doc comments on the
// interface declarations in pkg/oidc.
//
// The OIDC verifier (pkg/oidc.edgeJWKSVerifier) is constructed
// against a fresh pkg/edgejwks.Cache. The cache is per-process
// and refreshed on every Verify call — the same posture as the
// edge-rules gateway (cmd/gatewayd-internal/edge_rules_jwks.go:42-58).
func (s *server) buildOIDCHandler() *oidcHandler {
	// 5s JWKS fetch timeout. Mirrors the edgejwks refresh posture
	// (pkg/edgejwks/cache.go); a CI runner deploy call must
	// resolve inside a few seconds, so a 5s JWKS fetch is the
	// upper bound we tolerate. Failed fetches fall back to the
	// cached key set; sustained failures surface as 503 from the
	// handler.
	h := oidc.NewHandler(oidc.HandlerDeps{
		Verifier: oidc.NewVerifier(s.log),
		Policies: storeAsOIDCTrustPolicyStore(s.store),
		Tokens:   storeAsTokenExchangeStore(s.store),
		Lookups:  storeAsAccountLookupAdapter(s.store),
		Audit:    auditorAsOIDCEmitter(s.audit),
		Log:      s.log,
		// nil Clock = time.Now. The handler stamps ExpiresAt
		// against the system clock; tests inject a swappable
		// clock via HandlerDeps.Clock.
	})
	return &oidcHandler{inner: h}
}

// The narrow interfaces below are pkg/oidc-side contracts. The
// adapters are project-local glue (mirrors the pattern in
// cmd/apid/auth_adapters.go for the Authenticator interface).
// CodeQL review may flag the indirection as unnecessary — the
// interfaces are pkg/oidc's wire-shape, not a defensive layer.
// Keeping the adapters here means the pkg/oidc package can be
// independently unit-tested by injecting fakes; the production
// code path is the same adapter.

// storeAsOIDCTrustPolicyStore wraps state.Store as
// oidc.OIDCTrustPolicyStore. The store's surface is exactly the
// four UpsertOIDCTrustPolicy / GetOIDCTrustPolicy /
// ListOIDCTrustPoliciesForAccount / (DeleteOIDCExchangedToken — no,
// that's the token store) methods.
func storeAsOIDCTrustPolicyStore(s state.Store) oidc.OIDCTrustPolicyStore {
	return oidcTrustPolicyStoreAdapter{store: s}
}

type oidcTrustPolicyStoreAdapter struct{ store state.Store }

func (a oidcTrustPolicyStoreAdapter) Upsert(ctx context.Context, p *oidc.OIDCTrustPolicy) (*oidc.OIDCTrustPolicy, error) {
	return a.store.UpsertOIDCTrustPolicy(ctx, p)
}

func (a oidcTrustPolicyStoreAdapter) Get(ctx context.Context, accountID, issuerURL string) (*oidc.OIDCTrustPolicy, error) {
	policy, err := a.store.GetOIDCTrustPolicy(ctx, accountID, issuerURL)
	if errors.Is(err, state.ErrNotFound) {
		// The pkg/state contract is "ErrNotFound" but the
		// pkg/oidc-side interface uses a distinct sentinel so the
		// exchange handler can tell "no policy" from "store
		// failure". Translate at the adapter boundary so the
		// handler's errors.Is(err, ErrTrustPolicyNotFound) check
		// works for both PgStore and MemStore.
		return nil, oidc.ErrTrustPolicyNotFound
	}
	return policy, err
}

func (a oidcTrustPolicyStoreAdapter) ListForAccount(ctx context.Context, accountID string) ([]*oidc.OIDCTrustPolicy, error) {
	return a.store.ListOIDCTrustPoliciesForAccount(ctx, accountID)
}

// storeAsTokenExchangeStore wraps state.Store as
// oidc.TokenExchangeStore. The store's surface is the three
// InsertOIDCExchangedToken / GetOIDCExchangedTokenByHash /
// DeleteOIDCExchangedToken methods.
func storeAsTokenExchangeStore(s state.Store) oidc.TokenExchangeStore {
	return tokenExchangeStoreAdapter{store: s}
}

type tokenExchangeStoreAdapter struct{ store state.Store }

func (a tokenExchangeStoreAdapter) Insert(ctx context.Context, t *oidc.ExchangedToken) (string, error) {
	id, err := a.store.InsertOIDCExchangedToken(ctx, t)
	if err != nil {
		return "", err
	}
	// Return the server-minted row id so the handler can echo it
	// in the response and use it as the audit correlation key.
	t.ID = id
	return id, nil
}

func (a tokenExchangeStoreAdapter) GetByHash(ctx context.Context, hash []byte) (*oidc.ExchangedToken, error) {
	tok, err := a.store.GetOIDCExchangedTokenByHash(ctx, hash)
	if errors.Is(err, state.ErrNotFound) {
		// Same sentinel translation as
		// oidcTrustPolicyStoreAdapter.Get — the pkg/state contract
		// is "ErrNotFound" but pkg/oidc wants ErrTokenNotFound so
		// the auth middleware can distinguish "key never existed"
		// from "long-lived key was revoked".
		return nil, oidc.ErrTokenNotFound
	}
	return tok, err
}

func (a tokenExchangeStoreAdapter) Delete(ctx context.Context, id string) error {
	return a.store.DeleteOIDCExchangedToken(ctx, id)
}

// storeAsAccountLookupAdapter wraps state.Store as the narrow
// pkg/oidc.AccountLookup interface. The pkg/oidc handler only
// needs AccountByOIDCSubject; the auth chain's other lookups
// (AccountByKeyHash, AccountByID) are not on the OIDC path.
func storeAsAccountLookupAdapter(s state.Store) oidc.AccountLookup {
	return accountLookupAdapter{store: s}
}

type accountLookupAdapter struct{ store state.Store }

func (a accountLookupAdapter) AccountByOIDCSubject(ctx context.Context, issuerURL, subject string) (state.Account, error) {
	return a.store.AccountByOIDCSubject(ctx, issuerURL, subject)
}

// AccountByOIDCRepositoryBinding satisfies oidc.RepositoryBindingLookup when
// the store can resolve GitHub repository bindings.
func (a accountLookupAdapter) AccountByOIDCRepositoryBinding(ctx context.Context, issuerURL, subject string) (state.Account, error) {
	resolver, ok := a.store.(state.OIDCRepositoryBindingResolver)
	if !ok {
		return state.Account{}, state.ErrNotFound
	}
	return resolver.AccountByOIDCRepositoryBinding(ctx, issuerURL, subject)
}

// auditorAsOIDCEmitter wraps *auditor as the narrow
// pkg/oidc.AuditEmitter interface. The auditor's Emit is the same
// signature (kind, accountID, data); the adapter is a single
// delegation.
func auditorAsOIDCEmitter(a *auditor) oidc.AuditEmitter {
	if a == nil {
		return nilAuditEmitter{}
	}
	return oidcEmitterAdapter{a: a}
}

type oidcEmitterAdapter struct{ a *auditor }

func (a oidcEmitterAdapter) Emit(ctx context.Context, kind string, accountID *string, data map[string]any) {
	a.a.Emit(ctx, kind, accountID, data)
}

// nilAuditEmitter is the no-op emitter when the server has no
// audit handle (older tests + dev mode that hasn't wired the
// auditor). Matches the same nil-pattern as the router-side
// helpers.
type nilAuditEmitter struct{}

func (nilAuditEmitter) Emit(_ context.Context, _ string, _ *string, _ map[string]any) {}
