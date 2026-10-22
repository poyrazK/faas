// Package incidents is apid's unified incident list (ADR-20261022193512): a
// read-time projection of alert episodes, route monitor incidents and
// platform status incidents into one shape. It lives outside apidserver per
// ADR-982; apidserver only wires the routes and implements Host.
package incidents

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/publicstatus"
	"github.com/onebox-faas/faas/pkg/state"
)

// Store is the slice of state.Store the projection reads.
type Store interface {
	state.IncidentSourceStore
	ListPublicStatusEvents(ctx context.Context, options state.StatusEventListOptions) ([]state.StatusIncident, error)
	StatusEventByPublicID(ctx context.Context, publicID string) (state.StatusIncident, error)
	ListAlertDeliveriesForRule(ctx context.Context, ruleID string, limit int, includeTest bool) ([]state.AlertDelivery, error)
	AppByID(ctx context.Context, id string) (state.App, error)
}

// Host is what incidents need from apid.
type Host interface {
	// LoadApp resolves the account's app by slug and writes the problem
	// itself when it cannot.
	LoadApp(w http.ResponseWriter, r *http.Request, acct state.Account, slug string) (state.App, bool)
	Store() Store
	// Changes returns the app's change timeline (ADR-751) for the window,
	// or false while the change timeline is off for the account.
	Changes(ctx context.Context, acct state.Account, app state.App, since, until time.Time) (api.AppChangeTimelineResponse, bool)
	Logger() *slog.Logger
}

// Service serves the incidents API. Enabled is the FAAS_INCIDENTS_ENABLED
// gate; while it is off every route answers 503.
type Service struct {
	Host    Host
	Enabled bool
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Id prefixes; each names the store a detail read goes to.
const (
	alertPrefix    = "alert-"
	routePrefix    = "route-"
	platformPrefix = "platform-"
)

// sourceRows reads one row past the cap from each source, so a single
// source holding more than the cap still marks the merged list truncated.
const sourceRows = api.IncidentsMaxEntries + 1

// List serves GET /v1/incidents.
func (s *Service) List(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.gate(w) {
		return
	}
	now := s.now()
	q := r.URL.Query()
	since, prob := listSince(q.Get("since"), now)
	if prob == nil && q.Get("status") != "" && q.Get("status") != api.IncidentStatusOpen && q.Get("status") != "all" {
		prob = api.ErrIncidentsInvalid("status must be open or all")
	}
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	var app state.App
	if slug := q.Get("app"); slug != "" {
		var ok bool
		if app, ok = s.Host.LoadApp(w, r, acct, slug); !ok {
			return
		}
	}
	resp := s.list(r.Context(), acct, app, since)
	if q.Get("status") == api.IncidentStatusOpen {
		open := resp.Incidents[:0]
		for _, inc := range resp.Incidents {
			if inc.Status == api.IncidentStatusOpen {
				open = append(open, inc)
			}
		}
		resp.Incidents = open
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusOK, resp)
}

// listSince parses ?since= (RFC 3339): default IncidentsDefaultWindow ago,
// at most IncidentsMaxWindow ago.
func listSince(raw string, now time.Time) (time.Time, *api.Problem) {
	if raw == "" {
		return now.Add(-api.IncidentsDefaultWindow), nil
	}
	since, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, api.ErrIncidentsInvalid("since must be RFC 3339 (e.g. 2026-10-22T12:00:00Z)")
	}
	if now.Sub(since) > api.IncidentsMaxWindow {
		return time.Time{}, api.ErrIncidentsInvalid("since must be within the last " + api.IncidentsMaxWindow.String())
	}
	return since.UTC(), nil
}

type source struct {
	name  string
	fetch func(context.Context) ([]api.Incident, error)
}

