// auth_adapters.go — adapter facades for the pkg/auth.Middleware
// (ADR-046). The pkg/auth interface surfaces (`middleware.Authenticator`,
// `middleware.SessionLookup`, `middleware.Auditor`) are defined over
// the *minimal* method set the middleware needs; the concrete
// `*state.PgStore` lives in cmd/gatewayd and provides every method
// via duck-typing.
//
// cmd/apid has the same shapes in `cmd/apid/auth_adapters.go`. They
// are not lifted into pkg/auth because the adapters are pkg/state
// callers' project-local glue — pkg/auth should not own a hard
// dependency on pkg/state (the audit adapter lifts pkg/audit
// instead, which itself depends on pkg/state).
package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/audit"
	middleware "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// storeAsAuthenticator returns middleware.Authenticator as a view
// over a *state.PgStore. All four methods (AuthenticateKey,
// AccountByID, AppBySlug, TouchKeyLastUsed) are already on the
// pkg/state.Store interface — the adapter exists only because Go
// doesn't auto-cast a larger interface to a smaller one.
func storeAsAuthenticator(s state.Store) middleware.Authenticator {
	return authAdapter{store: s}
}

type authAdapter struct{ store state.Store }

func (a authAdapter) AuthenticateKey(ctx context.Context, hash []byte) (state.Account, state.APIKey, error) {
	return a.store.AuthenticateKey(ctx, hash)
}

func (a authAdapter) AccountByID(ctx context.Context, id string) (state.Account, error) {
	return a.store.AccountByID(ctx, id)
}

func (a authAdapter) AppBySlug(ctx context.Context, slug string) (state.App, error) {
	return a.store.AppBySlug(ctx, slug)
}

func (a authAdapter) TouchKeyLastUsed(ctx context.Context, keyID string) error {
	return a.store.TouchKeyLastUsed(ctx, keyID)
}

// storeAsSessionLookup returns middleware.SessionLookup as a view
// over a *state.PgStore. The IAM-3 cross-check (issue #165 ADR-039)
// requires the live-session-row read after the AEAD envelope
// verifies; the audit-iam-3 entry in the pkg/audit log shows the
// rationale. GetSession + TouchSessionLastSeen are both on the
// pkg/state.Store interface.
func storeAsSessionLookup(s state.Store) middleware.SessionLookup {
	return sessionLookupAdapter{store: s}
}

type sessionLookupAdapter struct{ store state.Store }

func (l sessionLookupAdapter) GetSession(ctx context.Context, sid string) (state.Session, error) {
	return l.store.GetSession(ctx, sid)
}

func (l sessionLookupAdapter) TouchSessionLastSeen(ctx context.Context, sid string) error {
	return l.store.TouchSessionLastSeen(ctx, sid)
}

// auditorAsAuthAuditor returns middleware.Auditor as a view over
// a *pkg/audit.Auditor. The middleware.Auditor interface is the
// minimal one-method subset (`Emit`); the audit package's *Auditor
// satisfies it directly. The adapter is just a named function so
// the wiring site reads as obvious.
func auditorAsAuthAuditor(a *audit.Auditor) middleware.Auditor {
	return auditAdapter{a: a}
}

type auditAdapter struct{ a *audit.Auditor }

func (a auditAdapter) Emit(ctx context.Context, kind string, accountID *string, data map[string]any) {
	a.a.Emit(ctx, kind, accountID, data)
}
