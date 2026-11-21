package apidserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/routeresources"
	"github.com/onebox-faas/faas/pkg/state"
)

// routeResourcesReadTimeout bounds the two reads one request makes.
const routeResourcesReadTimeout = 5 * time.Second

// getAppRouteResources serves which routes an app's memory and CPU peaks
// come from (ADR-20261121170000).
func (s *server) getAppRouteResources(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.platformFlagFor(platformflags.RouteResourcePeaks, flagSubject(r, acct)) {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Route resource peaks unavailable", "route resource peaks are not enabled for this account").
			WithHint("An operator releases route resource peaks account by account. Your credentials are not the cause."))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.RouteResourceStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("route resource peaks"))
		return
	}
	window, err := routeResourcesWindow(r.URL.Query().Get("since"))
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	appID, err := uuid.Parse(app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("app id"))
		return
	}
	until := time.Now().UTC().Truncate(time.Minute).Add(time.Minute)
	from := until.Add(-window)
	resp, err := readRouteResources(r.Context(), store, appID, from, until)
	if err != nil {
		api.WriteProblem(w, routeAnalyticsProblem(err, "route resource peaks"))
		return
	}
	resp.Slug = app.Slug
	writeJSON(w, http.StatusOK, resp)
}

func routeResourcesWindow(since string) (time.Duration, error) {
	if since == "" {
		return api.RouteResourceWindowDefault, nil
	}
	d, err := time.ParseDuration(since)
	if err != nil || d < time.Minute {
		return 0, errors.New("since must be a duration of at least 1m, such as 24h")
	}
	if d > api.RouteResourceWindowMax {
		return 0, errors.New("since must be at most 336h (14 days)")
	}
	return d, nil
}

func readRouteResources(ctx context.Context, store state.RouteResourceStore, appID uuid.UUID, from, until time.Time) (api.RouteResourcesResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, routeResourcesReadTimeout)
	defer cancel()
	resources, truncated, err := store.InstanceResourceMinutes(ctx, appID, from, until, api.RouteResourceReadRowsMax)
	if err != nil {
		return api.RouteResourcesResponse{}, err
	}
	if truncated && len(resources) > 0 {
		// Rows are newest first: analyse only the minutes both reads cover.
		from = resources[len(resources)-1].Minute
	}
	// A minute holds up to RouteResourceRoutesPerInstanceMinuteMax route rows.
	routes, routesTruncated, err := store.InstanceRouteMinutes(ctx, appID, from, until, api.RouteResourceReadRowsMax*4)
	if err != nil {
		return api.RouteResourcesResponse{}, err
	}
	return api.RouteResourcesResponse{
		From: from.Format(time.RFC3339), Until: until.Format(time.RFC3339),
		Truncated: truncated || routesTruncated,
		Report:    routeresources.Analyze(resources, routes, routeresources.Options{}),
	}, nil
}
