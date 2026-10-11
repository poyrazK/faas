package main

// ADR-960/964 on the dashboard edge-rules page: per-rule mode and 24 h hit
// counts, a mode switch for log-mode rollouts, and the security-events table.

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

const dashboardEdgeRuleEventsLimit = 50

// addDashboardEdgeRuleHits fills each rule's last-24 h counts. Counts are
// telemetry: a read failure leaves them at zero.
func (s *server) addDashboardEdgeRuleHits(ctx context.Context, log *slog.Logger, appID string, items []dashboard.EdgeRulePageItem) {
	store, ok := s.store.(state.EdgeRuleHitStore)
	if !ok {
		return
	}
	stats, err := store.EdgeRuleHitStatsForApp(ctx, appID, time.Now().UTC().Add(-24*time.Hour).Truncate(time.Hour))
	if err != nil {
		log.Warn("dashboard edge rules: hit counts", "app_id", appID, "err", err)
		return
	}
	byRule := make(map[string]state.EdgeRuleHitStats, len(stats))
	for _, st := range stats {
		byRule[st.RuleID] = st
	}
	for i := range items {
		items[i].Matched24h, items[i].Logged24h = byRule[items[i].ID].Matched, byRule[items[i].ID].Logged
	}
}

// loadDashboardEdgeRuleEvents reads the events table from the page's own
// query string (rule, outcome, since, cursor), with the API's validation and
// plan window. Invalid filters show an inline message instead of failing
// the page.
func (s *server) loadDashboardEdgeRuleEvents(r *http.Request, log *slog.Logger, acct state.Account, app state.App, rules []state.EdgeRule) dashboard.EdgeRuleEventsPageData {
	q := r.URL.Query()
	data := dashboard.EdgeRuleEventsPageData{Rule: q.Get("rule"), Outcome: q.Get("outcome"), Since: q.Get("since")}
	if data.Since == "" {
		data.Since = "24h"
	}
	store, ok := s.store.(state.EdgeRuleEventStore)
	if !ok {
		data.ErrorMessage = "Security events are not available on this deployment."
		return data
	}
	query, prob := edgeRuleEventsQuery(r, acct.Plan, app.ID, time.Now().UTC())
	if prob != nil {
		data.ErrorMessage = prob.Detail
		return data
	}
	query.Limit = dashboardEdgeRuleEventsLimit + 1
	events, err := store.ListEdgeRuleEvents(r.Context(), query)
	if err != nil {
		log.Warn("dashboard edge rules: list events", "app_id", app.ID, "err", err)
		data.ErrorMessage = "Security events are temporarily unavailable."
		return data
	}
	page := edgeRuleEventsResponse(query.Since, events, rules, dashboardEdgeRuleEventsLimit)
	data.SinceLabel = page.Since.Format("2006-01-02 15:04 MST")
	for _, e := range page.Events {
		label := e.RuleName
		if label == "" {
			label = e.RuleKind
		}
		data.Events = append(data.Events, dashboard.EdgeRuleEventPageItem{
			OccurredAt: e.OccurredAt.Format("2006-01-02 15:04:05"), RuleID: e.RuleID, RuleLabel: label,
			Outcome: e.Outcome, Method: e.Method, HostPath: e.Host + e.Path, ClientIP: e.ClientIP,
			Country: e.Country, UserAgent: e.UserAgent, RequestID: e.RequestID,
		})
	}
	if page.NextCursor != "" {
		next := url.Values{"since": {data.Since}, "cursor": {page.NextCursor}}
		if data.Rule != "" {
			next.Set("rule", data.Rule)
		}
		if data.Outcome != "" {
			next.Set("outcome", data.Outcome)
		}
		data.NextURL = "/dashboard/apps/" + url.PathEscape(app.Slug) + "/edge-rules?" + next.Encode() + "#security-events"
	}
	return data
}

// dashboardSetEdgeRuleMode switches a rule between log and enforce mode
// (ADR-960): the "start enforcing" step of a log-mode rollout.
func (s *server) dashboardSetEdgeRuleMode(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	if !s.verifyDashboardEdgeRulesCSRF(w, r, acct.ID) {
		return
	}
	slug, id := r.PathValue("slug"), r.PathValue("id")
	mode := r.PostFormValue("mode")
	if mode != state.EdgeRuleModeEnforce && mode != state.EdgeRuleModeLog {
		http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/edge-rules?action=error", http.StatusSeeOther)
		return
	}
	app, err := s.store.AppBySlug(r.Context(), slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	rule, err := s.store.GetEdgeRuleByID(r.Context(), id)
	if err != nil || rule.AccountID != acct.ID || rule.AppID != app.ID {
		http.NotFound(w, r)
		return
	}
	resp := s.forwardDashboardEdgeRuleJSON(r, acct, http.MethodPatch, "/v1/edge-rules/"+url.PathEscape(id), id, api.UpdateEdgeRuleRequest{Mode: &mode}, s.updateEdgeRule)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/edge-rules?action=mode_"+mode, http.StatusSeeOther)
}

// edgeRuleModeOrDefault renders a stored mode; empty predates ADR-960.
func edgeRuleModeOrDefault(mode string) string {
	if mode == "" {
		return state.EdgeRuleModeEnforce
	}
	return mode
}
