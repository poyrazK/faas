package apidserver

// ADR-960/964 on the dashboard edge-rules page: per-rule mode and 24 h hit
// counts, a mode switch for log-mode rollouts, and the security-events table.
// ADR-20261020105001: a 7-day trend per rule and the rule review.

import (
	"cmp"
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/edgerules"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

const dashboardEdgeRuleEventsLimit = 50

// dashboardEdgeRuleHits is the app's hourly match totals per rule over the
// review window.
type dashboardEdgeRuleHits struct {
	start, now time.Time
	series     map[string][]int64
}

// dashboardSparkBucketHours and dashboardSparkBuckets shape the 7-day trend:
// 28 six-hour totals.
const (
	dashboardSparkBucketHours = 6
	dashboardSparkBuckets     = 28
)

// addDashboardEdgeRuleHits fills each rule's last-24 h counts and 7-day
// trend from one read of the hourly buckets. Counts are telemetry: a read
// failure leaves them at zero and returns ok=false.
func (s *server) addDashboardEdgeRuleHits(ctx context.Context, log *slog.Logger, appID string, items []dashboard.EdgeRulePageItem) (dashboardEdgeRuleHits, bool) {
	store, ok := s.store.(state.EdgeRuleHitStore)
	if !ok {
		return dashboardEdgeRuleHits{}, false
	}
	now := time.Now().UTC()
	hits := dashboardEdgeRuleHits{start: now.Add(-api.EdgeRuleReviewQuietWindow).Truncate(time.Hour), now: now}
	rows, err := store.EdgeRuleHitSeriesForApp(ctx, appID, hits.start)
	if err != nil {
		log.Warn("dashboard edge rules: hit counts", "app_id", appID, "err", err)
		return dashboardEdgeRuleHits{}, false
	}
	dayStart := now.Add(-24 * time.Hour).Truncate(time.Hour)
	day := map[string]state.EdgeRuleHitStats{}
	for _, h := range rows {
		if h.Bucket.Before(dayStart) {
			continue
		}
		st := day[h.RuleID]
		if h.Outcome == state.EdgeRuleHitLogged {
			st.Logged += h.Hits
		} else {
			st.Matched += h.Hits
		}
		day[h.RuleID] = st
	}
	hits.series = edgerules.EdgeRuleHitSeries(rows, hits.start, now)
	for i := range items {
		items[i].Matched24h, items[i].Logged24h = day[items[i].ID].Matched, day[items[i].ID].Logged
		trend := api.FoldEdgeRuleSeries(hits.series[items[i].ID], dashboardSparkBucketHours, dashboardSparkBuckets)
		items[i].SparkPoints = dashboard.SparklinePoints(trend)
		for _, n := range trend {
			items[i].Matches7d += n
		}
	}
	return hits, true
}

// dashboardEdgeRuleReview runs the ADR-20261020105001 review over the app's enabled,
// unexpired rules: log-mode rules ready to enforce first (most matches
// first), then those still collecting, then rules that went quiet.
func dashboardEdgeRuleReview(rules []state.EdgeRule, items []dashboard.EdgeRulePageItem, hits dashboardEdgeRuleHits) []dashboard.EdgeRuleReviewView {
	byID := make(map[string]dashboard.EdgeRulePageItem, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	var out []dashboard.EdgeRuleReviewView
	for _, rule := range rules {
		if rule.ExpiresAt != nil && !rule.ExpiresAt.After(hits.now) {
			continue
		}
		review := api.ReviewEdgeRule(api.EdgeRuleReviewInput{
			Mode: edgeRuleModeOrDefault(rule.Mode), Enabled: rule.Enabled, ChangedAt: rule.UpdatedAt,
			SeriesStart: hits.start, Series: hits.series[rule.ID], Now: hits.now,
		})
		if review.Status == "" {
			continue
		}
		item := byID[rule.ID]
		label := cmp.Or(item.Name, string(rule.Kind)+" rule")
		match := item.MatchHost + item.MatchPath
		if item.AppWide {
			match = "every app host " + item.MatchPath
		}
		out = append(out, dashboard.EdgeRuleReviewView{
			ID: rule.ID, Label: label, Match: match, Status: review.Status,
			Since: review.Since.Format("Jan 2 15:04 MST"), ObservedHours: int64(review.Observed / time.Hour),
			Matches: review.Matches, PerDay: review.PerDay,
		})
	}
	rank := map[string]int{api.EdgeRuleReviewReady: 0, api.EdgeRuleReviewCollecting: 1, api.EdgeRuleReviewQuiet: 2}
	slices.SortStableFunc(out, func(a, b dashboard.EdgeRuleReviewView) int {
		return cmp.Or(cmp.Compare(rank[a.Status], rank[b.Status]), cmp.Compare(b.Matches, a.Matches))
	})
	return out
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
	query, prob := edgerules.EdgeRuleEventsQuery(r, acct.Plan, app.ID, time.Now().UTC())
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
	page := edgerules.EdgeRuleEventsResponse(query.Since, events, rules, dashboardEdgeRuleEventsLimit)
	data.SinceLabel = page.Since.Format("2006-01-02 15:04 MST")
	for _, e := range page.Events {
		label := e.RuleName
		if label == "" {
			label = e.RuleKind
		}
		data.Events = append(data.Events, dashboard.EdgeRuleEventPageItem{
			OccurredAt: e.OccurredAt.Format("2006-01-02 15:04:05"), RuleID: e.RuleID, RuleLabel: label,
			Outcome: e.Outcome, Method: e.Method, HostPath: e.Host + e.Path, ClientIP: e.ClientIP,
			Country: e.Country, UserAgent: e.UserAgent, RequestID: e.RequestID, WAF: e.WAFSummary(),
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
	resp := s.forwardDashboardEdgeRuleJSON(r, acct, http.MethodPatch, "/v1/edge-rules/"+url.PathEscape(id), id, api.UpdateEdgeRuleRequest{Mode: &mode}, s.edgeRules.UpdateEdgeRule)
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
