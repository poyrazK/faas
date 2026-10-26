package aiconsole

import (
	"net/http"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	spendRowsMax   = 20
	recentAlerts   = 10
	recentTraces   = 10
	tracesPageSize = 50
)

// integrationPage is one integration's UTC day.
func (s *Service) integrationPage(w http.ResponseWriter, r *http.Request, acct state.Account, integrationID string) {
	ctx := r.Context()
	offer, found, err := s.integration(ctx, acct.ID, integrationID)
	if err == nil && !found {
		http.NotFound(w, r)
		return
	}
	now := s.now()
	day := r.URL.Query().Get("date")
	if day == "" {
		day = now.Format(dayLayout)
	}
	data := dashboard.AIIntegrationData{Name: offer.Name, Provider: offer.AIProvider, Href: integrationHref(integrationID),
		TracesHref: tracesHref(integrationID), Day: day, MaxDay: now.Format(dayLayout),
		MinDay: now.AddDate(0, 0, -(api.OutboundAIUsageRetentionDays - 1)).Format(dayLayout)}
	page := dashboard.Page{Title: offer.Name + " · AI gateway", Body: "ai_integration"}
	if err != nil {
		s.log().Warn("dashboard ai: integration unavailable", "account_id", acct.ID, "err", err)
		data.Unavailable = unavailable("integration", err)
		page.Data = data
		s.Host.Render(w, r, acct, page)
		return
	}
	parsed, err := time.Parse(dayLayout, day)
	if err != nil || parsed.After(now) || now.Sub(parsed) >= api.OutboundAIUsageRetentionDays*24*time.Hour {
		data.Unavailable = "Choose a UTC day within the last 90 days."
		page.Data = data
		s.Host.Render(w, r, acct, page)
		return
	}
	apps, hidden := s.appNames(ctx, acct)
	store := s.Host.Store()
	if usageStore, ok := store.(state.OutboundAIStore); ok {
		usage, err := usageStore.GetOutboundAIUsage(ctx, acct.ID, integrationID, day)
		if err != nil {
			s.log().Warn("dashboard ai: usage unavailable", "account_id", acct.ID, "integration_id", integrationID, "err", err)
			data.Unavailable = unavailable("usage", err)
		} else {
			data.Summary, data.Models, data.Callers = usageViews(usage, apps, hidden)
		}
	}
	s.spendViews(r, acct, offer, day, now, &data)
	s.latencyView(r, acct, offer, &data)
	s.alertViews(r, acct, integrationID, &data)
	s.recentTraceViews(r, acct, offer, apps, hidden, &data)
	page.Data = data
	s.Host.Render(w, r, acct, page)
}

// usageViews turns a day's rollup into the summary and the by-model and
// by-caller tables, most expensive first. The integration's spend is the
// account's, so hidden apps still count, as one otherApps row.
func usageViews(usage state.OutboundAIUsage, apps map[string]string, hidden map[string]bool) (dashboard.AIUsageSummary, []dashboard.AIUsageRow, []dashboard.AIUsageRow) {
	total := sumUsage(usage.Items)
	summary := dashboard.AIUsageSummary{
		Requests: count(total.Requests), Errors: count(total.ErrorRequests), ErrorRate: percent(total.ErrorRequests, total.Requests),
		Tokens: count(total.InputTokens + total.OutputTokens), Cost: money(total.CostMillicents),
		MonthCost: money(usage.MonthlyCostMillicents), DailyCostLimit: optionalMoney(usage.DailyCostLimit),
		MonthlyCostLimit: optionalMoney(usage.MonthlyCostLimit),
		CacheSaved:       money(total.CacheSavedMillicents + total.PromptCacheSavedMillicents),
	}
	byModel, byCaller := map[string]*state.OutboundAIUsageItem{}, map[string]*state.OutboundAIUsageItem{}
	for _, item := range usage.Items {
		caller := callerName(item.Caller, apps)
		if hidden[item.Caller] {
			caller = otherApps
		}
		for _, group := range []struct {
			rows map[string]*state.OutboundAIUsageItem
			key  string
		}{{byModel, item.Model}, {byCaller, caller}} {
			row := group.rows[group.key]
			if row == nil {
				row = &state.OutboundAIUsageItem{}
				group.rows[group.key] = row
			}
			addUsage(row, item)
		}
	}
	return summary, usageRows(byModel, total.CostMillicents), usageRows(byCaller, total.CostMillicents)
}

