package apidserver

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/servicemap"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

// serviceMapHost adapts *server to servicemap.Host (ADR-1131), reading
// fields at call time so WithStatusCache and test swaps take effect.
type serviceMapHost struct{ s *server }

var _ servicemap.Host = serviceMapHost{}

func (h serviceMapHost) Apps(ctx context.Context, accountID string) ([]state.App, error) {
	return h.s.store.ListApps(ctx, accountID)
}

func (h serviceMapHost) PromQL() *promql.Client { return h.s.promqlClient }

func (h serviceMapHost) Logger() *slog.Logger { return h.s.log }

// WakeAheadEnabled reads the ADR-977 opt-in; a store without wake-ahead
// support reports every app as not opted in.
func (h serviceMapHost) WakeAheadEnabled(ctx context.Context, appID string) (bool, error) {
	store, ok := h.s.store.(state.ServiceWakeAheadStore)
	if !ok {
		return false, nil
	}
	return store.ServiceWakeAheadEnabled(ctx, appID)
}

// renderServiceMapDashboard serves GET /dashboard/service-map.
func (s *server) renderServiceMapDashboard(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	data, err := s.serviceMap.DashboardData(r, acct)
	if err != nil {
		renderProblem(w, s.log, err)
		return
	}
	page := dashboard.Page{Title: "Service map", Body: "service_map", Account: dashboardAccountView(acct, 0), Data: data}
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, s.log, err)
	}
}
