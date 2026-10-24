package apidserver

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/incidents"
	"github.com/onebox-faas/faas/pkg/state"
)

// incidentsHost adapts *server to incidents.Host (ADR-20261022193512),
// reading fields at call time so test store swaps take effect.
type incidentsHost struct{ s *server }

var _ incidents.Host = incidentsHost{}

func (h incidentsHost) LoadApp(w http.ResponseWriter, r *http.Request, acct state.Account, slug string) (state.App, bool) {
	return h.s.loadApp(w, r, acct, slug) //nolint:contextcheck // loadApp uses r.Context().
}

func (h incidentsHost) Store() incidents.Store { return h.s.store }

// Changes reads the ADR-751 change timeline under its own flag, decided for
// the app's organization.
func (h incidentsHost) Changes(ctx context.Context, acct state.Account, app state.App, since, until time.Time) (api.AppChangeTimelineResponse, bool) {
	if !h.s.changeTimelineFor(appFlagSubject(acct, app)) {
		return api.AppChangeTimelineResponse{}, false
	}
	return h.s.appChangeTimeline(ctx, acct, app, since, until, time.Now().UTC()), true
}

// Actor names the caller as release approvals record it.
func (h incidentsHost) Actor(r *http.Request, acct state.Account) string {
	return releaseApprovalActorLabel(r, acct)
}

func (h incidentsHost) Logger() *slog.Logger { return h.s.log }
