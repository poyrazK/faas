package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/queueleases"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// queueLeasesHost adapts *server to queueleases.Host (ADR-1152). It reads
// server fields at call time so tests that swap the store or verifier keep
// working.
type queueLeasesHost struct{ s *server }

var _ queueleases.Host = queueLeasesHost{}

func (h queueLeasesHost) Store() state.Store { return h.s.store }

func (h queueLeasesHost) WorkloadVerifier() *workloadidentity.Verifier {
	return h.s.flagsWorkloadVerifier
}

// queueLeaseRoute authenticates a lease call. A JWT-shaped bearer is a
// gregale:queues workload token from a live instance of the app; every other
// credential takes the ordinary API-key chain with queues:consume.
func (s *server) queueLeaseRoute(next queueleases.Handler) http.HandlerFunc {
	keyed := s.authLimited(s.requireMFA(s.requireScope(api.ScopesQueuesConsumeSurface...)(func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		if app, ok := s.loadApp(w, r, acct, r.PathValue("slug")); ok {
			next(w, r, acct, app, "")
		}
	})))
	workload := middleware.AuthLimitWithLimiter(middleware.AuthLimitConfig{Log: s.log}, s.apiAuthLimiter)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			acct, app, instanceID, err := s.queueLeases.WorkloadScope(r)
			if err != nil {
				queueleases.WriteWorkloadUnauthorized(w)
				return
			}
			next(w, r, acct, app, instanceID)
		}))
	return func(w http.ResponseWriter, r *http.Request) {
		if !queueBindingProductionRequest(w, r) {
			return
		}
		if queueleases.WorkloadBearer(r) {
			workload.ServeHTTP(w, r)
			return
		}
		keyed(w, r)
	}
}
