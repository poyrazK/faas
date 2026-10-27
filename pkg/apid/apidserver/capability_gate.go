package apidserver

import (
	"context"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
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
				if eventPublishRoutes[pattern] {
					api.WriteProblem(w, api.ErrEventsPaused(false))
					return
				}
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
			if eventPublishRoutes[r.Pattern] {
				api.WriteProblem(w, api.ErrEventsPaused(true))
				return
			}
			writeMissingRoute(w, r)
			return
		}
		next(w, r, acct)
	}
}

// eventPublishRoutes answer a switched-off events capability with 503
// events_paused rather than a missing route (ADR-20261027125625): a producer
// that reads 404 as permanent would drop the events it holds.
var eventPublishRoutes = map[string]bool{
	"POST /v1/events:publish":             true,
	"POST /v1/events:publish-batch":       true,
	"POST /v1/apps/{slug}/events:publish": true,
}

// eventsPausedProblem is the refusal for a publication path the events
// capability does not own (the platform-tenant route): nil while events is on
// for the app's account (ADR-20261027125625).
func (s *server) eventsPausedProblem(acct state.Account, app state.App) *api.Problem {
	if s.capabilityEnabledFor("events", acct, app) {
		return nil
	}
	return api.ErrEventsPaused(s.capGate.Released("POST /v1/events:publish"))
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

// logSearchReleasedForApp decides the log-search capability for work that
// arrives with only an app, such as vmmd's runtime lines
// (ADR-20261024222102). Without a gate every account is released; a failed
// account read decides off, so an unreadable account indexes nothing.
func (s *server) logSearchReleasedForApp(ctx context.Context, app state.App) bool {
	if s.capGate == nil {
		return true
	}
	acct, err := s.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		s.log.DebugContext(ctx, "log search release: account lookup", "app_id", app.ID, "err", err)
		return false
	}
	return s.capabilityEnabledFor(logSearchCapability, acct, app)
}

// datadogIntegrationProblem refuses creating a Datadog log drain or app
// webhook, or switching one to Datadog (ADR-20261024222102): 503 while the
// operator switch FAAS_DATADOG_INTEGRATION_ENABLED is off, 403
// capability_not_released while the datadog-integration capability is off
// for the subject. Existing Datadog destinations keep delivering, and edits
// that leave one Datadog pass.
func (s *server) datadogIntegrationProblem(subject platformflags.Subject, becomesDatadog bool) *api.Problem {
	switch {
	case !becomesDatadog:
		return nil
	case !s.datadogIntegrationEnabled:
		return api.NewProblem(http.StatusServiceUnavailable, "datadog_integration_unavailable",
			"Datadog integration unavailable", "the Datadog integration is not enabled for this deployment")
	case s.capGate != nil && !s.capGate.Enabled(datadogCapability, subject):
		return api.ErrCapabilityNotReleased("datadog destinations", s.capGate.Name(datadogCapability))
	}
	return nil
}

// logSearchCapability and datadogCapability are the catalog ids these gates
// decide (pkg/productcap/catalog.json).
const (
	logSearchCapability = "log-search"
	datadogCapability   = "datadog-integration"
)

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
