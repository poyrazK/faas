package main

// Per-app wake-timeline JSON mirror (per-app observability PR series,
// commit 3).
//
// GET /v1/apps/{slug}/wake-timeline
//
// Wire-friendly mirror of the dashboard HTML page at
// cmd/apid/handlers_dashboard.go:2548 (renderAppWakeTimeline). The
// HTML page keeps its pre-rendered HTML chips (RenderTriggerHistogram
// + RenderWakeTimelineTable) — this handler emits the same shape as
// JSON so a separate frontend agent can render without re-parsing
// HTML. The aggregation math (24h cutoff descending-break, the
// two-denominator rule for at-capacity %, em-dash policy) is shared
// with the HTML page via buildWakeTimeline below; the HTML page is
// untouched.
//
// Filename note: this lives alongside handlers_wake_timeline.go
// (issue #517 / PR-C, route /v1/apps/{slug}/wakes/{wake_id}/timeline)
// — distinct routes, distinct files. The shared aggregation is
// re-implemented here rather than extracted so the HTML page's
// inline-loop stays untouched (PR-A review-cluster precedent:
// don't refactor working code without a failing test).
//
// Plan gate: Hobby+ (PerAppMetricsAllowed). 402 on Free — same code
// as /v1/apps/{slug}/metrics (plan_per_app_metrics_not_allowed). The
// gate runs BEFORE loadApp so a Free customer probing a Hobby+ slug
// never gets a 404 (slug-leak guard).
//
// Auth chain matches getAppMetrics (read-only, no MFA, primary
// caller is an API key with ScopesReadSurface). IDOR-safe via
// loadApp.

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// getAppWakeTimeline serves GET /v1/apps/{slug}/wake-timeline.
// Returns api.AppWakeTimelineResponse. 200 on success, 402 on Free,
// 404 on cross-account slug (via loadApp).
func (s *server) getAppWakeTimeline(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !acct.Plan.PerAppMetricsAllowed() {
		api.WriteProblem(w, api.ErrPlanPerAppMetricsNotAllowed(acct.Plan))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug")) //nolint:contextcheck // loadApp uses r.Context() for its own DB calls; helper is shared across every per-app handler.
	if !ok {
		return
	}

	since, until, ok := parseWakeTimelineWindow(w, r)
	if !ok {
		return
	}
	resp, err := buildAppWakeTimelineWindow(r.Context(), s.store, app, s.log, acct, since, until)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"wake-timeline fetch failed",
			"could not load wake-timeline; see server logs"))
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func parseWakeTimelineWindow(w http.ResponseWriter, r *http.Request) (time.Time, time.Time, bool) {
	now := time.Now().UTC()
	until := now
	if raw := r.URL.Query().Get("until"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid until", "until must be an RFC3339 timestamp"))
			return time.Time{}, time.Time{}, false
		}
		until = parsed.UTC()
	}
	since := until.Add(-24 * time.Hour)
	if raw := r.URL.Query().Get("since"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid since", "since must be an RFC3339 timestamp"))
			return time.Time{}, time.Time{}, false
		}
		since = parsed.UTC()
	}
	if since.After(until) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid wake-timeline window", "since must be before until"))
		return time.Time{}, time.Time{}, false
	}
	return since, until, true
}

