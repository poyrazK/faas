package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// getAppChangeTimeline serves GET /v1/apps/{slug}/changes (ADR-741): a
// read-only, newest-first merge of the app's recorded changes and health
// transitions. A dark preview: without FAAS_CHANGE_TIMELINE_ENABLED=1 it
// answers 503.
func (s *server) getAppChangeTimeline(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.changeTimelineEnabled {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "change_timeline_unavailable",
			"Change timeline unavailable", "the change timeline preview is not enabled"))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug")) //nolint:contextcheck // loadApp uses r.Context().
	if !ok {
		return
	}
	now := time.Now().UTC()
	since, until, err := changeTimelineWindow(r, now)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "invalid window", err.Error()))
		return
	}
	resp := s.appChangeTimeline(r.Context(), acct, app, since, until, now)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, resp)
}

// changeTimelineWindow parses ?since= and ?until= (RFC 3339). Until defaults
// to now and is clamped to it; since defaults to until minus the default
// window. The window must be positive and at most ChangeTimelineMaxWindow.
func changeTimelineWindow(r *http.Request, now time.Time) (time.Time, time.Time, error) {
	until := now
	if raw := r.URL.Query().Get("until"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("until must be RFC 3339 (e.g. 2026-10-09T12:00:00Z)")
		}
		if t.Before(now) {
			until = t.UTC()
		}
	}
	since := until.Add(-api.ChangeTimelineDefaultWindow)
	if raw := r.URL.Query().Get("since"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("since must be RFC 3339 (e.g. 2026-10-08T12:00:00Z)")
		}
		since = t.UTC()
	}
	if !since.Before(until) {
		return time.Time{}, time.Time{}, errors.New("since must be before until")
	}
	if until.Sub(since) > api.ChangeTimelineMaxWindow {
		return time.Time{}, time.Time{}, fmt.Errorf("window must be at most %s", api.ChangeTimelineMaxWindow)
	}
	return since, until, nil
}

type changeTimelineSource struct {
	name  string
	fetch func(context.Context) ([]api.AppChangeEvent, error)
}

// appChangeTimeline reads every source independently. A failed source is
// reported in UnavailableSources instead of failing the timeline.
func (s *server) appChangeTimeline(ctx context.Context, acct state.Account, app state.App, since, until, now time.Time) api.AppChangeTimelineResponse {
	resp := api.AppChangeTimelineResponse{
		AppID: app.ID, AppSlug: app.Slug, Since: since, Until: until,
		Events: []api.AppChangeEvent{}, UnavailableSources: []string{},
	}
	for _, src := range s.changeTimelineSources(acct, app, since, until, now) {
		events, err := src.fetch(ctx)
		if err != nil {
			if s.log != nil {
				s.log.Warn("apid: change timeline source failed", "source", src.name, "err", err)
			}
			resp.UnavailableSources = append(resp.UnavailableSources, src.name)
			continue
		}
		for _, e := range events {
			if !e.At.Before(since) && e.At.Before(until) {
				resp.Events = append(resp.Events, e)
			}
		}
	}
	sort.SliceStable(resp.Events, func(i, j int) bool { return resp.Events[i].At.After(resp.Events[j].At) })
	if len(resp.Events) > api.ChangeTimelineMaxEvents {
		resp.Events, resp.Truncated = resp.Events[:api.ChangeTimelineMaxEvents], true
	}
	return resp
}

