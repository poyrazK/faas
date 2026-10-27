package apidserver

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/queueleases"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// queueLeasesHost adapts *server to queueleases.Host (ADR-1140). It reads
// server fields at call time so tests that swap the store or verifier keep
// working.
type queueLeasesHost struct{ s *server }

var _ queueleases.Host = queueLeasesHost{}

func (h queueLeasesHost) Store() state.Store { return h.s.store }

// WorkloadVerifier is the cluster workload trust (ADR-1141), or the Flags
// trust on an apid that only configures that.
func (h queueLeasesHost) WorkloadVerifier() *workloadidentity.Verifier {
	if h.s.workloadVerifier != nil {
		return h.s.workloadVerifier
	}
	return h.s.flagsWorkloadVerifier
}

func (h queueLeasesHost) Subscribe(ctx context.Context, channels []string) (<-chan db.Notification, func(), error) {
	return h.s.notif.Subscribe(ctx, channels)
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
			// The API-key chain decides the capability flag for the account;
			// a workload token gets the same decision (ADR-20261023115741).
			if !s.capabilityAllowedFor(r, acct) {
				writeMissingRoute(w, r)
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