// buildAppWakeTimeline runs the SQL reads + aggregation math for
// the JSON mirror. The default is the dashboard's 24h window;
// explicit since/until query parameters let CLI callers request a
// longer bounded history (A4 uses the trailing seven days). The
// two-denominator rule for at-capacity percentage remains shared
// with the dashboard helper.
//
// Returns the response with AsOf set to the caller's local
// time.Now().UTC(); the handler stamps that as the JSON envelope's
// authoritative "as of" instant.
func buildAppWakeTimelineWindow(
	ctx context.Context,
	store interface {
		ListLatestInstancesForApp(ctx context.Context, appID string, limit int) ([]state.Instance, error)
		LookupBootStartedForWakes(ctx context.Context, wakeIDs []string) (map[string]state.WakeBootMeta, error)
	},
	app state.App,
	log *slog.Logger,
	acct state.Account,
	since, until time.Time,
) (api.AppWakeTimelineResponse, error) {
	instances, err := store.ListLatestInstancesForApp(ctx, app.ID, 50)
	if err != nil {
		log.Warn("wake-timeline: list recent instances", "account_id", acct.ID, "app_id", app.ID, "err", err)
		instances = nil
	}
	wakeIDs := make([]string, 0, len(instances))
	for _, ins := range instances {
		if ins.WakeID != "" {
			wakeIDs = append(wakeIDs, ins.WakeID)
		}
	}
	bootMetas := make(map[string]state.WakeBootMeta)
	if len(wakeIDs) > 0 {
		if m, err := store.LookupBootStartedForWakes(ctx, wakeIDs); err == nil {
			bootMetas = m
		} else if log != nil {
			log.Warn("wake-timeline: lookup boot started", "account_id", acct.ID, "app_id", app.ID, "err", err)
		}
	}

	// Counter rollup. The two-denominator rule +
	// descending-cutoff break invariants (PR-A review cluster
	// findings #4/#5, PR #1031) live in
	// aggregateWakeTimeline — shared with the HTML page at
	// renderAppWakeTimeline (handlers_dashboard.go). Row-shape
	// differences (JSON has additional AtCapacityPresent +
	// ReadyInMS em-dash sentinel semantics) stay here because
	// they're a wire-contract concern, not an aggregation
	// concern.
	timelineInstances := wakeTimelineWindow(instances, since, until)
	// The range has already been applied above; a zero cutoff keeps the
	// shared aggregation helper's counters and denominator semantics
	// intact for both the default 24h and explicit windows.
	agg := aggregateWakeTimeline(timelineInstances, bootMetas, time.Time{})

	// Use the shared helper for the post-cutoff prefix so the
	// row-build loop doesn't re-implement the same
	// descending-break that aggregateWakeTimeline just performed
	// — the review cluster flagged this duplication as the
	// primary drift hazard.
	timelineRows := timelineInstances

	rows := make([]api.WakeTimelineJSONRow, 0, len(timelineRows))
	for _, ins := range timelineRows {
		row := api.WakeTimelineJSONRow{
			WakeID:            ins.WakeID,
			Kind:              "wake.boot_started",
			State:             ins.State,
			AtCapacity:        false,
			AtCapacityPresent: false,
			ReadyInMS:         -1, // em-dash sentinel — see dto.go
		}
		if !ins.StartedAt.IsZero() {
			row.At = ins.StartedAt.UTC().Format(time.RFC3339)
		}
		if meta, hasMeta := bootMetas[ins.WakeID]; hasMeta {
			row.Trigger = meta.Trigger
			row.Method = meta.Method
			row.Tier = meta.Tier
			row.QueuedCount = int32(meta.QueuedCount)
			row.ConcurrencyAtAdmit = int32(meta.ConcurrencyAtAdmit)
			row.AtCapacity = meta.AtCapacity
			row.AtCapacityPresent = meta.AtCapacityPresent
			if meta.ReadyInMS > 0 {
				row.ReadyInMS = int32(meta.ReadyInMS)
			}
		}
		rows = append(rows, row)
	}
	return api.AppWakeTimelineResponse{
		App: api.WakeTimelineApp{
			AppID: app.ID,
			Slug:  app.Slug,
		},
		WakeCount24h:      agg.WakeCount24h,
		WakeCountWithMeta: agg.WakeCountWithMeta,
		AtCapacityCount:   agg.AtCapacityCount,
		AtCapacityPct:     agg.AtCapacityPct,
		TriggerHistogram:  agg.TriggerHistogram, // empty map, not nil — wire shape contract
		Rows:              rows,
		AsOf:              time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func wakeTimelineWindow(instances []state.Instance, since, until time.Time) []state.Instance {
	if len(instances) == 0 {
		return instances
	}
	rows := make([]state.Instance, 0, len(instances))
	for _, ins := range instances {
		if !ins.StartedAt.IsZero() {
			started := ins.StartedAt.UTC()
			if started.After(until) {
				continue
			}
			if started.Before(since) {
				break
			}
		}
		rows = append(rows, ins)
	}
	return rows
}