func (s *server) changeTimelineSources(acct state.Account, app state.App, since, until, now time.Time) []changeTimelineSource {
	return []changeTimelineSource{
		{api.ChangeSourceDeployment, func(ctx context.Context) ([]api.AppChangeEvent, error) {
			store, ok := s.store.(state.AppChangeSourceStore)
			if !ok {
				return nil, errors.New("store lacks app change reads")
			}
			rows, err := store.ListAppDeploymentAuditBetween(ctx, acct.ID, app.ID, since, until, api.ChangeTimelineMaxEvents)
			return deploymentChangeEvents(rows), err
		}},
		{api.ChangeSourceEdgeRule, func(ctx context.Context) ([]api.AppChangeEvent, error) {
			store, ok := s.store.(state.AppChangeSourceStore)
			if !ok {
				return nil, errors.New("store lacks app change reads")
			}
			rows, err := store.ListAppEdgeRuleChangesBetween(ctx, acct.ID, app.ID, since, until, api.ChangeTimelineMaxEvents)
			return edgeRuleChangeEvents(rows), err
		}},
		{api.ChangeSourceRuntimeConfig, func(ctx context.Context) ([]api.AppChangeEvent, error) {
			at, ok, err := s.store.AppRuntimeConfigChangedAt(ctx, app.ID)
			if err != nil || !ok {
				return nil, err
			}
			return []api.AppChangeEvent{{At: at.UTC(), Source: api.ChangeSourceRuntimeConfig, Kind: "runtime_config.changed", Summary: "Runtime configuration changed"}}, nil
		}},
		{api.ChangeSourceIncident, func(ctx context.Context) ([]api.AppChangeEvent, error) {
			return s.incidentChangeEvents(ctx, acct, app)
		}},
		{api.ChangeSourceHealth, func(ctx context.Context) ([]api.AppChangeEvent, error) {
			return s.healthChangeEvents(ctx, acct, app, now)
		}},
		{api.ChangeSourceActivity, func(ctx context.Context) ([]api.AppChangeEvent, error) {
			return s.activityChangeEvents(ctx, app)
		}},
	}
}

var deploymentChangeSummaries = map[string]string{
	"deploy.created":              "Deployment %s created",
	"deploy.source_ref":           "Deployment %s built from a source ref",
	"deploy.local_tarball":        "Deployment %s uploaded from local source",
	"deploy.traffic_changed":      "Traffic changed for deployment %s",
	"deploy.health_probe_failed":  "Health probe failed for deployment %s",
	"deploy.health_recovered":     "Deployment %s passed health probes again",
	"deploy.rolled_back":          "Rollback recorded for deployment %s",
	"deploy.removed":              "Deployment %s removed",
	"deploy.rollout_started":      "Rollout of deployment %s started",
	"deploy.rollout_completed":    "Rollout of deployment %s completed",
	"deploy.rollout_aborted":      "Rollout of deployment %s aborted",
	"deploy.canary_step_advanced": "Canary of deployment %s advanced",
	"deploy.alert_rule_fired":     "Alert fired for deployment %s",
	"deploy.scan_regressed":       "Vulnerability scan regressed for deployment %s",
}

func deploymentChangeEvents(rows []state.AppChangeRow) []api.AppChangeEvent {
	out := make([]api.AppChangeEvent, 0, len(rows))
	for _, row := range rows {
		format, ok := deploymentChangeSummaries[row.Kind]
		if !ok {
			format = row.Kind + " for deployment %s"
		}
		out = append(out, api.AppChangeEvent{
			At: row.At.UTC(), Source: api.ChangeSourceDeployment, Kind: row.Kind,
			DeploymentID: row.DeploymentID, Summary: fmt.Sprintf(format, shortChangeID(row.DeploymentID)),
		})
	}
	return out
}

func edgeRuleChangeEvents(rows []state.AppChangeRow) []api.AppChangeEvent {
	out := make([]api.AppChangeEvent, 0, len(rows))
	for _, row := range rows {
		out = append(out, api.AppChangeEvent{
			At: row.At.UTC(), Source: api.ChangeSourceEdgeRule, Kind: "edge_rule." + row.Kind,
			Summary: fmt.Sprintf("Edge rule %s %s", shortChangeID(row.RuleID), row.Kind),
		})
	}
	return out
}

