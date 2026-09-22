package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
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
		if headers != nil {
			item.Traceparent = headers["traceparent"]
		}
		result.Invocations = append(result.Invocations, item)
	}

	now := result.GeneratedAt
	retention := time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour
	seenSpans := make(map[string]struct{})
	for _, app := range apps {
		row, err := s.store.GetRequestTelemetryByAppAndIdentifier(r.Context(), sqlc.GetRequestTelemetryByAppAndIdentifierParams{
			AppID:         stringToPgUUID(app.ID),
			Identifier:    traceID,
			ReceivedFrom:  pgtype.Timestamptz{Time: now.Add(-retention), Valid: true},
			ReceivedUntil: pgtype.Timestamptz{Time: now, Valid: true},
		})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			result.Partial = true
			result.Errors = append(result.Errors, api.AccountTraceLookupError{App: app.Slug, Detail: "request telemetry unavailable"})
			continue
		}
		result.Matches = append(result.Matches, api.AccountTraceMatch{App: app.Slug, Request: debugTelemetryGetRowToItem(row)})
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
	sort.Slice(result.Matches, func(i, j int) bool { return result.Matches[i].App < result.Matches[j].App })
	sort.SliceStable(result.Spans, func(i, j int) bool {
		if result.Spans[i].DurationNanos != result.Spans[j].DurationNanos {
			return result.Spans[i].DurationNanos > result.Spans[j].DurationNanos
		}
		return result.Spans[i].SpanID < result.Spans[j].SpanID
	})

	if len(result.Matches) == 0 && len(result.Invocations) == 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "trace not found"))
		return
	}
	writeJSON(w, http.StatusOK, result)
}
