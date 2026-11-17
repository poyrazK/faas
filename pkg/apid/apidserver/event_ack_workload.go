package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/queueleases"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// eventAckRoute authenticates a deferred event acknowledgment call
// (ADR-20261110200000). A JWT-shaped bearer is a gregale:events workload
// token from a live instance of the app named in the path, so a worker needs
// no API key; every other credential takes the ordinary API-key chain with
// scopes. The delivery token stays the fence on each invocation either way.
func (s *server) eventAckRoute(scopes []string, next func(http.ResponseWriter, *http.Request, state.Account)) http.HandlerFunc {
	keyed := s.authLimited(s.requireMFA(s.requireScope(scopes...)(next)))
	workload := middleware.AuthLimitWithLimiter(middleware.AuthLimitConfig{Log: s.log}, s.apiAuthLimiter)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			// Event deliveries reach stage deployments too, so any live
			// instance of the app may settle its own deliveries.
			acct, _, _, err := s.queueLeases.WorkloadScopeFor(r, workloadidentity.EventsAudience, false)
			if err != nil {
				api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Workload identity required",
					"a live workload token for gregale:events from an instance of this app is required"))
				return
			}
			if !s.capabilityAllowedFor(r, acct) {
				writeMissingRoute(w, r)
				return
			}
			next(w, r, acct)
		}))
	return func(w http.ResponseWriter, r *http.Request) {
		if queueleases.WorkloadBearer(r) {
			workload.ServeHTTP(w, r)
			return
		}
		keyed(w, r)
	}
}