func (s *server) incidentChangeEvents(ctx context.Context, acct state.Account, app state.App) ([]api.AppChangeEvent, error) {
	store, ok := s.store.(state.RouteMonitorStore)
	if !ok {
		return nil, errors.New("store lacks route monitor reads")
	}
	page, err := store.ListRouteMonitorIncidents(ctx, acct.ID, app.ID, api.RouteMonitorMaxPage, "")
	if errors.Is(err, state.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []api.AppChangeEvent
	for _, inc := range page.Incidents {
		out = append(out, api.AppChangeEvent{
			At: inc.OpenedAt.UTC(), Source: api.ChangeSourceIncident, Kind: "incident.opened",
			DeploymentID: inc.DeploymentID, Summary: "Route incident opened",
		})
		if inc.ClosedAt != nil {
			out = append(out, api.AppChangeEvent{
				At: inc.ClosedAt.UTC(), Source: api.ChangeSourceIncident, Kind: "incident.closed",
				DeploymentID: inc.DeploymentID, Summary: "Route incident closed",
			})
		}
	}
	return out, nil
}

func (s *server) healthChangeEvents(ctx context.Context, acct state.Account, app state.App, now time.Time) ([]api.AppChangeEvent, error) {
	store, ok := s.store.(state.AppHealthHistoryStore)
	if !ok {
		return nil, errors.New("store lacks app health history")
	}
	page, err := store.ListAppHealthHistory(ctx, acct.ID, app.ID, api.AppHealthHistoryMaxPage, "", now)
	if errors.Is(err, state.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []api.AppChangeEvent
	for _, entry := range page.Entries {
		at, err := time.Parse(time.RFC3339Nano, entry.ObservedAt)
		if err != nil {
			continue
		}
		summary := "Health recorded as " + entry.Assessment.Status
		if entry.PreviousStatus != "" && entry.PreviousStatus != entry.Assessment.Status {
			summary = fmt.Sprintf("Health changed from %s to %s", entry.PreviousStatus, entry.Assessment.Status)
		}
		out = append(out, api.AppChangeEvent{
			At: at.UTC(), Source: api.ChangeSourceHealth, Kind: "health." + entry.Kind, Summary: summary,
		})
	}
	return out, nil
}

// activityChangeSummaries is the closed set of org-activity kinds the
// timeline shows. deploy.* activity is excluded because deployment_audit
// already records it.
var activityChangeSummaries = map[string]string{
	"env.set":           "Environment variable %s set",
	"env.deleted":       "Environment variable %s deleted",
	"domain.added":      "Domain %s added",
	"domain.removed":    "Domain %s removed",
	"domain.tls_issued": "TLS certificate issued for %s",
}

func (s *server) activityChangeEvents(ctx context.Context, app state.App) ([]api.AppChangeEvent, error) {
	orgID, err := uuid.Parse(app.OrgID)
	if err != nil {
		return nil, nil // apps without an owning org have no org activity
	}
	appID, err := uuid.Parse(app.ID)
	if err != nil {
		return nil, fmt.Errorf("parse app id: %w", err)
	}
	store, ok := s.store.(state.OrgActivityStore)
	if !ok {
		return nil, errors.New("store lacks org activity reads")
	}
	rows, err := store.ListOrgActivity(ctx, state.OrgActivityFilter{OrgID: orgID, AppID: &appID, Limit: api.ChangeTimelineMaxEvents})
	if err != nil {
		return nil, err
	}
	var out []api.AppChangeEvent
	for _, row := range rows {
		format, ok := activityChangeSummaries[row.Kind]
		if !ok {
			continue
		}
		e := api.AppChangeEvent{
			At: row.OccurredAt.UTC(), Source: api.ChangeSourceActivity, Kind: row.Kind,
			Summary: fmt.Sprintf(format, row.ResourceLabel),
		}
		if row.DeploymentID != nil {
			e.DeploymentID = row.DeploymentID.String()
		}
		out = append(out, e)
	}
	return out, nil
}

// shortChangeID renders the first eight hex digits of an ID for summaries.
func shortChangeID(id string) string {
	id = strings.ReplaceAll(id, "-", "")
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
