package main

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var accountTraceIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// accountTraceLookup is the durable tenant-scoped read behind `gregale trace`.
// Request evidence remains retention-bound per app; durable invocation rows
// are joined from the indexed invocation envelope so a trace can be followed
// even when no request-telemetry row was retained for one of the hops.
func (s *server) accountTraceLookup(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}
	traceID := r.PathValue("trace_id")
	if !accountTraceIDPattern.MatchString(traceID) || traceID == "00000000000000000000000000000000" {
		api.WriteProblem(w, api.ErrValidation("trace id must be 32 lowercase hexadecimal characters"))
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 500 {
			api.WriteProblem(w, api.ErrValidation("limit must be an integer between 1 and 500"))
			return
		}
		limit = n
	}

	apps, err := s.store.ListApps(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("list apps for trace lookup"))
		return
	}
	appSlugs := make(map[string]string, len(apps))
	for _, app := range apps {
		appSlugs[app.ID] = app.Slug
	}

	invocationRows, err := s.store.ListInvocationsByTraceID(r.Context(), acct.ID, traceID, limit)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("list trace invocations"))
		return
	}
	result := api.AccountTraceLookupResponse{
		TraceID:     traceID,
		GeneratedAt: time.Now().UTC(),
		Limit:       limit,
		Matches:     make([]api.AccountTraceMatch, 0),
		Invocations: make([]api.AccountTraceInvocation, 0, len(invocationRows)),
		Logs:        make([]api.LogQueryEvent, 0),
		Spans:       make([]api.DebugTelemetrySpan, 0),
		Errors:      make([]api.AccountTraceLookupError, 0),
	}
	for _, inv := range invocationRows {
		app, ok := appSlugs[inv.AppID]
		if !ok {
			continue
		}
		var headers map[string]string
		if len(inv.Headers) > 0 {
			_ = json.Unmarshal(inv.Headers, &headers)
		}
		item := api.AccountTraceInvocation{
			App:       app,
			ID:        inv.ID,
			Source:    string(inv.Source),
			QueueName: inv.QueueName,
			State:     string(inv.State),
			Attempts:  inv.Attempts,
			CreatedAt: inv.CreatedAt.UTC().Format(time.RFC3339Nano),
		}
		if inv.CompletedAt != nil {
			item.CompletedAt = inv.CompletedAt.UTC().Format(time.RFC3339Nano)
		}
		if inv.ReceivedAt != nil {
			item.StartedAt = inv.ReceivedAt.UTC().Format(time.RFC3339Nano)
		}
		if headers != nil {
			item.Traceparent = headers["traceparent"]
		}
		result.Invocations = append(result.Invocations, item)
	}

	now := result.GeneratedAt
	retention := time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour
	logLimit := limit
	if logLimit > state.MaxLogEventPage {
		logLimit = state.MaxLogEventPage
	}
	s.addAccountTraceEvidence(r.Context(), acct.ID, apps, traceID, now.Add(-retention), now, logLimit, &result)
	sort.SliceStable(result.Logs, func(i, j int) bool {
		left, leftErr := time.Parse(time.RFC3339Nano, result.Logs[i].Timestamp)
		right, rightErr := time.Parse(time.RFC3339Nano, result.Logs[j].Timestamp)
		if leftErr == nil && rightErr == nil && !left.Equal(right) {
			return left.After(right)
		}
		return result.Logs[i].ID > result.Logs[j].ID
	})
	if len(result.Logs) > logLimit {
		result.LogsTruncated = true
		result.Logs = result.Logs[:logLimit]
	}
	sort.Slice(result.Matches, func(i, j int) bool { return result.Matches[i].App < result.Matches[j].App })
	sort.SliceStable(result.Spans, func(i, j int) bool {
		if result.Spans[i].DurationNanos != result.Spans[j].DurationNanos {
			return result.Spans[i].DurationNanos > result.Spans[j].DurationNanos
		}
		return result.Spans[i].SpanID < result.Spans[j].SpanID
	})

	if len(result.Matches) == 0 && len(result.Invocations) == 0 && len(result.Logs) == 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "trace not found"))
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// addAccountTraceEvidence attaches the retained HTTP logs and request
// telemetry for traceID across every app of the account, one account-scoped
// read each. production-us hunt #7: the per-app loop this replaces cost two
// round trips per app; at 71 apps it ran past apid's 5 s request budget and
// every app after the deadline came back "unavailable", so `gregale trace`
// and `gregale logs --trace` exited partial.
func (s *server) addAccountTraceEvidence(ctx context.Context, accountID string, apps []state.App, traceID string, since, until time.Time, logLimit int, result *api.AccountTraceLookupResponse) {
	appSlugs := make(map[string]string, len(apps))
	appIDs := make([]string, 0, len(apps))
	for _, app := range apps {
		appSlugs[app.ID] = app.Slug
		appIDs = append(appIDs, app.ID)
	}
	logEvents, hasMoreLogs, err := s.store.ListAccountTraceLogEvents(ctx, state.AccountTraceLogFilter{
		AccountID: accountID, AppIDs: appIDs, TraceID: traceID,
		Source: state.LogEventSourceHTTP, Since: since, Until: until, Limit: logLimit,
	})
	if err != nil {
		result.Partial = true
		result.Errors = append(result.Errors, api.AccountTraceLookupError{Detail: "HTTP log events unavailable"})
	} else {
		result.LogsTruncated = result.LogsTruncated || hasMoreLogs
		for _, event := range logEvents {
			result.Logs = append(result.Logs, accountTraceLogQueryEvent(appSlugs[event.AppID], event))
		}
	}

	rows, err := s.store.ListRequestTelemetryByAccountTrace(ctx, sqlc.ListRequestTelemetryByAccountTraceParams{
		AccountID:     stringToPgUUID(accountID),
		TraceID:       traceID,
		ReceivedFrom:  pgtype.Timestamptz{Time: since, Valid: true},
		ReceivedUntil: pgtype.Timestamptz{Time: until, Valid: true},
	})
	if err != nil {
		result.Partial = true
		result.Errors = append(result.Errors, api.AccountTraceLookupError{Detail: "request telemetry unavailable"})
		return
	}
	seenSpans := make(map[string]struct{})
	for _, row := range rows {
		slug, ok := appSlugs[uuidFromPg(row.AppID)]
		if !ok {
			continue // retained rows of a deleted app stay out of the trace
		}
		result.Matches = append(result.Matches, api.AccountTraceMatch{App: slug, Request: debugTelemetryGetRowToItem(accountTraceTelemetryRow(row))})
		spans, truncated := parseDebugEvidenceSpans(row.SpansSummary)
		result.SpansTruncated = result.SpansTruncated || truncated
		for _, span := range spans {
			if span.SpanID != "" {
				if _, exists := seenSpans[span.SpanID]; exists {
					continue
				}
				seenSpans[span.SpanID] = struct{}{}
			}
			result.Spans = append(result.Spans, span)
		}
	}
}