// list reads every source independently; a failed source is reported in
// UnavailableSources instead of failing the list.
func (s *Service) list(ctx context.Context, acct state.Account, app state.App, since time.Time) api.IncidentsResponse {
	resp := api.IncidentsResponse{Since: since, Incidents: []api.Incident{}, UnavailableSources: []string{}}
	slugs := s.slugger(ctx, app)
	store := s.Host.Store()
	sources := []source{
		{api.IncidentSourceAlert, func(ctx context.Context) ([]api.Incident, error) {
			eps, err := store.ListAlertEpisodes(ctx, acct.ID, app.ID, since, sourceRows)
			out := make([]api.Incident, 0, len(eps))
			for _, ep := range eps {
				out = append(out, alertIncident(ep, slugs(ep.AppID)))
			}
			return out, err
		}},
		{api.IncidentSourceRouteMonitor, func(ctx context.Context) ([]api.Incident, error) {
			incs, err := store.ListRouteMonitorIncidentSummaries(ctx, acct.ID, app.ID, since, sourceRows)
			out := make([]api.Incident, 0, len(incs))
			for _, inc := range incs {
				out = append(out, routeIncident(inc, slugs(inc.AppID)))
			}
			return out, err
		}},
		{api.IncidentSourcePlatform, func(ctx context.Context) ([]api.Incident, error) {
			// Since filters on the last update, so an open incident nobody
			// updated in the window is read on its own.
			active, err := store.ListPublicStatusEvents(ctx, state.StatusEventListOptions{Kind: publicstatus.KindIncident, ActiveOnly: true, Limit: sourceRows})
			if err != nil {
				return nil, err
			}
			recent, err := store.ListPublicStatusEvents(ctx, state.StatusEventListOptions{Kind: publicstatus.KindIncident, Since: since, Limit: sourceRows})
			seen := map[string]bool{}
			out := make([]api.Incident, 0, len(active)+len(recent))
			for _, ev := range append(active, recent...) {
				if !seen[ev.PublicID] {
					seen[ev.PublicID] = true
					out = append(out, platformIncident(ev))
				}
			}
			return out, err
		}},
	}
	for _, src := range sources {
		incs, err := src.fetch(ctx)
		if err != nil {
			s.Host.Logger().WarnContext(ctx, "incidents: source failed", "source", src.name, "err", err)
			resp.UnavailableSources = append(resp.UnavailableSources, src.name)
			continue
		}
		resp.Incidents = append(resp.Incidents, incs...)
	}
	sort.SliceStable(resp.Incidents, func(i, j int) bool {
		a, b := resp.Incidents[i], resp.Incidents[j]
		if (a.Status == api.IncidentStatusOpen) != (b.Status == api.IncidentStatusOpen) {
			return a.Status == api.IncidentStatusOpen
		}
		return a.OpenedAt.After(b.OpenedAt)
	})
	if len(resp.Incidents) > api.IncidentsMaxEntries {
		resp.Incidents, resp.Truncated = resp.Incidents[:api.IncidentsMaxEntries], true
	}
	return resp
}

// slugger maps app ids to slugs, reading each app once per request. The
// filtered app is known up front.
func (s *Service) slugger(ctx context.Context, known state.App) func(string) string {
	slugs := map[string]string{"": ""}
	if known.ID != "" {
		slugs[known.ID] = known.Slug
	}
	return func(id string) string {
		if slug, ok := slugs[id]; ok {
			return slug
		}
		app, err := s.Host.Store().AppByID(ctx, id)
		if err != nil {
			s.Host.Logger().WarnContext(ctx, "incidents: resolve app", "app_id", id, "err", err)
		}
		slugs[id] = app.Slug
		return app.Slug
	}
}

func alertIncident(ep state.AlertEpisode, app string) api.Incident {
	inc := api.Incident{
		ID: alertPrefix + ep.ID, Source: api.IncidentSourceAlert, Status: api.IncidentStatusOpen,
		Title: ep.RuleName, App: app, OpenedAt: ep.OpenedAt, RuleID: ep.RuleID, Metric: string(ep.Metric),
	}
	if ep.ResolvedAt != nil {
		inc.Status, inc.ResolvedAt = api.IncidentStatusResolved, ep.ResolvedAt
	}
	return inc
}

func routeIncident(rm state.RouteMonitorIncidentSummary, app string) api.Incident {
	inc := api.Incident{
		ID: routePrefix + rm.ID, Source: api.IncidentSourceRouteMonitor, Status: api.IncidentStatusOpen,
		Title: "Route requirements violated after a deploy", App: app, OpenedAt: rm.OpenedAt, DeploymentID: rm.DeploymentID,
	}
	if rm.Status != "open" {
		inc.Status, inc.ResolvedAt = api.IncidentStatusResolved, rm.ClosedAt
	}
	return inc
}

func platformIncident(ev state.StatusIncident) api.Incident {
	title := ev.Title
	if title == "" {
		title = ev.Message
	}
	opened := ev.PostedAt
	if ev.StartsAt != nil {
		opened = *ev.StartsAt
	}
	inc := api.Incident{
		ID: platformPrefix + ev.PublicID, Source: api.IncidentSourcePlatform, Status: api.IncidentStatusOpen,
		Title: title, OpenedAt: opened.UTC(), Impact: string(ev.Impact),
	}
	if ev.ResolvedAt != nil {
		at := ev.ResolvedAt.UTC()
		inc.Status, inc.ResolvedAt = api.IncidentStatusResolved, &at
	}
	return inc
}

// splitID returns an incident id's source prefix and store id.
func splitID(id string) (string, string) {
	for _, prefix := range []string{alertPrefix, routePrefix, platformPrefix} {
		if rest, ok := strings.CutPrefix(id, prefix); ok && rest != "" {
			return prefix, rest
		}
	}
	return "", ""
}

func (s *Service) gate(w http.ResponseWriter) bool {
	if !s.Enabled {
		api.WriteProblem(w, api.ErrIncidentsUnavailable())
	}
	return s.Enabled
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// writeReadErr maps a detail read's error to a problem.
func (s *Service) writeReadErr(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrIncidentNotFound())
		return
	}
	s.Host.Logger().WarnContext(r.Context(), "incidents: read incident", "err", err)
	api.WriteProblem(w, api.ErrCapacity("could not read the incident"))
}
