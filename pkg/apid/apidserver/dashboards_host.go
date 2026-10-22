package apidserver

import (
	"context"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/apid/dashboards"
	"github.com/onebox-faas/faas/pkg/state"
)

// dashboardsHost adapts *server to dashboards.Host (ADR-20261022173833),
// reading fields at call time so test store swaps take effect.
type dashboardsHost struct{ s *server }

var _ dashboards.Host = dashboardsHost{}

// AppForAccount resolves an app the account owns; deleted apps and other
// accounts' apps read as absent.
func (h dashboardsHost) AppForAccount(ctx context.Context, acct state.Account, slug string) (state.App, bool) {
	if slug == "" {
		return state.App{}, false
	}
	app, err := h.s.store.AppBySlug(ctx, slug)
	if err != nil || app.AccountID != acct.ID {
		return state.App{}, false
	}
	return app, true
}

func (h dashboardsHost) Dashboards() state.DashboardStore { return h.s.store }

func (h dashboardsHost) Logger() *slog.Logger { return h.s.log }
