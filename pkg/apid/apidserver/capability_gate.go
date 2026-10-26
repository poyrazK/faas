package apidserver

import (
	"context"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/apid/capgate"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/productcap"
	"github.com/onebox-faas/faas/pkg/state"
)

// newCapabilityGate builds the route gate from the product catalog and this
// server's platform-flag snapshot (ADR-20261023115741).
func (s *server) newCapabilityGate() (*capgate.Gate, error) {
	catalog, err := productcap.Load()
	if err != nil {
		return nil, err
	}
	return capgate.New(catalog, func() *platformflags.Snapshot { return s.platformFlags().Snapshot() }), nil
}

// WithCapabilityGate installs the capability route gate. Production apid
// always installs one; a nil gate releases every route.
func (s *server) WithCapabilityGate(gate *capgate.Gate) *server {
	s.capGate = gate
	return s
}

// capabilityKillSwitch hides a matched route whose capability is switched off
// platform-wide, before authentication, exactly as an unknown route answers.
func (s *server) capabilityKillSwitch(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.capGate != nil {
			if _, pattern := mux.Handler(r); pattern != "" && !s.capGate.Released(capabilityGateKey(pattern, r)) {
				writeMissingRoute(w, r)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// capabilityPathHidden reports a request whose path belongs to a capability
// switched off platform-wide. apiContractHandler uses it so a hidden route
// answers a method mismatch with 404, not a revealing 405.
func (s *server) capabilityPathHidden(r *http.Request) bool {
	if s.capGate == nil {
		return false
	}
	_, ok := s.capGate.Capability(r.URL.Path)
	return ok && !s.capGate.Released(r.URL.Path)
}

// capabilityAllowed hides an authenticated route whose capability flag is
// off for the caller. It runs before app and organization resources are
// resolved, so a hidden route never reveals whether the resource exists.
func (s *server) capabilityAllowed(next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		if !s.capabilityAllowedFor(r, acct) {
			writeMissingRoute(w, r)
			return
		}
		next(w, r, acct)
	}
}

func (s *server) capabilityAllowedFor(r *http.Request, acct state.Account) bool {
	if s.capGate == nil || r.Pattern == "" {
		return true
	}
	return s.capGate.Allowed(capabilityGateKey(r.Pattern, r), flagSubject(r, acct))
}

// capabilityGateKey is what the gate decides a request by: the matched
// pattern for a specific route, the request path for a subtree pattern such
// as "GET /dashboard/", whose handler dispatches many pages by path.
func capabilityGateKey(pattern string, r *http.Request) string {
	if strings.HasSuffix(pattern, "/") {
		return r.URL.Path
	}
	return pattern
}

// writeMissingRoute answers as a route that does not exist: the API problem
// for /v1, ServeMux's plain 404 elsewhere.
func writeMissingRoute(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/v1/") {
		httpkit.NoSuchRoute(w)
		return
	}
	http.NotFound(w, r)
}

// capabilityEnabledFor decides a capability for work on a loaded app
// (ADR-20261023115741). Without a gate (unit tests) every capability is on.
func (s *server) capabilityEnabledFor(capability string, acct state.Account, app state.App) bool {
	return s.capGate == nil || s.capGate.Enabled(capability, appFlagSubject(acct, app))
}

// realtimeReleased decides the realtime capability for a realtime endpoint's
// account and app (ADR-20261023115741). A failed lookup keeps the endpoint
// registered, so a transient store error never disconnects healthy sockets.
func (s *server) realtimeReleased(ctx context.Context, accountID, appID string) bool {
	if s.capGate == nil {
		return true
	}
	acct, err := s.store.AccountByID(ctx, accountID)
	if err != nil {
		return true
	}
	app, err := s.store.AppByID(ctx, appID)
	if err != nil {
		return true
	}
	return s.capGate.Enabled("realtime", appFlagSubject(acct, app))
}
