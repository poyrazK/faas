package apidserver

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/routepeaks"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// getAppRouteResources serves which routes an app's memory and CPU peaks
// come from (ADR-20261121170000); the logic is pkg/apid/routepeaks.
func (s *server) getAppRouteResources(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.platformFlagFor(platformflags.RouteResourcePeaks, routepeaks.FlagSubject(acct.ID, string(acct.Plan))) {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Route resource peaks unavailable", "route resource peaks are not enabled for this account").
			WithHint("An operator releases route resource peaks account by account. Your credentials are not the cause."))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.RouteResourceStore)
	appID, err := uuid.Parse(app.ID)
	if !ok || err != nil {
		api.WriteProblem(w, api.ErrCapacity("route resource peaks"))
		return
	}
	window, err := routepeaks.Window(r.URL.Query().Get("since"))
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	until := time.Now().UTC().Truncate(time.Minute).Add(time.Minute)
	resp, err := routepeaks.Read(r.Context(), store, appID, until.Add(-window), until)
	if err != nil {
		api.WriteProblem(w, routeAnalyticsProblem(err, "route resource peaks"))
		return
	}
	resp.Slug = app.Slug
	resp.Split = routepeaks.SplitFor(r.Context(), s.store, app, resp.Report)
	writeJSON(w, http.StatusOK, resp)
}
