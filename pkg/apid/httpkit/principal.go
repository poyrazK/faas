package httpkit

import (
	"context"
	"net/http"

	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// Principal is the authenticated caller. Key is nil when the caller
// authenticated via the dashboard session cookie (in which case the
// caller is implicitly treated as having the "admin" scope by
// requireScope). See ADR-034.
type Principal struct {
	Acct state.Account
	Key  *state.APIKey
}

// PrincipalFrom returns the principal stamped onto r by the auth middleware.
// Returns ok=false if the middleware did not run (e.g. tests that wire a
// handler directly to httptest). It reads pkg/auth/middleware's context key
// so cmd/apid's observeWrap and middleware.RequireScope see the same value
// (ADR-044).
func PrincipalFrom(r *http.Request) (Principal, bool) {
	acct, key, ok := authmw.AccountFromContext(r)
	if !ok {
		return Principal{}, false
	}
	return Principal{Acct: acct, Key: key}, true
}

// accountContextKey is the request-context key for the authenticated
// account (under sessionAuth). Avoids stringly-typed context values.
type accountContextKey struct{}

// WithAccount returns ctx with acct stashed under the sessionAuth key.
func WithAccount(ctx context.Context, acct state.Account) context.Context {
	return context.WithValue(ctx, accountContextKey{}, acct)
}

// AccountFrom extracts the session-authenticated account stored by
// WithAccount.
func AccountFrom(ctx context.Context) (state.Account, bool) {
	a, ok := ctx.Value(accountContextKey{}).(state.Account)
	return a, ok
}

// AppLoader resolves a slug to an app owned by acct, collapsing cross-account
// and missing rows to the same 404 (ADR-046). On failure it has already
// written the response and returns false. *middleware.Middleware satisfies
// it; domain packages take an AppLoader instead of reaching for *server.
type AppLoader interface {
	LoadApp(w http.ResponseWriter, r *http.Request, acct state.Account, slug string) (state.App, bool)
}

var _ AppLoader = (*authmw.Middleware)(nil)