// accountTraceTelemetryRow reuses the per-app debugger projection for an
// account-wide trace row; the two queries select the same columns.
func accountTraceTelemetryRow(row sqlc.ListRequestTelemetryByAccountTraceRow) sqlc.GetRequestTelemetryByAppAndIdentifierRow {
	return sqlc.GetRequestTelemetryByAppAndIdentifierRow{
		ID: row.ID, DeploymentID: row.DeploymentID, Route: row.Route, Method: row.Method,
		Status: row.Status, LatencyMs: row.LatencyMs, Count: row.Count, ColdBoot: row.ColdBoot,
		TraceID: row.TraceID, ReceivedAt: row.ReceivedAt, SpansSummary: row.SpansSummary,
		WakeID: row.WakeID, InstanceID: row.InstanceID, GuestDurationMs: row.GuestDurationMs,
		GuestRuntime: row.GuestRuntime, GuestOutcome: row.GuestOutcome, GuestErrorClass: row.GuestErrorClass,
		ConsumerID: row.ConsumerID, NodeID: row.NodeID, Region: row.Region, CommitSha: row.CommitSha,
		DeploymentTag: row.DeploymentTag, DeploymentCreatedAt: row.DeploymentCreatedAt, ImageDigest: row.ImageDigest,
	}
}

func accountTraceLogQueryEvent(app string, event state.LogEvent) api.LogQueryEvent {
	latencyMS := 0
	if event.LatencyMS != nil {
		latencyMS = *event.LatencyMS
	}
	return api.LogQueryEvent{
		ID:           event.ID,
		App:          app,
		Timestamp:    event.OccurredAt.UTC().Format(time.RFC3339Nano),
		Source:       api.LogSource(event.Source),
		DeploymentID: event.DeploymentID,
		InstanceID:   event.InstanceID,
		RequestID:    event.RequestID,
		TraceID:      event.TraceID,
		Route:        event.Route,
		Method:       event.Method,
		Status:       event.Status,
		Level:        event.Level,
		Stream:       event.Stream,
		Message:      event.Message,
		LatencyMS:    latencyMS,
		Count:        event.Occurrences,
		ColdBoot:     event.ColdBoot,
	}
}
