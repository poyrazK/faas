// Adapters that bridge cmd/apid's concrete types (state.Store,
// *auditor) to the pkg/auth interfaces. Lives alongside
// auth_facade.go so the bridge is one place a future reader can
// audit. ADR-044.
//
// state.Store already has every method pkg/auth.Authenticator
// needs (AuthenticateKey, AccountByID, AppBySlug, TouchKeyLastUsed)
// with the right signatures, so the adapter is a free pass-through
// — no wrapping or translation. We expose it as a named function
// so the call site is searchable ("where do we declare the auth
// interface conformance?") instead of an inline cast.
package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

// storeAsAuthenticator returns middleware.Authenticator as a view over a
// state.Store. The view is the same pointer; no copy.
func storeAsAuthenticator(s state.Store) middleware.Authenticator {
	return storeAuthAdapter{s}
}

// storeAuthAdapter is the unexported type that satisfies
// middleware.Authenticator by delegating to a state.Store. Defined here
// (not via a method set on state.Store itself) so pkg/auth doesn't
// force state.Store to know about the auth interface.
type storeAuthAdapter struct{ state.Store }

// The deploy-token methods live on the concrete stores, not on the
// state.Store interface, so the embedded interface does not promote them.
// Without these forwards the middleware's DeployTokenAuthenticator
// assertion fails and every deploy token is rejected with a bare 401.
var _ middleware.DeployTokenAuthenticator = storeAuthAdapter{}

type deployTokenStore interface {
	middleware.DeployTokenAuthenticator
	TouchDeployTokenLastUsed(ctx context.Context, tokenID string) error
}

func (a storeAuthAdapter) AuthenticateDeployToken(ctx context.Context, hash []byte) (state.Account, state.APIKey, error) {
	d, ok := a.Store.(deployTokenStore)
	if !ok {
		return state.Account{}, state.APIKey{}, state.ErrNotFound
	}
	return d.AuthenticateDeployToken(ctx, hash)
}

func (a storeAuthAdapter) TouchDeployTokenLastUsed(ctx context.Context, tokenID string) error {
	d, ok := a.Store.(deployTokenStore)
	if !ok {
		return nil
	}
	return d.TouchDeployTokenLastUsed(ctx, tokenID)
}

// storeAsSessionLookup returns middleware.SessionLookup as a view over a
// state.Store. The cookie-branch of pkg/auth.RequireSession uses
// this to cross-check the AEAD-bound envelope against the live
// sessions row (IAM-3 / ADR-039 replay defense) and to stamp
// last_seen_at via TouchSessionLastSeen. Both methods already
// exist on state.Store (pkg/state/{pgstore,memstore}.go); the
// adapter is a free pass-through.
func storeAsSessionLookup(s state.Store) middleware.SessionLookup {
	return storeAuthAdapter{s}
}

// auditorAsAuthAuditor returns middleware.Auditor as a view over an
// *auditor. The auditor type today (cmd/apid/handlers_audit.go)
// already has an Emit method with the right signature; this
// adapter is also a free pass-through.
func auditorAsAuthAuditor(a *auditor) middleware.Auditor {
	return auditorAuthAdapter{a}
}

type auditorAuthAdapter struct{ *auditor }

// auditorAsAuthzAuditor returns authz.AuditEmitter as a view over
// an *auditor. Issue #190 / IAM-6 / ADR-061 PR 4 — LoadOrg
// (cmd/apid/auth_facade.go::loadOrg) emits authz.denied rows when
// the membership lookup fails. The adapter is a free pass-through
// because *auditor.Emit and authz.AuditEmitter.Emit share the same
// (ctx, event, *string, map[string]any) signature.
func auditorAsAuthzAuditor(a *auditor) authz.AuditEmitter {
	return auditorAuthzAdapter{a}
}

type auditorAuthzAdapter struct{ *auditor }

// Compile-time assertions: the adapters must satisfy the exported
// pkg/auth + pkg/authz interfaces. A future method added to either
// interface surfaces as a compile error here.
var (
	_ middleware.Authenticator = storeAuthAdapter{}
	_ middleware.SessionLookup = storeAuthAdapter{}
	_ middleware.Auditor       = auditorAuthAdapter{}
	_ authz.AuditEmitter       = auditorAuthzAdapter{}
)
