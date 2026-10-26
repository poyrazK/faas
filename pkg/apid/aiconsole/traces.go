package aiconsole

import (
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

// Timeline geometry, in SVG units.
const (
	timelineWidth = 1000
	rowHeight     = 14
	rowGap        = 4
	axisHeight    = 16
	timelineTicks = 4
)

// tracesPage is the trace explorer.
func (s *Service) tracesPage(w http.ResponseWriter, r *http.Request, acct state.Account, integrationID string) {
	ctx := r.Context()
	offer, found, err := s.integration(ctx, acct.ID, integrationID)
	if err == nil && !found {
		http.NotFound(w, r)
		return
	}
	query := r.URL.Query()
	data := dashboard.AITracesData{IntegrationName: offer.Name, IntegrationHref: integrationHref(integrationID),
		Action: tracesHref(integrationID), EndUser: query.Get("end_user")}
	page := dashboard.Page{Title: "Traces · " + offer.Name, Body: "ai_traces"}
	if err != nil {
		data.Unavailable = unavailable("integration", err)
		page.Data = data
		s.Host.Render(w, r, acct, page)
		return
	}
	apps, hidden := s.appNames(ctx, acct)
	appID := query.Get("app_id")
	for id, slug := range apps {
		data.Apps = append(data.Apps, dashboard.AIOption{Value: id, Label: slug, Selected: id == appID})
	}
	sort.Slice(data.Apps, func(i, j int) bool { return data.Apps[i].Label < data.Apps[j].Label })
	filter := state.OutboundAITraceQuery{AppID: appID, EndUser: data.EndUser, Limit: tracesPageSize}
	if raw := query.Get("before"); raw != "" {
		before, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			data.Unavailable = unavailable("trace list", state.ErrInvalidArgument)
			page.Data = data
			s.Host.Render(w, r, acct, page)
			return
		}
		filter.Before = &before
	}
	traces, ok := s.Host.Store().(state.OutboundAITraceStore)
	if !ok {
		data.Unavailable = unavailable("trace list", errStoreUnavailable)
		page.Data = data
		s.Host.Render(w, r, acct, page)
		return
	}
	list, err := traces.ListOutboundAITraces(ctx, acct.ID, integrationID, filter)
	if err != nil {
		data.Unavailable = unavailable("trace list", err)
		page.Data = data
		s.Host.Render(w, r, acct, page)
		return
	}
	for _, trace := range list {
		if !hidden[trace.Caller] {
			data.Traces = append(data.Traces, traceRow(integrationID, trace, apps, offer.AITraceMaxRepeatedToolCalls))
		}
	}
	if len(list) == tracesPageSize {
		older := url.Values{"before": {list[len(list)-1].LastAt.UTC().Format(time.RFC3339Nano)}}
		if appID != "" {
			older.Set("app_id", appID)
		}
		if data.EndUser != "" {
			older.Set("end_user", data.EndUser)
		}
		data.OlderHref = tracesHref(integrationID) + "?" + older.Encode()
	}
	if !offer.AIRequestLogEnabled && offer.AITraceMaxCalls == nil && offer.AITraceMaxCostMillicents == nil &&
		offer.AITraceMaxRepeatedToolCalls == nil {
		data.Note = "Traces are recorded while the request log or trace limits are on; this integration has neither."
	}
	page.Data = data
	s.Host.Render(w, r, acct, page)
}

// tracePage is one trace's timeline.
func (s *Service) tracePage(w http.ResponseWriter, r *http.Request, acct state.Account, integrationID, traceID string) {
	ctx := r.Context()
	offer, found, err := s.integration(ctx, acct.ID, integrationID)
	if err == nil && !found {
		http.NotFound(w, r)
		return
	}
	data := dashboard.AITraceData{IntegrationName: offer.Name, IntegrationHref: integrationHref(integrationID),
		TracesHref: tracesHref(integrationID)}
	page := dashboard.Page{Title: "Trace " + traceID + " · " + offer.Name, Body: "ai_trace"}
	traces, ok := s.Host.Store().(state.OutboundAITraceStore)
	if err != nil || !ok {
		data.Unavailable = unavailable("trace", errors.Join(err, errStoreUnavailable))
		page.Data = data
		s.Host.Render(w, r, acct, page)
		return
	}
	detail, err := traces.GetOutboundAITrace(ctx, acct.ID, integrationID, traceID)
	if errors.Is(err, state.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		data.Unavailable = unavailable("trace", err)
		page.Data = data
		s.Host.Render(w, r, acct, page)
		return
	}
	apps, hidden := s.appNames(ctx, acct)
	if hidden[detail.Trace.Caller] {
		http.NotFound(w, r)
		return
	}
	calls := detail.Calls[:0:0]
	for _, call := range detail.Calls {
		if !hidden[call.Caller] {
			calls = append(calls, call)
		}
	}
	detail.Calls = calls
	data.Trace = traceRow(integrationID, detail.Trace, apps, offer.AITraceMaxRepeatedToolCalls)
	data.FirstCall = timestamp(detail.Trace.FirstAt)
	data.Span = duration(max(detail.Trace.LastAt.Sub(detail.Trace.FirstAt).Milliseconds(), 0))
	data.Timeline = timeline(detail.Calls)
	for index, call := range detail.Calls {
		data.Calls = append(data.Calls, callRow(index+1, call, apps))
	}
	switch {
	case len(detail.Calls) == 0:
		data.Note = "No logged calls: the trace's totals are recorded, but each call shows here only while the request log is on."
	case detail.CallsTruncated:
		data.Note = "Showing the first " + count(int64(len(detail.Calls))) + " logged calls."
	}
	page.Data = data
	s.Host.Render(w, r, acct, page)
}

