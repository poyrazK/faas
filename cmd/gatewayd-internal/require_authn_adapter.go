// require_authn_adapter bridges pkg/auth.Middleware.Authenticator
// to the narrow pkg/gateway.RequireAuthnAuthenticator interface
// the per-deployment authz branch consumes (issue #560).
//
// Why a bridge instead of passing pkg/auth's Authenticator
// directly: pkg/gateway deliberately has zero imports on
// pkg/auth or pkg/state (the gateway package is consumed by
// every daemon that fronts the edge, including the in-memory
// fake backends in tests; pulling state.APIKey's full struct
// into the handler would couple the hot path to sqlc). The
// adapter is the single seam that converts:
//
//   - state.Account → gateway.RequireAuthnAccount (ID only)
//   - state.APIKey  → gateway.RequireAuthnKey  (ID only)
//   - state.ErrAPIKeyExpired / ErrAPIKeyRevoked →
//     gateway.ErrAPIKeyExpired / gateway.ErrAPIKeyRevoked,
//     which are the sentinels pkg/gateway/handler.go
//     compares with errors.Is.
//
// The sentinels are produced by value here so errors.Is in
// the handler matches; a drift in either sentinel's string
// value surfaces as a 401 audit-row misclassification
// (state sends "expired", adapter maps to gateway's
// "api_key_expired" sentinel, handler emits reason="expired"
// in the audit row — the chain depends on every link).

package main

import (
	"context"
	"errors"
	"fmt"

	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// requireAuthnAdapter wraps an *authmw.Middleware so its
// Authenticator field (the wide pkg/auth interface that
// returns state.Account / state.APIKey) satisfies the
// narrow pkg/gateway.RequireAuthnAuthenticator interface
// (which returns gateway.RequireAuthnAccount /
// gateway.RequireAuthnKey). The conversion is allocation-
// cheap — it copies two string IDs per call.
type requireAuthnAdapter struct {
	authn authmw.Authenticator
	// limits loads a key's project limit; nil leaves keys unlimited.
	limits authmw.KeyProjectLimiter
}

// newRequireAuthnAdapter wraps the production middleware.
// nil middleware → nil adapter; pkg/gateway's authz branch
// is nil-safe so a nil return means "authz branch disabled"
// (the pre-issue behaviour, preserved for unit tests + dev
// boxes).
func newRequireAuthnAdapter(mw *authmw.Middleware, limits authmw.KeyProjectLimiter) *requireAuthnAdapter {
	if mw == nil {
		return nil
	}
	return &requireAuthnAdapter{authn: mw.Authn, limits: limits}
}

// AuthenticateKey implements gateway.RequireAuthnAuthenticator.
// The conversion is allocation-cheap: it copies two string IDs
// from state.Account / state.APIKey into the gateway-local
// mirror types. The store call + PG context are unchanged —
// the hot path still hits the same api_keys-by-hash row.
//
// Error translation: state.ErrAPIKeyExpired /
// state.ErrAPIKeyRevoked become gateway.ErrAPIKeyExpired /
// gateway.ErrAPIKeyRevoked. The conversion is via errors.Is
// on the incoming error (not a type switch) so a wrapped
// error (fmt.Errorf with %w, or errors.Join) still matches —
// pkg/state sometimes wraps.
func (a *requireAuthnAdapter) AuthenticateKey(ctx context.Context, hash []byte) (gateway.RequireAuthnAccount, gateway.RequireAuthnKey, error) {
	acct, key, err := a.authn.AuthenticateKey(ctx, hash)
	if err != nil {
		switch {
		case errors.Is(err, state.ErrAPIKeyExpired):
			return gateway.RequireAuthnAccount{}, gateway.RequireAuthnKey{}, gateway.ErrAPIKeyExpired
		case errors.Is(err, state.ErrAPIKeyRevoked):
			return gateway.RequireAuthnAccount{}, gateway.RequireAuthnKey{}, gateway.ErrAPIKeyRevoked
		case errors.Is(err, state.ErrNotFound):
			// A definite miss the gateway's key cache may remember.
			return gateway.RequireAuthnAccount{}, gateway.RequireAuthnKey{}, gateway.ErrAPIKeyUnknown
		}
		return gateway.RequireAuthnAccount{}, gateway.RequireAuthnKey{}, err
	}
	out := gateway.RequireAuthnKey{ID: key.ID, Scopes: append([]string(nil), key.Scopes...), OrgID: key.OrgID, AppID: key.AppID}
	// ADR-20261104085712: an agent or mcp-login key limited to projects
	// unlocks only their apps here too. The limit is read for those keys
	// only; the gateway's key cache keeps the answer with the key.
	if a.limits != nil && authmw.ProjectLimitable(key) {
		ids, limited, err := a.limits.APIKeyProjectLimit(ctx, key.ID)
		if err != nil {
			return gateway.RequireAuthnAccount{}, gateway.RequireAuthnKey{}, fmt.Errorf("gatewayd: read key project limit: %w", err)
		}
		out.ProjectLimited, out.ProjectIDs = limited, ids
	}
	return gateway.RequireAuthnAccount{ID: acct.ID}, out, nil
}

// Compile-time check: requireAuthnAdapter satisfies the narrow
// gateway.RequireAuthnAuthenticator interface. A drift in
// either side's signatures fails to compile here, surfacing
// before tests.
var _ gateway.RequireAuthnAuthenticator = (*requireAuthnAdapter)(nil)