func usageRows(rows map[string]*state.OutboundAIUsageItem, totalCost int64) []dashboard.AIUsageRow {
	names := make([]string, 0, len(rows))
	for name := range rows {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if rows[names[i]].CostMillicents != rows[names[j]].CostMillicents {
			return rows[names[i]].CostMillicents > rows[names[j]].CostMillicents
		}
		if rows[names[i]].Requests != rows[names[j]].Requests {
			return rows[names[i]].Requests > rows[names[j]].Requests
		}
		return names[i] < names[j]
	})
	views := make([]dashboard.AIUsageRow, 0, len(names))
	for _, name := range names {
		row := rows[name]
		views = append(views, dashboard.AIUsageRow{Name: name, Requests: count(row.Requests),
			ErrorRate: percent(row.ErrorRequests, row.Requests), InputTokens: count(row.InputTokens),
			OutputTokens: count(row.OutputTokens), Cost: money(row.CostMillicents), Quality: qualityFlags(*row),
			Ratings: ratings(*row), Score: meanScore(*row), Bar: share(row.CostMillicents, totalCost)})
	}
	return views
}

// spendViews fills the most expensive tags and end users of the day; both
// are kept for OutboundAIUserUsageRetentionDays.
func (s *Service) spendViews(r *http.Request, acct state.Account, offer state.OutboundIntegrationOffer, day string, now time.Time,
	data *dashboard.AIIntegrationData) {
	parsed, _ := time.Parse(dayLayout, day)
	if now.Sub(parsed) >= api.OutboundAIUserUsageRetentionDays*24*time.Hour {
		data.TagsNote = "Usage by tag is kept for 31 days."
		data.UsersNote = "Usage by end user is kept for 31 days."
		return
	}
	ctx, store := r.Context(), s.Host.Store()
	if tags, ok := store.(state.OutboundAITagStore); ok {
		usage, err := tags.GetOutboundAITagUsage(ctx, acct.ID, offer.ID, day, "", spendRowsMax)
		switch {
		case err != nil:
			data.TagsNote = unavailable("tag usage", err)
		case len(usage.Items) == 0:
			data.TagsNote = "No tagged calls this day. Tag calls with X-Gregale-AI-Tags: feature=search,tier=free."
		default:
			var total int64
			for _, item := range usage.Items {
				total += item.CostMillicents
			}
			for _, item := range usage.Items {
				data.Tags = append(data.Tags, dashboard.AISpendRow{Name: item.Tag, Requests: count(item.Requests),
					Tokens: count(item.InputTokens + item.OutputTokens), Cost: money(item.CostMillicents),
					Limit: optionalMoney(item.DailyCostLimit), Bar: share(item.CostMillicents, total)})
			}
			if usage.TrackedTags > int64(len(usage.Items)) {
				data.TagsNote = "The " + count(int64(len(usage.Items))) + " most expensive of " + count(usage.TrackedTags) + " tags."
			}
		}
	}
	if users, ok := store.(state.OutboundAIUserStore); ok {
		usage, err := users.GetOutboundAIUserUsage(ctx, acct.ID, offer.ID, day, "", spendRowsMax)
		switch {
		case err != nil:
			data.UsersNote = unavailable("end-user usage", err)
		case len(usage.Items) == 0:
			data.UsersNote = "No calls named an end user this day. Name one with X-Gregale-AI-User."
		default:
			var total int64
			for _, item := range usage.Items {
				total += item.CostMillicents
			}
			for _, item := range usage.Items {
				data.Users = append(data.Users, dashboard.AISpendRow{Name: item.EndUser, Requests: count(item.Requests),
					Tokens: count(item.InputTokens + item.OutputTokens), Cost: money(item.CostMillicents),
					Bar: share(item.CostMillicents, total)})
			}
			if usage.TrackedUsers > int64(len(usage.Items)) {
				data.UsersNote = "The " + count(int64(len(usage.Items))) + " most expensive of " + count(usage.TrackedUsers) + " end users."
			}
		}
	}
}

