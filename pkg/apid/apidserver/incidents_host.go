package apidserver

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/incidents"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// incidentsHost adapts *server to incidents.Host (ADR-20261022193512),
// reading fields at call time so test store swaps take effect.
type incidentsHost struct{ s *server }

var _ incidents.Host = incidentsHost{}

// TeamApps lists the apps of the caller's organizations that teammates
// created and the caller may read (ADR-20261109120000): their alert rules
// and incidents are held under the creating member's account, so the
// caller's own account never lists them. A failed read lists none.
func (h incidentsHost) TeamApps(ctx context.Context, acct state.Account) []state.App {
	lister, ok := h.s.store.(state.OrgAppLister)
	if !ok {
		return nil
	}
	actor := acct
	if principal, _, ok := middleware.AccountFromCtx(ctx); ok {
		actor = principal
	}
	orgs, err := h.s.store.ListOrgsForAccount(ctx, actor.ID)
	if err != nil {
		h.s.log.WarnContext(ctx, "incidents: list organizations", "err", err)
		return nil
	}
	var apps []state.App
	for _, org := range orgs {
		if org.Personal {
			continue
		}
		orgApps, err := lister.ListAppsByOrg(ctx, org.ID)
		if err != nil {
			h.s.log.WarnContext(ctx, "incidents: list organization apps", "org_id", org.ID, "err", err)
			continue
		}
		for _, app := range orgApps {
			if app.AccountID != acct.ID {
				apps = append(apps, app)
			}
		}
	}
	visible, err := accountViewApps(ctx, h.s.store, acct, apps)
	if err != nil {
		h.s.log.WarnContext(ctx, "incidents: organization app access", "err", err)
		return nil
	}
	return visible
}

func (h incidentsHost) LoadApp(w http.ResponseWriter, r *http.Request, acct state.Account, slug string) (state.App, bool) {
	return h.s.loadApp(w, r, acct, slug) //nolint:contextcheck // loadApp uses r.Context().
}

func (h incidentsHost) Store() incidents.Store { return h.s.store }

// AppVisible applies the account inventories' rule to an incident's app.
func (h incidentsHost) AppVisible(ctx context.Context, acct state.Account, app state.App) bool {
	return accountViewAppVisible(ctx, h.s.store, h.s.log, acct, app)
}

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