// callStart is when a logged call began: the log records its end and its
// duration.
func callStart(call state.OutboundAIRequestLogEntry) time.Time {
	return call.CreatedAt.Add(-time.Duration(call.DurationMS) * time.Millisecond)
}

// timeline lays the calls out as bars from start to end, one row each, on
// a shared time axis.
func timeline(calls []state.OutboundAIRequestLogEntry) dashboard.AITimeline {
	if len(calls) == 0 {
		return dashboard.AITimeline{}
	}
	first, last := callStart(calls[0]), calls[0].CreatedAt
	for _, call := range calls {
		if start := callStart(call); start.Before(first) {
			first = start
		}
		if call.CreatedAt.After(last) {
			last = call.CreatedAt
		}
	}
	span := max(last.Sub(first), time.Millisecond)
	position := func(at time.Time) int {
		return int(float64(at.Sub(first)) / float64(span) * timelineWidth)
	}
	view := dashboard.AITimeline{Width: timelineWidth, Height: len(calls)*(rowHeight+rowGap) + axisHeight}
	for index, call := range calls {
		y := index * (rowHeight + rowGap)
		x := position(callStart(call))
		bar := dashboard.AITimelineBar{X: x, Y: y, W: max(position(call.CreatedAt)-x, 2), H: rowHeight, LabelY: y + rowHeight,
			Error: call.Outcome == "error" || call.Status >= 400, Flagged: len(call.QualityFlags) > 0,
			Label: "#" + strconv.Itoa(index+1) + " " + call.Model + " · " + duration(call.DurationMS)}
		if call.TTFBMS != nil {
			bar.HasTTFB = true
			bar.TTFBX = position(callStart(call).Add(time.Duration(*call.TTFBMS) * time.Millisecond))
		}
		view.Bars = append(view.Bars, bar)
	}
	for tick := 0; tick <= timelineTicks; tick++ {
		view.Ticks = append(view.Ticks, dashboard.AITimelineTick{X: timelineWidth * tick / timelineTicks,
			Label: duration(span.Milliseconds() * int64(tick) / timelineTicks)})
	}
	return view
}

// callRow is one call in the trace's table.
func callRow(number int, call state.OutboundAIRequestLogEntry, apps map[string]string) dashboard.AITraceCallRow {
	row := dashboard.AITraceCallRow{Number: strconv.Itoa(number), Started: timestamp(callStart(call)), Model: call.Model,
		Status: strconv.Itoa(call.Status), Tokens: count(call.InputTokens) + " / " + count(call.OutputTokens),
		Cost: money(call.CostMillicents), Duration: duration(call.DurationMS), TTFB: milliseconds(call.TTFBMS),
		Tools: joinOr(call.ToolCalls, "—"), Flags: joinOr(call.QualityFlags, "—"), Feedback: "—", Score: "—",
		Caller: callerName(call.Caller, apps), Error: call.Outcome == "error" || call.Status >= 400}
	if call.Feedback != "" {
		row.Feedback = call.Feedback
		if call.FeedbackLabel != "" {
			row.Feedback += " (" + call.FeedbackLabel + ")"
		}
	}
	if call.EvaluationScore != nil {
		row.Score = strconv.Itoa(int(*call.EvaluationScore))
	}
	return row
}

func joinOr(values []string, empty string) string {
	if len(values) == 0 {
		return empty
	}
	return strings.Join(values, ", ")
}