// latencyView fills latency and error rate by model from recent logged
// calls.
func (s *Service) latencyView(r *http.Request, acct state.Account, offer state.OutboundIntegrationOffer, data *dashboard.AIIntegrationData) {
	if !offer.AIRequestLogEnabled {
		data.LatencyNote = "Latency comes from the request log. Turn it on with gregale ai policy " + offer.ID + " --request-log on."
		return
	}
	stats, ok := s.Host.Store().(state.OutboundAICallStatsStore)
	if !ok {
		return
	}
	rows, calls, err := stats.GetOutboundAICallStats(r.Context(), acct.ID, offer.ID)
	if err != nil {
		data.LatencyNote = unavailable("latency", err)
		return
	}
	for _, row := range rows {
		data.Latency = append(data.Latency, dashboard.AILatencyRow{Model: row.Model, Calls: count(row.Calls),
			ErrorRate: percent(row.ErrorCalls, row.Calls), TTFBP50: milliseconds(row.TTFBP50MS), TTFBP95: milliseconds(row.TTFBP95MS),
			DurationP50: milliseconds(row.DurationP50MS), DurationP95: milliseconds(row.DurationP95MS)})
	}
	data.LatencyNote = "Over the latest " + count(calls) + " logged calls of the last 24 hours (at most " +
		count(api.OutboundAICallStatsCallsMax) + "), whatever the day above."
}

// alertViews fills the latest drift alerts.
func (s *Service) alertViews(r *http.Request, acct state.Account, integrationID string, data *dashboard.AIIntegrationData) {
	quality, ok := s.Host.Store().(state.OutboundAIQualityStore)
	if !ok {
		return
	}
	alerts, err := quality.ListOutboundAIQualityAlerts(r.Context(), acct.ID, integrationID, recentAlerts)
	if err != nil {
		s.log().Warn("dashboard ai: quality alerts unavailable", "account_id", acct.ID, "err", err)
		return
	}
	for _, alert := range alerts {
		data.Alerts = append(data.Alerts, dashboard.AIAlertRow{Day: alert.UsageDate, Model: alert.Model, Metric: metricName(alert.Metric),
			Current: qualityValue(alert.Metric, alert.CurrentValue), Baseline: qualityValue(alert.Metric, alert.BaselineValue),
			Counts: count(alert.CurrentCount) + " / " + count(alert.BaselineCount), Detected: timestamp(alert.DetectedAt)})
	}
}

// recentTraceViews fills the latest traces.
func (s *Service) recentTraceViews(r *http.Request, acct state.Account, offer state.OutboundIntegrationOffer, apps map[string]string,
	hidden map[string]bool, data *dashboard.AIIntegrationData) {
	traces, ok := s.Host.Store().(state.OutboundAITraceStore)
	if !ok {
		return
	}
	list, err := traces.ListOutboundAITraces(r.Context(), acct.ID, offer.ID, state.OutboundAITraceQuery{Limit: recentTraces})
	if err != nil {
		data.TracesNote = unavailable("trace list", err)
		return
	}
	for _, trace := range list {
		if !hidden[trace.Caller] {
			data.Traces = append(data.Traces, traceRow(offer.ID, trace, apps, offer.AITraceMaxRepeatedToolCalls))
		}
	}
	if len(data.Traces) == 0 {
		data.TracesNote = "No traces yet. Send X-Gregale-AI-Trace (or traceparent) with an agent's calls; traces are recorded " +
			"when the request log or trace limits are on."
	}
}

// traceRow is one trace in a list. A trace is looping when its streak of
// identical tool requests reached half its limit, or three without one.
func traceRow(integrationID string, trace state.OutboundAITrace, apps map[string]string, repeatLimit *int64) dashboard.AITraceRow {
	threshold := int64(3)
	if repeatLimit != nil {
		threshold = max(*repeatLimit/2, 2)
	}
	row := dashboard.AITraceRow{ID: trace.TraceID, Href: traceHref(integrationID, trace.TraceID), LastCall: timestamp(trace.LastAt),
		Calls: count(trace.Calls), Errors: count(trace.ErrorCalls), Cost: money(trace.CostMillicents), ToolCalls: count(trace.ToolCalls),
		Repeated: count(trace.RepeatedToolCalls), Models: joinOr(trace.Models, "—"), Caller: callerName(trace.Caller, apps),
		EndUser: trace.EndUser, Looping: trace.RepeatedToolCalls >= threshold}
	if row.EndUser == "" {
		row.EndUser = "—"
	}
	return row
}
