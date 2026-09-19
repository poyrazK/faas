package main

// handlers_debug_telemetry.go — customer-facing production debugger
// surfaces (ADR-127). Request telemetry is written by the publisher → gRPC
// receiver path; replay is the one write-side consumer operation and queues
// a metadata-only invocation through the configured mirror rule.
//
// Handlers: list recent requests, retrieve one request by id, and return
// bounded evidence for a request. Regression / compare / replay are the
// adjacent debugger consumer surfaces.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/debugger"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// debugTelemetryListHandler — GET /v1/apps/{slug}/debug/requests
//
// Plan-gated by DebugTelemetryEnabled. `since` is clamped to the
// plan's DebugTelemetryRetentionDays (Hobby 3d, Pro 7d, Scale 14d).
// `limit` defaults to 20, capped at 200 (matches
// handlers_invocations.go:451-455). The endpoint is IDOR-safe via
// loadApp (cross-account slug → 404). Cursor pages carry the original
// window and every filter so the ordering remains stable while new rows
// arrive.
func (s *server) debugTelemetryListHandler(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}
	// since is a duration string ("3h", "24h", "7d"); we clamp to
	// the plan's retention cap so a Free user passing ?since=90d
	// is silently rounded down to DebugTelemetryRetentionDays.
	// The raw form is captured separately so the response can
	// echo it verbatim — round-trip safe (see echoDebugSince).
	sinceRaw := r.URL.Query().Get("since")
	sinceDur := parseDebugSinceFromString(sinceRaw, 24*time.Hour)
	cap := time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour
	retentionClamped := false
	if cap > 0 && sinceDur > cap {
		sinceDur = cap
		retentionClamped = true
	}
	limit := 20
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= 200 {
			limit = n
		} else {
			api.WriteProblem(w, api.ErrValidation("limit must be an integer between 1 and 200"))
			return
		}
	}
	route := r.URL.Query().Get("route")
	if len(route) > 256 {
		api.WriteProblem(w, api.ErrValidation("route must be at most 256 characters"))
		return
	}
	filters, err := parseDebugTelemetryFilters(r.URL.Query())
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}

	cursorRaw := strings.TrimSpace(r.URL.Query().Get("cursor"))
	if len(cursorRaw) > 8192 {
		api.WriteProblem(w, api.ErrValidation("cursor must be at most 8192 characters"))
		return
	}
	decodedCursor, err := decodeDebugTelemetryCursor(cursorRaw)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("cursor is invalid; restart the request list"))
		return
	}
	if decodedCursor.Version != 0 && (decodedCursor.AppID != app.ID || decodedCursor.Route != route || !filters.same(decodedCursor.filters())) {
		api.WriteProblem(w, api.ErrValidation("cursor does not match this app or filters"))
		return
	}

	now := time.Now().UTC()
	windowStart := now.Add(-sinceDur)
	windowEnd := now
	if decodedCursor.Version != 0 {
		// A cursor pins both bounds. Reject an old cursor after the plan's
		// retention horizon rather than allowing it to read aged-out rows.
		if decodedCursor.WindowEnd.After(now.Add(time.Minute)) || decodedCursor.WindowStart.Before(now.Add(-cap)) {
			api.WriteProblem(w, api.ErrValidation("cursor has expired; restart the request list"))
			return
		}
		if sinceRaw != "" && sinceDur != decodedCursor.WindowEnd.Sub(decodedCursor.WindowStart) {
			api.WriteProblem(w, api.ErrValidation("cursor does not match since; restart the request list"))
			return
		}
		windowStart = decodedCursor.WindowStart.UTC()
		windowEnd = decodedCursor.WindowEnd.UTC()
		sinceDur = windowEnd.Sub(windowStart)
		retentionClamped = decodedCursor.RetentionClamped || retentionClamped
	}
	cursorReceivedAt, cursorID := debugTelemetryCursorParams(decodedCursor)
	rows, err := s.store.ListRequestTelemetryByApp(r.Context(), sqlc.ListRequestTelemetryByAppParams{
		AppID:             stringToPgUUID(app.ID),
		ReceivedAt:        pgtype.Timestamptz{Time: windowStart, Valid: true},
		ReceivedAt_2:      pgtype.Timestamptz{Time: windowEnd, Valid: true},
		CursorReceivedAt:  cursorReceivedAt,
		CursorID:          cursorID,
		Route:             route,
		DeploymentID:      filters.DeploymentID,
		StatusFilter:      int32(filters.Status),
		ColdBootFilter:    filters.sqlColdBootFilter(),
		ConsumerID:        filters.sqlConsumerID(),
		ConsumerAnonymous: filters.sqlConsumerAnonymous(),
		MinLatencyMs:      int32(filters.MinLatencyMS),
		Limit:             int32(limit + 1),
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("list request telemetry"))
		return
	}
	if rows == nil {
		rows = []sqlc.ListRequestTelemetryByAppRow{}
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	items := make([]api.DebugTelemetryRequestItem, len(rows))
	for i, row := range rows {
		items[i] = debugTelemetryRowToItem(row)
	}
	nextCursor := ""
	if hasMore && len(rows) > 0 {
		nextCursor = encodeDebugTelemetryCursorWithFilters(app.ID, route, filters.cursor(), windowStart, windowEnd, retentionClamped, rows[len(rows)-1])
	}
	writeJSON(w, http.StatusOK, api.DebugTelemetryListResponse{
		Requests:         items,
		Since:            echoDebugSince(sinceRaw, sinceDur),
		WindowStart:      windowStart.Format(time.RFC3339Nano),
		WindowEnd:        windowEnd.Format(time.RFC3339Nano),
		RetentionClamped: retentionClamped,
		Complete:         !hasMore || nextCursor == "",
		NextCursor:       nextCursor,
		Filters:          filters.response(),
	})
}

// debugTelemetryCoverageHandler — GET /v1/apps/{slug}/debug/coverage
//
// Returns a bounded, weighted view of which debugger signals are present in
// the requested retention window. This is deliberately an observed-coverage
// endpoint: the durable table cannot tell us how many requests were dropped
// before persistence, so the response never presents represented_requests as
// a platform-wide capture denominator.
func (s *server) debugTelemetryCoverageHandler(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}

	sinceRaw := strings.TrimSpace(r.URL.Query().Get("since"))
	since, err := parseDebugSinceStrict(sinceRaw, 24*time.Hour)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	retention := time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour
	if retention > 0 && since > retention {
		since = retention
	}
	now := time.Now().UTC()
	from := now.Add(-since)
	row, err := s.store.RequestTelemetryCoverage(r.Context(), sqlc.RequestTelemetryCoverageParams{
		AppID:        stringToPgUUID(app.ID),
		AccountID:    stringToPgUUID(acct.ID),
		ReceivedAt:   pgtype.Timestamptz{Time: from, Valid: true},
		ReceivedAt_2: pgtype.Timestamptz{Time: now, Valid: true},
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("get debug coverage"))
		return
	}

	writeJSON(w, http.StatusOK, api.DebugCoverageResponse{
		AppID:               app.ID,
		Since:               echoDebugSince(sinceRaw, since),
		WindowStart:         from.Format(time.RFC3339Nano),
		WindowEnd:           now.Format(time.RFC3339Nano),
		PlanRetentionDays:   limits.DebugTelemetryRetentionDays,
		TelemetryRows:       row.TelemetryRows,
		RepresentedRequests: row.RepresentedRequests,
		ErrorRequests:       row.ErrorRequests,
		TraceLinked:         debugCoverageSignal(row.TraceLinkedRows, row.TraceLinkedRequests, row.RepresentedRequests),
		SpanEvidence:        debugCoverageSignal(row.SpanEvidenceRows, row.SpanEvidenceRequests, row.RepresentedRequests),
		WakeEvidence:        debugCoverageSignal(row.WakeEvidenceRows, row.WakeEvidenceRequests, row.RepresentedRequests),
		GuestEvidence:       debugCoverageSignal(row.GuestEvidenceRows, row.GuestEvidenceRequests, row.RepresentedRequests),
		OldestTelemetryAt:   debugCoverageTimestamp(row.OldestTelemetryAt),
		LatestTelemetryAt:   debugCoverageTimestamp(row.LatestTelemetryAt),
	})
}

const (
	debugDependencyHistoryMaxRows     = 2000
	debugDependencyHistoryMaxGroups   = 256
	debugDependencyHistoryMaxOutput   = 50
	debugDependencyHistoryMinCalls    = int64(5)
	debugDependencyRegressionFactor   = 1.5
	debugDependencyRegressionDeltaMS  = int64(25)
	debugCriticalPathHistoryMaxGroups = 128
	debugCriticalPathHistoryMaxOutput = 25
)

// debugDependencyLatencyHandler — GET /v1/apps/{slug}/debug/dependencies.
//
// The database read is deliberately capped before JSON parsing. Span
// summaries are already redacted at write time; this handler applies the same
// allowlist used by the per-request evidence endpoint, then computes bounded
// weighted percentiles. The two halves of the selected window provide a
// small, explainable regression signal without persisting another time series.
func (s *server) debugDependencyLatencyHandler(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}

	sinceRaw := strings.TrimSpace(r.URL.Query().Get("since"))
	since, err := parseDebugSinceStrict(sinceRaw, 24*time.Hour)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	retention := time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour
	retentionClamped := false
	if retention > 0 && since > retention {
		since = retention
		retentionClamped = true
	}
	now := time.Now().UTC()
	windowStart := now.Add(-since)
	rows, err := s.store.ListRequestTelemetryDependencySpans(r.Context(), sqlc.ListRequestTelemetryDependencySpansParams{
		AppID:        stringToPgUUID(app.ID),
		AccountID:    stringToPgUUID(acct.ID),
		ReceivedAt:   pgtype.Timestamptz{Time: windowStart, Valid: true},
		ReceivedAt_2: pgtype.Timestamptz{Time: now, Valid: true},
		Limit:        debugDependencyHistoryMaxRows + 1,
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("get debug dependency latency"))
		return
	}
	truncated := len(rows) > debugDependencyHistoryMaxRows
	if truncated {
		rows = rows[:debugDependencyHistoryMaxRows]
	}
	dependencies, aggregationTruncated, representedRequests, spanSamples := buildDebugDependencyLatencyHistory(rows, windowStart, now)
	truncated = truncated || aggregationTruncated
	writeJSON(w, http.StatusOK, api.DebugDependencyLatencyResponse{
		AppID:               app.ID,
		Since:               echoDebugSince(sinceRaw, since),
		WindowStart:         windowStart.Format(time.RFC3339Nano),
		WindowEnd:           now.Format(time.RFC3339Nano),
		RetentionClamped:    retentionClamped,
		Complete:            !truncated,
		Truncated:           truncated,
		TelemetryRows:       int64(len(rows)),
		RepresentedRequests: representedRequests,
		SpanSamples:         spanSamples,
		Dependencies:        dependencies,
	})
}

// debugCriticalPathHistoryHandler — GET /v1/apps/{slug}/debug/critical-paths.
//
// This intentionally reads the same bounded, redacted span summaries as the
// dependency history endpoint. A path is a stable sequence of allowlisted
// span identities; no raw span attributes or destinations cross the API.
func (s *server) debugCriticalPathHistoryHandler(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}

	sinceRaw := strings.TrimSpace(r.URL.Query().Get("since"))
	since, err := parseDebugSinceStrict(sinceRaw, 24*time.Hour)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	retention := time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour
	retentionClamped := false
	if retention > 0 && since > retention {
		since = retention
		retentionClamped = true
	}
	now := time.Now().UTC()
	windowStart := now.Add(-since)
	rows, err := s.store.ListRequestTelemetryDependencySpans(r.Context(), sqlc.ListRequestTelemetryDependencySpansParams{
		AppID:        stringToPgUUID(app.ID),
		AccountID:    stringToPgUUID(acct.ID),
		ReceivedAt:   pgtype.Timestamptz{Time: windowStart, Valid: true},
		ReceivedAt_2: pgtype.Timestamptz{Time: now, Valid: true},
		Limit:        debugDependencyHistoryMaxRows + 1,
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("get debug critical path history"))
		return
	}
	truncated := len(rows) > debugDependencyHistoryMaxRows
	if truncated {
		rows = rows[:debugDependencyHistoryMaxRows]
	}
	paths, pathsTruncated, complete, representedRequests, pathSamples := buildDebugCriticalPathHistory(rows, windowStart, now)
	truncated = truncated || pathsTruncated
	writeJSON(w, http.StatusOK, api.DebugCriticalPathHistoryResponse{
		AppID:               app.ID,
		Since:               echoDebugSince(sinceRaw, since),
		WindowStart:         windowStart.Format(time.RFC3339Nano),
		WindowEnd:           now.Format(time.RFC3339Nano),
		RetentionClamped:    retentionClamped,
		Complete:            complete && !truncated,
		Truncated:           truncated,
		TelemetryRows:       int64(len(rows)),
		RepresentedRequests: representedRequests,
		PathSamples:         pathSamples,
		CriticalPaths:       paths,
	})
}

type debugCriticalPathHistorySample = debugDependencyHistorySample

type debugCriticalPathHistoryAggregate struct {
	signature      string
	segments       []api.DebugCriticalPathSegment
	all            []debugCriticalPathHistorySample
	baseline       []debugCriticalPathHistorySample
	current        []debugCriticalPathHistorySample
	calls          int64
	errors         int64
	baselineCalls  int64
	currentCalls   int64
	baselineErrors int64
	currentErrors  int64
}

func buildDebugCriticalPathHistory(rows []sqlc.ListRequestTelemetryDependencySpansRow, windowStart, windowEnd time.Time) ([]api.DebugCriticalPathHistoryItem, bool, bool, int64, int64) {
	cutover := windowStart.Add(windowEnd.Sub(windowStart) / 2)
	aggregates := make(map[string]*debugCriticalPathHistoryAggregate)
	truncated := false
	complete := true
	var representedRequests int64
	var pathSamples int64
	for _, row := range rows {
		weight := int64(row.Count)
		if weight < 1 {
			weight = 1
		}
		representedRequests += weight
		spans, spanTruncated := parseDebugEvidenceSpans(row.SpansSummary)
		if spanTruncated {
			complete = false
		}
		if len(row.SpansSummary) == 0 || len(spans) == 0 {
			complete = false
			continue
		}
		path := buildDebugCriticalPath(spans)
		if path == nil {
			complete = false
			continue
		}
		if !path.Complete {
			complete = false
		}
		pathSamples++
		signature, segments := debugCriticalPathIdentity(path)
		if signature == "" {
			complete = false
			continue
		}
		aggregate := aggregates[signature]
		if aggregate == nil {
			if len(aggregates) >= debugCriticalPathHistoryMaxGroups {
				truncated = true
				continue
			}
			aggregate = &debugCriticalPathHistoryAggregate{
				signature: signature,
				segments:  segments,
			}
			aggregates[signature] = aggregate
		}
		sample := debugCriticalPathHistorySample{
			durationNanos: debugCriticalPathDurationNanos(path.DurationMS),
			weight:        weight,
			isError:       debugCriticalPathHasError(path),
		}
		aggregate.all = append(aggregate.all, sample)
		aggregate.calls += weight
		if sample.isError {
			aggregate.errors += weight
		}
		isCurrent := row.ReceivedAt.Valid && !row.ReceivedAt.Time.Before(cutover)
		if isCurrent {
			aggregate.current = append(aggregate.current, sample)
			aggregate.currentCalls += weight
			if sample.isError {
				aggregate.currentErrors += weight
			}
		} else {
			aggregate.baseline = append(aggregate.baseline, sample)
			aggregate.baselineCalls += weight
			if sample.isError {
				aggregate.baselineErrors += weight
			}
		}
	}

	ordered := make([]*debugCriticalPathHistoryAggregate, 0, len(aggregates))
	for _, aggregate := range aggregates {
		ordered = append(ordered, aggregate)
	}
	out := make([]api.DebugCriticalPathHistoryItem, 0, len(ordered))
	for _, aggregate := range ordered {
		baselineP95 := debugWeightedDependencyPercentile(aggregate.baseline, 0.95)
		currentP95 := debugWeightedDependencyPercentile(aggregate.current, 0.95)
		p95Delta := currentP95 - baselineP95
		factor := float64(0)
		if baselineP95 > 0 {
			factor = roundDebugDependencyFactor(float64(currentP95) / float64(baselineP95))
		}
		regression := aggregate.baselineCalls >= debugDependencyHistoryMinCalls &&
			aggregate.currentCalls >= debugDependencyHistoryMinCalls &&
			baselineP95 > 0 && currentP95 > 0 &&
			factor >= debugDependencyRegressionFactor &&
			p95Delta >= debugDependencyRegressionDeltaMS
		baselineErrorRate := debugDependencyErrorRate(aggregate.baselineErrors, aggregate.baselineCalls)
		currentErrorRate := debugDependencyErrorRate(aggregate.currentErrors, aggregate.currentCalls)
		out = append(out, api.DebugCriticalPathHistoryItem{
			Signature:            aggregate.signature,
			Segments:             aggregate.segments,
			Calls:                aggregate.calls,
			ErrorCalls:           aggregate.errors,
			ErrorRatePct:         debugDependencyErrorRate(aggregate.errors, aggregate.calls),
			P50MS:                debugWeightedDependencyPercentile(aggregate.all, 0.50),
			P95MS:                debugWeightedDependencyPercentile(aggregate.all, 0.95),
			P99MS:                debugWeightedDependencyPercentile(aggregate.all, 0.99),
			BaselineCalls:        aggregate.baselineCalls,
			CurrentCalls:         aggregate.currentCalls,
			BaselineP95MS:        baselineP95,
			CurrentP95MS:         currentP95,
			P95DeltaMS:           p95Delta,
			RegressionFactor:     factor,
			Regression:           regression,
			BaselineErrorRatePct: baselineErrorRate,
			CurrentErrorRatePct:  currentErrorRate,
			ErrorRateDeltaPct:    currentErrorRate - baselineErrorRate,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Regression != out[j].Regression {
			return out[i].Regression
		}
		if out[i].CurrentP95MS != out[j].CurrentP95MS {
			return out[i].CurrentP95MS > out[j].CurrentP95MS
		}
		if out[i].Calls != out[j].Calls {
			return out[i].Calls > out[j].Calls
		}
		return out[i].Signature < out[j].Signature
	})
	if len(out) > debugCriticalPathHistoryMaxOutput {
		out = out[:debugCriticalPathHistoryMaxOutput]
		truncated = true
	}
	return out, truncated, complete, representedRequests, pathSamples
}

func debugCriticalPathIdentity(path *api.DebugRequestCriticalPath) (string, []api.DebugCriticalPathSegment) {
	if path == nil || len(path.Spans) == 0 {
		return "", nil
	}
	segments := make([]api.DebugCriticalPathSegment, 0, len(path.Spans))
	parts := make([]string, 0, len(path.Spans))
	for _, span := range path.Spans {
		dependencyType := span.DependencyType
		if dependencyType == "" {
			dependencyType = "application"
		}
		name := span.Name
		if name == "" {
			name = "<unnamed>"
		}
		segment := api.DebugCriticalPathSegment{Type: dependencyType, Kind: span.DependencyKind, Name: name}
		segments = append(segments, segment)
		parts = append(parts, dependencyType+"/"+span.DependencyKind+"/"+name)
	}
	return strings.Join(parts, " > "), segments
}

func debugCriticalPathHasError(path *api.DebugRequestCriticalPath) bool {
	for _, span := range path.Spans {
		if strings.EqualFold(span.Status, "error") {
			return true
		}
	}
	return false
}

func maxInt64(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func debugCriticalPathDurationNanos(durationMS int64) uint64 {
	durationMS = maxInt64(durationMS)
	maxDurationMS := int64((24 * time.Hour) / time.Millisecond)
	if durationMS > maxDurationMS {
		durationMS = maxDurationMS
	}
	return uint64(durationMS) * uint64(time.Millisecond)
}

type debugDependencyHistorySample struct {
	durationNanos uint64
	weight        int64
	isError       bool
}

type debugDependencyHistoryAggregate struct {
	dependencyType string
	dependencyKind string
	name           string
	all            []debugDependencyHistorySample
	baseline       []debugDependencyHistorySample
	current        []debugDependencyHistorySample
	calls          int64
	errors         int64
	baselineCalls  int64
	currentCalls   int64
	baselineErrors int64
	currentErrors  int64
}

func buildDebugDependencyLatencyHistory(rows []sqlc.ListRequestTelemetryDependencySpansRow, windowStart, windowEnd time.Time) ([]api.DebugDependencyLatencyItem, bool, int64, int64) {
	cutover := windowStart.Add(windowEnd.Sub(windowStart) / 2)
	aggregates := make(map[string]*debugDependencyHistoryAggregate)
	truncated := false
	var representedRequests int64
	var spanSamples int64
	for _, row := range rows {
		weight := int64(row.Count)
		if weight < 1 {
			weight = 1
		}
		representedRequests += weight
		spans, spanTruncated := parseDebugEvidenceSpans(row.SpansSummary)
		truncated = truncated || spanTruncated
		spanSamples += int64(len(spans))
		isCurrent := row.ReceivedAt.Valid && !row.ReceivedAt.Time.Before(cutover)
		for _, span := range spans {
			dependencyType := span.DependencyType
			if dependencyType == "" {
				dependencyType = "application"
			}
			name := span.Name
			if name == "" {
				name = "<unnamed>"
			}
			key := dependencyType + "\x00" + span.DependencyKind + "\x00" + name
			aggregate := aggregates[key]
			if aggregate == nil {
				if len(aggregates) >= debugDependencyHistoryMaxGroups {
					truncated = true
					continue
				}
				aggregate = &debugDependencyHistoryAggregate{
					dependencyType: dependencyType,
					dependencyKind: span.DependencyKind,
					name:           name,
				}
				aggregates[key] = aggregate
			}
			sample := debugDependencyHistorySample{
				durationNanos: minDebugDependencyDuration(span.DurationNanos),
				weight:        weight,
				isError:       strings.EqualFold(span.Status, "error"),
			}
			aggregate.all = append(aggregate.all, sample)
			aggregate.calls += weight
			if sample.isError {
				aggregate.errors += weight
			}
			if isCurrent {
				aggregate.current = append(aggregate.current, sample)
				aggregate.currentCalls += weight
				if sample.isError {
					aggregate.currentErrors += weight
				}
			} else {
				aggregate.baseline = append(aggregate.baseline, sample)
				aggregate.baselineCalls += weight
				if sample.isError {
					aggregate.baselineErrors += weight
				}
			}
		}
	}

	ordered := make([]*debugDependencyHistoryAggregate, 0, len(aggregates))
	for _, aggregate := range aggregates {
		ordered = append(ordered, aggregate)
	}
	out := make([]api.DebugDependencyLatencyItem, 0, len(ordered))
	for _, aggregate := range ordered {
		baselineP95 := debugWeightedDependencyPercentile(aggregate.baseline, 0.95)
		currentP95 := debugWeightedDependencyPercentile(aggregate.current, 0.95)
		p95Delta := currentP95 - baselineP95
		factor := float64(0)
		if baselineP95 > 0 {
			factor = roundDebugDependencyFactor(float64(currentP95) / float64(baselineP95))
		}
		regression := aggregate.baselineCalls >= debugDependencyHistoryMinCalls &&
			aggregate.currentCalls >= debugDependencyHistoryMinCalls &&
			baselineP95 > 0 && currentP95 > 0 &&
			factor >= debugDependencyRegressionFactor &&
			p95Delta >= debugDependencyRegressionDeltaMS
		baselineErrorRate := debugDependencyErrorRate(aggregate.baselineErrors, aggregate.baselineCalls)
		currentErrorRate := debugDependencyErrorRate(aggregate.currentErrors, aggregate.currentCalls)
		out = append(out, api.DebugDependencyLatencyItem{
			Type:                 aggregate.dependencyType,
			Kind:                 aggregate.dependencyKind,
			Name:                 aggregate.name,
			Calls:                aggregate.calls,
			ErrorCalls:           aggregate.errors,
			ErrorRatePct:         debugDependencyErrorRate(aggregate.errors, aggregate.calls),
			P50MS:                debugWeightedDependencyPercentile(aggregate.all, 0.50),
			P95MS:                debugWeightedDependencyPercentile(aggregate.all, 0.95),
			P99MS:                debugWeightedDependencyPercentile(aggregate.all, 0.99),
			BaselineP95MS:        baselineP95,
			CurrentP95MS:         currentP95,
			P95DeltaMS:           p95Delta,
			RegressionFactor:     factor,
			Regression:           regression,
			BaselineErrorRatePct: baselineErrorRate,
			CurrentErrorRatePct:  currentErrorRate,
			ErrorRateDeltaPct:    currentErrorRate - baselineErrorRate,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Regression != out[j].Regression {
			return out[i].Regression
		}
		if out[i].CurrentP95MS != out[j].CurrentP95MS {
			return out[i].CurrentP95MS > out[j].CurrentP95MS
		}
		if out[i].Calls != out[j].Calls {
			return out[i].Calls > out[j].Calls
		}
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > debugDependencyHistoryMaxOutput {
		out = out[:debugDependencyHistoryMaxOutput]
		truncated = true
	}
	return out, truncated, representedRequests, spanSamples
}

func minDebugDependencyDuration(duration uint64) uint64 {
	const maxDuration = uint64(24 * time.Hour)
	if duration > maxDuration {
		return maxDuration
	}
	return duration
}

func debugWeightedDependencyPercentile(samples []debugDependencyHistorySample, quantile float64) int64 {
	if len(samples) == 0 {
		return 0
	}
	sort.SliceStable(samples, func(i, j int) bool {
		return samples[i].durationNanos < samples[j].durationNanos
	})
	var total int64
	for _, sample := range samples {
		total += sample.weight
	}
	target := int64(float64(total) * quantile)
	if float64(target) < float64(total)*quantile {
		target++
	}
	if target < 1 {
		target = 1
	}
	var cumulative int64
	for _, sample := range samples {
		cumulative += sample.weight
		if cumulative >= target {
			return int64(sample.durationNanos / uint64(time.Millisecond))
		}
	}
	return int64(samples[len(samples)-1].durationNanos / uint64(time.Millisecond))
}

func debugDependencyErrorRate(errors, calls int64) float64 {
	if calls <= 0 {
		return 0
	}
	return math.Round((float64(errors)*100/float64(calls))*100) / 100
}

func roundDebugDependencyFactor(value float64) float64 {
	return math.Round(value*100) / 100
}

func debugCoverageSignal(rows, requests, total int64) api.DebugCoverageSignal {
	rate := float64(0)
	if total > 0 {
		rate = float64(requests) * 100 / float64(total)
	}
	return api.DebugCoverageSignal{Rows: rows, Requests: requests, RatePct: rate}
}

// sqlc represents MIN/MAX timestamptz expressions as interface{} because
// they are nullable for an empty window. PostgreSQL returns time.Time for a
// non-empty result; keep the projection defensive so an empty window remains
// a valid 200 response rather than a type assertion failure.
func debugCoverageTimestamp(value interface{}) string {
	t, ok := value.(time.Time)
	if !ok {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

const debugRequestIdentifierMaxBytes = 128

// normalizeDebugRequestIdentifier accepts the public x-faas-request-id kept
// in request_telemetry.trace_id and the internal telemetry-row UUID returned
// by older list clients. Keeping the value opaque is intentional because the
// generated public ID is a 32-character trace identifier rather than a UUID.
func normalizeDebugRequestIdentifier(raw string) (string, error) {
	identifier := strings.TrimSpace(raw)
	if identifier == "" {
		return "", fmt.Errorf("req_id must be a public request id or telemetry row id")
	}
	if len(identifier) > debugRequestIdentifierMaxBytes {
		return "", fmt.Errorf("req_id must be at most %d bytes", debugRequestIdentifierMaxBytes)
	}
	return identifier, nil
}

// debugTelemetryGetHandler — GET /v1/apps/{slug}/debug/requests/{req_id}
//
// Direct lookup for a single request. Unlike the list endpoint, this
// does not depend on the request still being inside the first page of
// recent telemetry, which makes it usable for incident links and CLI
// drill-downs on busy apps. loadApp + app_id in the query keep it
// IDOR-safe.
func (s *server) debugTelemetryGetHandler(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}
	identifier, err := normalizeDebugRequestIdentifier(r.PathValue("req_id"))
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	now := time.Now().UTC()
	retention := time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour
	row, err := s.store.GetRequestTelemetryByAppAndIdentifier(r.Context(), sqlc.GetRequestTelemetryByAppAndIdentifierParams{
		AppID:         stringToPgUUID(app.ID),
		Identifier:    identifier,
		ReceivedFrom:  pgtype.Timestamptz{Time: now.Add(-retention), Valid: true},
		ReceivedUntil: pgtype.Timestamptz{Time: now, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "request telemetry not found"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("get request telemetry"))
		return
	}
	writeJSON(w, http.StatusOK, debugTelemetryGetRowToItem(row))
}

const debugEvidenceMaxSpans = 100
const debugEvidenceMaxSpanTextBytes = 256

var (
	debugEvidenceQuotedLiteral  = regexp.MustCompile(`'(?:''|[^'])*'`)
	debugEvidenceNumericLiteral = regexp.MustCompile(`\b\d+(?:\.\d+)?\b`)
)

// debugRequestEvidenceHandler — GET /v1/apps/{slug}/debug/requests/{req_id}/evidence
//
// Returns a deterministic request/wake timeline, bounded redacted span
// evidence, and an active regression observation when the regression cron has
// produced one. The response contains no LLM call; a future synthesis layer
// can consume this safe structure asynchronously.
func (s *server) debugRequestEvidenceHandler(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}
	identifier, err := normalizeDebugRequestIdentifier(r.PathValue("req_id"))
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	now := time.Now().UTC()
	retention := time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour
	row, err := s.store.GetRequestTelemetryByAppAndIdentifier(r.Context(), sqlc.GetRequestTelemetryByAppAndIdentifierParams{
		AppID:         stringToPgUUID(app.ID),
		Identifier:    identifier,
		ReceivedFrom:  pgtype.Timestamptz{Time: now.Add(-retention), Valid: true},
		ReceivedUntil: pgtype.Timestamptz{Time: now, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "request telemetry not found"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("get debug evidence"))
		return
	}

	request := debugTelemetryGetRowToItem(row)
	spans, truncated := parseDebugEvidenceSpans(row.SpansSummary)
	var regression *api.DebugRegressionItem
	var regressionErr error
	regRows, err := s.store.ListActiveRegressionsByApp(r.Context(), sqlc.ListActiveRegressionsByAppParams{
		AppID: stringToPgUUID(app.ID),
		Column2: pgtype.Interval{
			Microseconds: int64(retention / time.Microsecond),
			Valid:        true,
		},
	})
	if err != nil {
		// Regression observations are enrichment. A schema/query outage must
		// not discard the request row, wake timeline, or bounded spans that
		// were already loaded successfully.
		regressionErr = err
		if s.log != nil {
			s.log.Warn("debug evidence regression enrichment unavailable",
				"app_id", app.ID, "request_id", identifier, "err", err)
		}
	}
	if regressionErr == nil {
		deploymentID := uuidFromPg(row.DeploymentID)
		for i := range regRows {
			if uuidFromPg(regRows[i].DeploymentID) == deploymentID && regRows[i].Route == row.Route {
				item := debugRegressionRowToItem(regRows[i])
				regression = &item
				break
			}
		}
	}
	timeline, err := s.buildDebugRequestTimeline(r.Context(), app.ID, request, regression)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("get debug request timeline"))
		return
	}
	correlation := buildDebugRequestCorrelation(request, timeline, spans)
	criticalPath := buildDebugCriticalPath(spans)
	if criticalPath != nil && truncated {
		criticalPath.Complete = false
	}
	dependencyLatency, dependencyLatencyTruncated := buildDebugDependencyLatency(spans)

	explanation := buildDebugEvidenceExplanation(request, regression, spans)
	if regressionErr != nil {
		explanation = buildDebugEvidenceDegradedExplanation(spans)
	}

	response := api.DebugRequestEvidenceResponse{
		Request:                    request,
		Regression:                 regression,
		Timeline:                   timeline,
		Correlation:                correlation,
		CriticalPath:               criticalPath,
		DependencyLatency:          dependencyLatency,
		DependencyLatencyTruncated: dependencyLatencyTruncated,
		Spans:                      spans,
		SpansTruncated:             truncated,
		Explanation:                explanation,
		GeneratedAt:                now.Format(time.RFC3339Nano),
	}
	response.Explanation = debugger.Synthesize(response)
	writeJSON(w, http.StatusOK, response)
}

func buildDebugEvidenceDegradedExplanation(spans []api.DebugTelemetrySpan) api.DebugEvidenceExplanation {
	explanation := api.DebugEvidenceExplanation{
		Status:   "regression_unavailable",
		Headline: "Regression enrichment is temporarily unavailable; request evidence is otherwise complete.",
	}
	if len(spans) > 0 {
		primary := spans[0]
		explanation.PrimarySpan = &primary
	}
	return explanation
}

type debugEvidenceSpan struct {
	TraceID           string            `json:"trace_id"`
	SpanID            string            `json:"span_id"`
	ParentSpanID      string            `json:"parent_span_id"`
	Name              string            `json:"name"`
	Kind              string            `json:"kind"`
	StartTimeUnixNano uint64            `json:"start_time_unix_nano"`
	EndTimeUnixNano   uint64            `json:"end_time_unix_nano"`
	DurationNanos     uint64            `json:"duration_nanos"`
	Status            string            `json:"status"`
	DBStatement       string            `json:"db_statement"`
	Attributes        map[string]string `json:"attributes"`
}

// parseDebugEvidenceSpans parses the writer's JSON summary, drops sensitive
// fields, sorts slowest first, and caps the response size. Malformed summaries
// are treated as absent evidence so a bad future payload cannot break lookup.
func parseDebugEvidenceSpans(raw []byte) ([]api.DebugTelemetrySpan, bool) {
	if len(raw) == 0 {
		return []api.DebugTelemetrySpan{}, false
	}
	var input []debugEvidenceSpan
	if err := json.Unmarshal(raw, &input); err != nil {
		return []api.DebugTelemetrySpan{}, false
	}
	sort.SliceStable(input, func(i, j int) bool {
		if input[i].DurationNanos != input[j].DurationNanos {
			return input[i].DurationNanos > input[j].DurationNanos
		}
		return input[i].SpanID < input[j].SpanID
	})
	truncated := len(input) > debugEvidenceMaxSpans
	if truncated {
		input = input[:debugEvidenceMaxSpans]
	}
	out := make([]api.DebugTelemetrySpan, 0, len(input))
	for _, span := range input {
		dependencyType := sanitizeDebugDependencyType(span.Attributes["gregale.dependency.type"])
		dependencyKind := ""
		if dependencyType != "" {
			dependencyKind = sanitizeDebugDependencyKind(span.Attributes["gregale.dependency.kind"])
		}
		out = append(out, api.DebugTelemetrySpan{
			TraceID:        boundDebugEvidenceText(span.TraceID, debugEvidenceMaxSpanTextBytes),
			SpanID:         boundDebugEvidenceText(span.SpanID, debugEvidenceMaxSpanTextBytes),
			ParentSpanID:   boundDebugEvidenceText(span.ParentSpanID, debugEvidenceMaxSpanTextBytes),
			Name:           boundDebugEvidenceText(span.Name, debugEvidenceMaxSpanTextBytes),
			Kind:           boundDebugEvidenceText(span.Kind, debugEvidenceMaxSpanTextBytes),
			StartTime:      debugEvidenceTime(span.StartTimeUnixNano),
			EndTime:        debugEvidenceTime(span.EndTimeUnixNano),
			DurationNanos:  span.DurationNanos,
			Status:         boundDebugEvidenceText(span.Status, debugEvidenceMaxSpanTextBytes),
			DBStatement:    sanitizeDebugDBStatement(span.DBStatement),
			DependencyType: dependencyType,
			DependencyKind: dependencyKind,
		})
	}
	return out, truncated
}

func debugEvidenceTime(unixNano uint64) string {
	if unixNano == 0 || unixNano > uint64(^uint64(0)>>1) {
		return ""
	}
	return time.Unix(0, int64(unixNano)).UTC().Format(time.RFC3339Nano)
}

func sanitizeDebugDependencyType(value string) string {
	switch strings.TrimSpace(value) {
	case "managed_binding", "outbound_integration", "guest_transport", "platform_internal":
		return strings.TrimSpace(value)
	default:
		return ""
	}
}

func sanitizeDebugDependencyKind(value string) string {
	value = strings.TrimSpace(value)
	if len(value) == 0 || len(value) > 64 {
		return ""
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '.' && c != '_' && c != '-' {
			return ""
		}
	}
	return value
}

type debugDependencyLatencyAggregate struct {
	dependencyType string
	dependencyKind string
	name           string
	calls          int
	errors         int
	totalNanos     uint64
	maxNanos       uint64
}

// buildDebugDependencyLatency groups only retained, already-redacted spans.
// The platform classification comes from a small allowlist of attributes;
// arbitrary customer attributes and destinations are ignored.
func buildDebugDependencyLatency(spans []api.DebugTelemetrySpan) ([]api.DebugRequestDependencyLatency, bool) {
	const maxAggregateNanos = uint64(24 * time.Hour)

	aggregates := make(map[string]*debugDependencyLatencyAggregate)
	for _, span := range spans {
		dependencyType := span.DependencyType
		if dependencyType == "" {
			dependencyType = "application"
		}
		name := span.Name
		if name == "" {
			name = "<unnamed>"
		}
		key := dependencyType + "\x00" + span.DependencyKind + "\x00" + name
		aggregate := aggregates[key]
		if aggregate == nil {
			aggregate = &debugDependencyLatencyAggregate{
				dependencyType: dependencyType,
				dependencyKind: span.DependencyKind,
				name:           name,
			}
			aggregates[key] = aggregate
		}
		duration := span.DurationNanos
		if duration > maxAggregateNanos {
			duration = maxAggregateNanos
		}
		aggregate.calls++
		if strings.EqualFold(span.Status, "error") {
			aggregate.errors++
		}
		if aggregate.totalNanos > maxAggregateNanos-duration {
			aggregate.totalNanos = maxAggregateNanos
		} else {
			aggregate.totalNanos += duration
		}
		if duration > aggregate.maxNanos {
			aggregate.maxNanos = duration
		}
	}

	ordered := make([]*debugDependencyLatencyAggregate, 0, len(aggregates))
	for _, aggregate := range aggregates {
		ordered = append(ordered, aggregate)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].maxNanos != ordered[j].maxNanos {
			return ordered[i].maxNanos > ordered[j].maxNanos
		}
		if ordered[i].totalNanos != ordered[j].totalNanos {
			return ordered[i].totalNanos > ordered[j].totalNanos
		}
		if ordered[i].dependencyType != ordered[j].dependencyType {
			return ordered[i].dependencyType < ordered[j].dependencyType
		}
		if ordered[i].dependencyKind != ordered[j].dependencyKind {
			return ordered[i].dependencyKind < ordered[j].dependencyKind
		}
		return ordered[i].name < ordered[j].name
	})

	truncated := len(ordered) > debugDependencyLatencyMax
	if truncated {
		ordered = ordered[:debugDependencyLatencyMax]
	}
	out := make([]api.DebugRequestDependencyLatency, 0, len(ordered))
	for _, aggregate := range ordered {
		out = append(out, api.DebugRequestDependencyLatency{
			Type:            aggregate.dependencyType,
			Kind:            aggregate.dependencyKind,
			Name:            aggregate.name,
			Calls:           aggregate.calls,
			Errors:          aggregate.errors,
			TotalDurationMS: int64(aggregate.totalNanos / uint64(time.Millisecond)),
			MaxDurationMS:   int64(aggregate.maxNanos / uint64(time.Millisecond)),
		})
	}
	return out, truncated
}

func boundDebugEvidenceText(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func sanitizeDebugDBStatement(statement string) string {
	statement = debugEvidenceQuotedLiteral.ReplaceAllString(statement, "?")
	statement = debugEvidenceNumericLiteral.ReplaceAllString(statement, "?")
	statement = strings.Join(strings.Fields(statement), " ")
	return boundDebugEvidenceText(statement, 512)
}

func buildDebugEvidenceExplanation(request api.DebugTelemetryRequestItem, regression *api.DebugRegressionItem, spans []api.DebugTelemetrySpan) api.DebugEvidenceExplanation {
	if regression == nil {
		return api.DebugEvidenceExplanation{
			Status:   "unobserved",
			Headline: "No active regression observation is available for this request.",
		}
	}
	headline := fmt.Sprintf("%s on %s is %sx slower than baseline (p95 %dms vs %dms).", request.Route, regression.DeploymentID, regression.Factor, regression.P95MS, regression.P95BaseMS)
	var primary *api.DebugTelemetrySpan
	if len(spans) > 0 {
		copy := spans[0]
		primary = &copy
		headline += fmt.Sprintf(" Slowest span: %s (%dms).", copy.Name, copy.DurationNanos/1_000_000)
	}
	return api.DebugEvidenceExplanation{
		Status:      "regression_detected",
		Headline:    headline,
		PrimarySpan: primary,
	}
}

// debugTelemetryRowToItem maps a sqlc-generated row to the wire
// DTO. Lives in cmd/apid/ because pkg/api cannot import pkg/state
// (import cycle). The mapping handles pgtype.UUID → string
// (hyphenated hex), pgtype.Timestamptz → RFC3339Nano, and
// pgtype.Text (nullable trace_id) → *string.
func debugTelemetryRowToItem(row sqlc.ListRequestTelemetryByAppRow) api.DebugTelemetryRequestItem {
	return debugTelemetryItemFromFields(
		row.ID,
		row.DeploymentID,
		row.Route,
		row.Method,
		row.Status,
		row.LatencyMs,
		row.Count,
		row.ColdBoot,
		row.TraceID,
		row.ReceivedAt,
		row.WakeID,
		row.InstanceID,
		row.GuestDurationMs,
		row.GuestRuntime,
		row.GuestOutcome,
		row.GuestErrorClass,
		row.ConsumerID,
	)
}

func debugTelemetryGetRowToItem(row sqlc.GetRequestTelemetryByAppAndIdentifierRow) api.DebugTelemetryRequestItem {
	return debugTelemetryItemFromFields(
		row.ID,
		row.DeploymentID,
		row.Route,
		row.Method,
		row.Status,
		row.LatencyMs,
		row.Count,
		row.ColdBoot,
		row.TraceID,
		row.ReceivedAt,
		row.WakeID,
		row.InstanceID,
		row.GuestDurationMs,
		row.GuestRuntime,
		row.GuestOutcome,
		row.GuestErrorClass,
		row.ConsumerID,
	)
}

func debugTelemetryItemFromFields(
	id, deploymentID pgtype.UUID,
	route, method string,
	status, latencyMS int32,
	count int32,
	coldBoot bool,
	traceID pgtype.Text,
	receivedAt pgtype.Timestamptz,
	wakeID, instanceID pgtype.Text,
	guestDurationMS int32, guestRuntime, guestOutcome, guestErrorClass string,
	consumerID pgtype.UUID,
) api.DebugTelemetryRequestItem {
	item := api.DebugTelemetryRequestItem{
		// pgtype.UUID -> hyphenated hex string. Falls back to "" when
		// Valid=false so the JSON renders "" rather than the driver's
		// base64 zero-bytes shape.
		ID:           uuidFromPg(id),
		DeploymentID: uuidFromPg(deploymentID),
		Route:        route,
		Method:       method,
		Status:       int(status),
		LatencyMS:    int(latencyMS),
		Count:        int(count),
		ColdBoot:     coldBoot,
		ReceivedAt:   timeFromPg(receivedAt),
		WakeID:       textFromPg(wakeID),
		InstanceID:   textFromPg(instanceID),
		ConsumerID:   uuidFromPg(consumerID),
	}
	if traceID.Valid {
		s := traceID.String
		item.TraceID = &s
	}
	if guestRuntime != "" && guestRuntime != "__unknown__" && guestOutcome != "" && guestOutcome != "missing" {
		item.Guest = &api.DebugGuestExecutionEvidence{
			Runtime: guestRuntime, DurationMS: int(guestDurationMS),
			Outcome: guestOutcome, ErrorClass: guestErrorClass,
		}
	}
	return item
}

func textFromPg(value pgtype.Text) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

const (
	debugTimelineMaxEvents    = 200
	debugDependencyLatencyMax = 16
)

var debugCorrelationPhases = [...]string{"edge", "queue", "wake", "guest", "downstream", "billing"}

// buildDebugRequestTimeline joins the retained request row to the wake event
// stream using the opaque wake_id captured at the gateway. The request/error
// markers are synthesized from the row itself, so a failed request remains
// explainable even when no wake was involved. Event payloads are intentionally
// reduced to a stable summary; arbitrary JSON never reaches the customer.
func (s *server) buildDebugRequestTimeline(ctx context.Context, appID string, request api.DebugTelemetryRequestItem, regression *api.DebugRegressionItem) ([]api.DebugTimelineEvent, error) {
	recordedCompletionAt, err := time.Parse(time.RFC3339Nano, request.ReceivedAt)
	if err != nil {
		return nil, err
	}
	wakeTimeline := make([]api.DebugTimelineEvent, 0, 12)
	var earliestWakeAt, latestWakeAt time.Time
	if request.WakeID != "" {
		events, listErr := s.store.ListEventsByWakeID(ctx, request.WakeID, time.Time{}, debugTimelineMaxEvents+1)
		if listErr != nil {
			return nil, listErr
		}
		for _, event := range events {
			if !eventDataHasAppID(event.Data, appID) {
				continue
			}
			// Reserve room for the request completion/error and optional
			// regression markers so a very chatty wake never hides the
			// outcome that the customer is investigating.
			if len(wakeTimeline) >= debugTimelineMaxEvents-4 {
				break
			}
			wakeTimeline = append(wakeTimeline, api.DebugTimelineEvent{
				At:      event.At.UTC().Format(time.RFC3339Nano),
				Phase:   "wake",
				Kind:    event.Kind,
				Actor:   event.Actor,
				Summary: debugWakeTimelineSummary(event),
			})
			if debugWakeEventAnchorsRequest(event.Kind) {
				if earliestWakeAt.IsZero() || event.At.Before(earliestWakeAt) {
					earliestWakeAt = event.At
				}
				if latestWakeAt.IsZero() || event.At.After(latestWakeAt) {
					latestWakeAt = event.At
				}
			}
		}
	}

	// request_telemetry.received_at is a collapsed minute-bucket timestamp,
	// while wake events retain precise times. Keep the representative latency
	// visible, but anchor impossible coarse markers around their linked wake so
	// the customer timeline can never claim completion before queue, boot, or
	// first-byte evidence.
	completionAt := recordedCompletionAt
	completionAnchored := false
	if !latestWakeAt.IsZero() && !completionAt.After(latestWakeAt) {
		completionAt = latestWakeAt.Add(time.Nanosecond)
		completionAnchored = true
	}
	startAt := recordedCompletionAt.Add(-time.Duration(request.LatencyMS) * time.Millisecond)
	if !earliestWakeAt.IsZero() && !startAt.Before(earliestWakeAt) {
		startAt = earliestWakeAt.Add(-time.Nanosecond)
	}
	if !startAt.Before(completionAt) {
		startAt = completionAt.Add(-time.Millisecond)
	}
	timeline := make([]api.DebugTimelineEvent, 0, len(wakeTimeline)+5)
	timeline = append(timeline, api.DebugTimelineEvent{
		At:      startAt.UTC().Format(time.RFC3339Nano),
		Phase:   "request",
		Kind:    "request.received",
		Summary: "representative request entered the gateway (estimated from collapsed telemetry)",
		// received_at is the minute bucket boundary for collapsed rows,
		// so request markers are intentionally labeled approximate.
		Approximate: true,
	})
	if request.Guest != nil {
		guestAt := completionAt.Add(-time.Duration(request.Guest.DurationMS) * time.Millisecond)
		timeline = append(timeline, api.DebugTimelineEvent{
			At:          guestAt.UTC().Format(time.RFC3339Nano),
			Phase:       "guest",
			Kind:        "guest.execution",
			Summary:     fmt.Sprintf("%s guest execution observed (%s); timestamp estimated", request.Guest.Runtime, request.Guest.Outcome),
			DurationMS:  int64(request.Guest.DurationMS),
			Status:      request.Status,
			Approximate: true,
		})
	}
	timeline = append(timeline, wakeTimeline...)

	if regression != nil && regression.LastDetectedAt != "" {
		timeline = append(timeline, api.DebugTimelineEvent{
			At:      regression.LastDetectedAt,
			Phase:   "regression",
			Kind:    "regression.detected",
			Summary: fmt.Sprintf("route regression observed at %sx baseline", regression.Factor),
		})
	}

	completionSummary := fmt.Sprintf("representative request completed in %dms (estimated from collapsed telemetry)", request.LatencyMS)
	if completionAnchored {
		completionSummary += "; completion anchored after linked wake evidence"
	}
	timeline = append(timeline, api.DebugTimelineEvent{
		At:          completionAt.UTC().Format(time.RFC3339Nano),
		Phase:       "request",
		Kind:        "request.completed",
		Summary:     completionSummary,
		DurationMS:  int64(request.LatencyMS),
		Status:      request.Status,
		Approximate: true,
	})
	if request.Status >= http.StatusBadRequest {
		timeline = append(timeline, api.DebugTimelineEvent{
			At:      completionAt.UTC().Format(time.RFC3339Nano),
			Phase:   "error",
			Kind:    "request.error",
			Summary: fmt.Sprintf("HTTP %d response", request.Status),
			Status:  request.Status,
		})
	}

	sort.SliceStable(timeline, func(i, j int) bool {
		if timeline[i].At != timeline[j].At {
			return timeline[i].At < timeline[j].At
		}
		return debugTimelinePhaseRank(timeline[i].Phase) < debugTimelinePhaseRank(timeline[j].Phase)
	})
	if len(timeline) > debugTimelineMaxEvents {
		timeline = timeline[:debugTimelineMaxEvents]
	}
	return timeline, nil
}

func debugWakeEventAnchorsRequest(kind string) bool {
	switch kind {
	case "wake.queue_accepted", "wake.admitted", "wake.boot_started", "wake.readiness_200", "wake.boot_completed", "wake.proxy_first_byte", "wake.boot_failed":
		return true
	default:
		return false
	}
}

func debugTimelinePhaseRank(phase string) int {
	switch phase {
	case "request":
		return 0
	case "guest":
		return 1
	case "wake":
		return 2
	case "error":
		return 3
	case "regression":
		return 4
	default:
		return 4
	}
}

func debugWakeTimelineSummary(event state.Event) string {
	switch event.Kind {
	case "wake.queue_accepted":
		return "wake queued for admission"
	case "wake.admitted":
		return "wake admitted"
	case "wake.boot_started":
		return "instance boot started"
	case "wake.readiness_200":
		return "instance readiness probe returned 200"
	case "wake.proxy_first_byte":
		return "first byte received from instance"
	case "wake.boot_completed":
		return "instance boot completed"
	case "wake.boot_failed":
		return "instance boot failed"
	default:
		return "wake lifecycle event"
	}
}

// buildDebugRequestCorrelation turns the bounded timeline and span evidence
// into a fixed-shape request narrative. A stage is never omitted: customers
// can distinguish an inapplicable warm-request wake from a missing telemetry
// signal, instead of mistaking an empty timeline for a healthy request.
func buildDebugRequestCorrelation(request api.DebugTelemetryRequestItem, timeline []api.DebugTimelineEvent, spans []api.DebugTelemetrySpan) api.DebugRequestCorrelation {
	stages := make([]api.DebugRequestCorrelationStage, 0, len(debugCorrelationPhases))
	for _, phase := range debugCorrelationPhases {
		stages = append(stages, api.DebugRequestCorrelationStage{Phase: phase, Status: "missing"})
	}

	// The request row is a collapsed latency bucket, so the edge markers are
	// useful but explicitly approximate rather than pretending to be a raw
	// request trace.
	edge := &stages[0]
	edge.Status = "partial"
	edge.StartedAt = correlationEventAt(timeline, "request.received")
	edge.CompletedAt = correlationEventAt(timeline, "request.completed")
	edge.DurationMS = int64(request.LatencyMS)
	edge.EvidenceCount = countCorrelationKinds(timeline, "request.received", "request.completed")
	edge.Approximate = true
	edge.Reason = "estimated from collapsed request telemetry; linked wake markers anchor impossible ordering"

	if request.WakeID == "" {
		stages[1] = api.DebugRequestCorrelationStage{
			Phase:  "queue",
			Status: "not_applicable",
			Reason: "request did not record a wake",
		}
		stages[2] = api.DebugRequestCorrelationStage{
			Phase:  "wake",
			Status: "not_applicable",
			Reason: "request did not record a wake",
		}
	} else {
		buildDebugQueueCorrelation(&stages[1], timeline)
		buildDebugWakeCorrelation(&stages[2], timeline)
	}

	guest := &stages[3]
	if request.Guest != nil {
		guest.Status = "partial"
		guest.DurationMS = int64(request.Guest.DurationMS)
		guest.EvidenceCount = 1 + countCorrelationKinds(timeline, "wake.proxy_first_byte")
		guest.Approximate = true
		if event := firstCorrelationEvent(timeline, "guest.execution"); event != nil {
			guest.StartedAt = event.At
			guest.CompletedAt = event.At
			if at, err := time.Parse(time.RFC3339Nano, event.At); err == nil {
				guest.CompletedAt = at.Add(time.Duration(request.Guest.DurationMS) * time.Millisecond).UTC().Format(time.RFC3339Nano)
			}
		}
		guest.Reason = fmt.Sprintf("runner observed %s execution (%s)", request.Guest.Runtime, request.Guest.Outcome)
		if request.Guest.ErrorClass != "" {
			guest.Reason += "; error class " + request.Guest.ErrorClass
		}
	} else if event := firstCorrelationEvent(timeline, "wake.proxy_first_byte"); event != nil {
		guest.Status = "partial"
		guest.CompletedAt = event.At
		guest.EvidenceCount = 1
		guest.Reason = "first-byte marker retained; guest execution duration is not captured"
	} else {
		guest.Reason = "no guest first-byte marker was retained"
	}

	downstream := &stages[4]
	if len(spans) > 0 {
		downstream.Status = "observed"
		downstream.EvidenceCount = len(spans)
		downstream.Reason = "slowest retained child span; span durations may overlap"
		for _, span := range spans {
			ms := int64(span.DurationNanos / 1_000_000)
			if ms > downstream.DurationMS {
				downstream.DurationMS = ms
			}
		}
	} else {
		downstream.Reason = "no linked OpenTelemetry spans were retained"
	}

	// Billed dimensions are deliberately not part of request_telemetry yet.
	// Keep the absence visible in the same fixed-shape response so customers
	// know this is a product gap, not evidence that billing was free.
	stages[5] = api.DebugRequestCorrelationStage{
		Phase:  "billing",
		Status: "missing",
		Reason: "billed dimensions are not attached to request evidence yet",
	}

	complete := true
	for _, stage := range stages {
		if stage.Status == "missing" || stage.Status == "partial" {
			complete = false
			break
		}
	}
	return api.DebugRequestCorrelation{Stages: stages, Complete: complete}
}

func buildDebugQueueCorrelation(stage *api.DebugRequestCorrelationStage, timeline []api.DebugTimelineEvent) {
	accepted := firstCorrelationEvent(timeline, "wake.queue_accepted")
	admitted := firstCorrelationEvent(timeline, "wake.admitted")
	stage.EvidenceCount = countCorrelationKinds(timeline, "wake.queue_accepted", "wake.admitted")
	switch {
	case accepted != nil && admitted != nil:
		stage.Status = "observed"
		stage.StartedAt, stage.CompletedAt, stage.DurationMS = correlationRange(accepted, admitted)
	case accepted != nil:
		stage.Status = "partial"
		stage.StartedAt = accepted.At
		stage.Reason = "queue admission marker is missing"
	case admitted != nil:
		stage.Status = "partial"
		stage.CompletedAt = admitted.At
		stage.Reason = "queue accepted marker is missing"
	default:
		stage.Reason = "no queue lifecycle markers were retained"
	}
}

func buildDebugWakeCorrelation(stage *api.DebugRequestCorrelationStage, timeline []api.DebugTimelineEvent) {
	started := firstCorrelationEvent(timeline, "wake.boot_started")
	ready := firstCorrelationEvent(timeline, "wake.readiness_200")
	completed := firstCorrelationEvent(timeline, "wake.boot_completed")
	failed := firstCorrelationEvent(timeline, "wake.boot_failed")
	stage.EvidenceCount = countCorrelationKinds(timeline, "wake.boot_started", "wake.readiness_200", "wake.boot_completed", "wake.boot_failed")
	terminal := completed
	if terminal == nil {
		terminal = ready
	}
	if terminal == nil {
		terminal = failed
	}
	switch {
	case started != nil && completed != nil:
		stage.Status = "observed"
		stage.StartedAt, stage.CompletedAt, stage.DurationMS = correlationRange(started, completed)
	case started != nil && terminal != nil:
		stage.Status = "partial"
		stage.StartedAt, stage.CompletedAt, stage.DurationMS = correlationRange(started, terminal)
		if failed != nil {
			stage.Reason = "wake boot failed before completion"
		} else {
			stage.Reason = "wake completion marker is missing"
		}
	case started != nil:
		stage.Status = "partial"
		stage.StartedAt = started.At
		stage.Reason = "wake readiness/completion marker is missing"
	case terminal != nil:
		stage.Status = "partial"
		stage.CompletedAt = terminal.At
		stage.Reason = "wake boot-start marker is missing"
	default:
		stage.Reason = "no wake lifecycle markers were retained"
	}
}

func firstCorrelationEvent(timeline []api.DebugTimelineEvent, kind string) *api.DebugTimelineEvent {
	for i := range timeline {
		if timeline[i].Kind == kind {
			return &timeline[i]
		}
	}
	return nil
}

func countCorrelationKinds(timeline []api.DebugTimelineEvent, kinds ...string) int {
	wanted := make(map[string]struct{}, len(kinds))
	for _, kind := range kinds {
		wanted[kind] = struct{}{}
	}
	count := 0
	for _, event := range timeline {
		if _, ok := wanted[event.Kind]; ok {
			count++
		}
	}
	return count
}

func correlationEventAt(timeline []api.DebugTimelineEvent, kind string) string {
	if event := firstCorrelationEvent(timeline, kind); event != nil {
		return event.At
	}
	return ""
}

func correlationRange(start, end *api.DebugTimelineEvent) (string, string, int64) {
	if start == nil || end == nil {
		return correlationEventAtValue(start), correlationEventAtValue(end), 0
	}
	startAt, errStart := time.Parse(time.RFC3339Nano, start.At)
	endAt, errEnd := time.Parse(time.RFC3339Nano, end.At)
	if errStart != nil || errEnd != nil || endAt.Before(startAt) {
		return start.At, end.At, 0
	}
	return start.At, end.At, endAt.Sub(startAt).Milliseconds()
}

func correlationEventAtValue(event *api.DebugTimelineEvent) string {
	if event == nil {
		return ""
	}
	return event.At
}

// uuidFromPg renders a pgtype.UUID as the canonical hyphenated-hex
// string. Empty when !Valid so the wire shows "" rather than the
// driver's zero-bytes encoding.
func uuidFromPg(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

// timeFromPg renders a pgtype.Timestamptz as an RFC3339Nano string
// (matches the format the rest of the apid surface uses for
// timestamps). Empty when !Valid.
func timeFromPg(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.UTC().Format(time.RFC3339Nano)
}

// echoDebugSince produces a wire-stable echo of a `since` request
// parameter so that customer automation can feed the response
// straight back into a follow-up request without re-parsing.
// When the customer supplied a parseable value AND the effective
// duration matches (no clamp), the original raw form is returned
// verbatim — round-trip safe (`?since=5d` → `"5d"`, not
// `120h0m0s` which parseDebugSinceFromString would accept but
// downstream tooling rarely normalizes to). When the effective
// duration was clamped by the plan cap, or when no raw value
// was supplied, the effective duration is rendered in the
// canonical `Nh` or `Nd` form so the customer can detect the
// discrepancy (or supply a default that round-trips).
func echoDebugSince(raw string, eff time.Duration) string {
	if raw != "" && parseDebugSinceFromString(raw, -1) == eff {
		return raw
	}
	if eff <= 0 {
		return ""
	}
	if eff%(24*time.Hour) == 0 {
		n := int(eff / (24 * time.Hour))
		return strconv.Itoa(n) + "d"
	}
	return eff.String()
}

// stringToPgUUID converts a hyphenated-hex UUID string into the
// pgtype.UUID shape the sqlc-generated queries expect. Invalid
// strings produce a zero-UUID value with Valid=false so the
// Postgres driver returns no rows rather than an error.
func stringToPgUUID(s string) pgtype.UUID {
	uid, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: uid, Valid: true}
}

// debugRegressionsHandler — GET /v1/apps/{slug}/debug/regressions
//
// Returns active regression observations for an app, ordered by
// regression_factor DESC then last_detected_at DESC (worst
// first). Plan-gated by DebugTelemetryEnabled. `since` is
// clamped to the plan's DebugTelemetryRetentionDays.
//
// The endpoint backs the dashboard regression banner
// (pkg/dashboard/templates/app_debug.html, PR-B) and the
// `gregale debug regressions <slug>` CLI verb.
func (s *server) debugRegressionsHandler(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}
	sinceRaw := r.URL.Query().Get("since")
	sinceDur := parseDebugSinceFromString(sinceRaw, 1*time.Hour)
	cap := time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour
	if cap > 0 && sinceDur > cap {
		sinceDur = cap
	}
	rows, err := s.store.ListActiveRegressionsByApp(r.Context(), sqlc.ListActiveRegressionsByAppParams{
		AppID: stringToPgUUID(app.ID),
		Column2: pgtype.Interval{
			Microseconds: int64(sinceDur / time.Microsecond),
			Valid:        true,
		},
	})
	if err != nil {
		if s.log != nil {
			s.log.Error("debug regression read failed", "app_id", app.ID, "err", err)
		}
		api.WriteProblem(w, api.ErrDebugRegressionUnavailable(
			"regression observations are temporarily unavailable; retry after the debugger database is repaired"))
		return
	}
	if rows == nil {
		rows = []sqlc.ListActiveRegressionsByAppRow{}
	}
	items := make([]api.DebugRegressionItem, len(rows))
	for i, row := range rows {
		items[i] = debugRegressionRowToItem(row)
	}
	writeJSON(w, http.StatusOK, api.DebugRegressionsResponse{
		Since:       echoDebugSince(sinceRaw, sinceDur),
		Regressions: items,
	})
}

// checkDebugRegressionReadiness is intentionally a narrow optional seam so
// MemStore-backed unit tests keep their fast shape while production's PgStore
// verifies the relation and parameterized read before apid starts serving.
func checkDebugRegressionReadiness(ctx context.Context, store state.Store) error {
	_, err := debugRegressionReadinessCheck(ctx, store)
	return err
}

func debugRegressionReadinessCheck(ctx context.Context, store state.Store) (bool, error) {
	checker, ok := store.(interface {
		CheckDebugRegressionReadiness(context.Context) error
	})
	if !ok {
		return false, nil
	}
	return true, checker.CheckDebugRegressionReadiness(ctx)
}

// debugRegressionRowToItem maps a sqlc row to the wire DTO.
func debugRegressionRowToItem(row sqlc.ListActiveRegressionsByAppRow) api.DebugRegressionItem {
	item := api.DebugRegressionItem{
		DeploymentID:    uuidFromPg(row.DeploymentID),
		Route:           row.Route,
		P95MS:           int(row.P95Ms),
		P95BaseMS:       int(row.P95BaseMs),
		AffectedCount:   int(row.AffectedCount),
		FirstDetectedAt: timeFromPg(row.FirstDetectedAt),
		LastDetectedAt:  timeFromPg(row.LastDetectedAt),
		State:           debugRegressionStateValue(row.State),
		AcknowledgedAt:  timeFromPg(row.AcknowledgedAt),
		DismissedUntil:  timeFromPg(row.DismissedUntil),
		ResolvedAt:      timeFromPg(row.ResolvedAt),
	}
	// Numeric factor → string. pgtype.Numeric has its own
	// Float64Value helper; we render via pgx's numeric decoder
	// to avoid the precision drift of the marshaller.
	if row.RegressionFactor.Valid {
		f, err := row.RegressionFactor.Float64Value()
		if err == nil {
			item.Factor = formatFloat2(f.Float64)
		}
	}
	return item
}

func debugRegressionStateValue(value interface{}) string {
	switch value := value.(type) {
	case string:
		return value
	case []byte:
		return string(value)
	default:
		return ""
	}
}

// formatFloat2 renders a float with up to 2 decimal places —
// matches the schema's NUMERIC(5,2) precision. Used for the
// regression_factor wire field; "1.20", "2.43", "1.00".
func formatFloat2(v float64) string {
	if v <= 0 {
		return "0.00"
	}
	return fmt.Sprintf("%.2f", v)
}

// debugCompareHandler — POST /v1/apps/{slug}/debug/compare
//
// Compares two deployments' per-route latency distributions in a
// shared time window. Body shape: DebugCompareRequest (source,
// mirror deployment_ids + optional route filter + optional
// window bounds). Returns DebugCompareResponse with one row per
// route that shipped traffic in both deployments.
//
// PR-B composes two PR-A queries (RequestTelemetryBaselineP95ByRoute)
// in Go — the CTE-on-CTE shape trips sqlc v1.31's parser (the
// workaround is documented in queries.sql.go:5626-5631).
//
// Plan-gated by DebugTelemetryEnabled.
func (s *server) debugCompareHandler(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}
	var req api.DebugCompareRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid compare body"))
		return
	}
	if _, err := uuid.Parse(req.Source); err != nil {
		api.WriteProblem(w, api.ErrValidation("source must be a deployment id"))
		return
	}
	if _, err := uuid.Parse(req.Mirror); err != nil {
		api.WriteProblem(w, api.ErrValidation("mirror must be a deployment id"))
		return
	}
	// Default window: last 1h. Clamp to plan retention.
	sinceDur := parseDebugSinceFromString(req.Since, 1*time.Hour)
	capDur := time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour
	if capDur > 0 && sinceDur > capDur {
		sinceDur = capDur
	}
	until := time.Now().UTC()
	if req.Until != "" {
		if t, err := time.Parse(time.RFC3339, req.Until); err == nil {
			until = t.UTC()
		}
	}
	from := until.Add(-sinceDur)
	srcID := stringToPgUUID(req.Source)
	mirID := stringToPgUUID(req.Mirror)

	// Fetch both distributions. Both calls share one index scan
	// over request_telemetry_app_dep_received_idx — four
	// aggregates (p50/p95/p99/represented request count) in a
	// single pass (Debugger UX v1 stage 3 sqlc rewrite; PR-B
	// minimum was p95-only and
	// walked client-side for the others).
	srcStats, err := s.fetchRouteStats(r.Context(), app.ID, srcID, from, until, req.Route)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("compare source"))
		return
	}
	mirStats, err := s.fetchRouteStats(r.Context(), app.ID, mirID, from, until, req.Route)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("compare mirror"))
		return
	}
	// Merge: union of routes; missing entries render zero stats.
	// A missing side means zero rows in the window — DO NOT
	// synthesize percentiles from the other side. Test (b) in
	// handlers_debug_telemetry_compare_test.go asserts the
	// N=0/percentiles=0 contract.
	routes := make(map[string]api.DebugCompareRouteStats, len(srcStats)+len(mirStats))
	for route, s := range srcStats {
		routes[route] = api.DebugCompareRouteStats{
			Route:     route,
			SourceP50: s.P50, SourceP95: s.P95, SourceP99: s.P99, SourceN: s.N,
		}
	}
	for route, m := range mirStats {
		existing := routes[route]
		existing.Route = route
		existing.MirrorP50 = m.P50
		existing.MirrorP95 = m.P95
		existing.MirrorP99 = m.P99
		existing.MirrorN = m.N
		routes[route] = existing
	}
	// Stable ordering for deterministic dashboard rendering.
	out := make([]api.DebugCompareRouteStats, 0, len(routes))
	for _, v := range routes {
		out = append(out, v)
	}
	sortRouteStats(out)
	writeJSON(w, http.StatusOK, api.DebugCompareResponse{
		Source: req.Source,
		Mirror: req.Mirror,
		Routes: out,
	})
}

// routeStats is the per-route aggregate used by the compare
// endpoint. Debugger UX v1 stage 3 extended
// RequestTelemetryBaselineP95ByRoute to return p50/p95/p99/N
// from a single index scan; this struct mirrors that shape so
// the dashboard can render the full latency distribution
// alongside the represented request count.
type routeStats struct {
	P50 int
	P95 int
	P99 int
	N   int64
}

// fetchRouteStats reads the per-route p50/p95/p99 + represented request count
// for a single deployment in the window. The shape mirrors the
// regression cron (cmd/apid/debug_regression_cron.go) — same
// count-weighted percentile aggregate, same window split, single index
// scan over request_telemetry_app_dep_received_idx (PR-A
// migration 00427).
//
// Returns an empty map (not nil) when no rows match so the
// caller doesn't have to nil-check. A deployment that had zero
// traffic in the window maps to absent entries (not zero-valued
// entries) — the caller synthesizes the zero-valued DTO so the
// merge loop stays symmetric.
func (s *server) fetchRouteStats(ctx context.Context, appID string, deploymentID pgtype.UUID, from, until time.Time, route string) (map[string]routeStats, error) {
	rows, err := s.store.RequestTelemetryBaselineP95ByRoute(ctx, sqlc.RequestTelemetryBaselineP95ByRouteParams{
		AppID:        stringToPgUUID(appID),
		DeploymentID: deploymentID,
		ReceivedAt:   pgtype.Timestamptz{Time: from, Valid: true},
		ReceivedAt_2: pgtype.Timestamptz{Time: until, Valid: true},
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]routeStats, len(rows))
	for _, row := range rows {
		if route != "" && row.Route != route {
			continue
		}
		out[row.Route] = routeStats{
			P50: int(row.P50Ms),
			P95: int(row.P95Ms),
			P99: int(row.P99Ms),
			N:   row.N,
		}
	}
	return out, nil
}

// sortRouteStats sorts by route name (stable, deterministic
// output for dashboard diffing).
func sortRouteStats(stats []api.DebugCompareRouteStats) {
	// insertion sort — the slice is typically <100 routes
	for i := 1; i < len(stats); i++ {
		j := i
		for j > 0 && stats[j-1].Route > stats[j].Route {
			stats[j-1], stats[j] = stats[j], stats[j-1]
			j--
		}
	}
}

// parseDebugSinceFromString is a variant of parseDebugSince that
// takes the raw string directly (the compare body has its own
// since field, not a query param). Empty / unparseable → def.
func parseDebugSinceFromString(raw string, def time.Duration) time.Duration {
	if raw == "" {
		return def
	}
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}
	if n, err := strconv.Atoi(raw[:len(raw)-1]); err == nil && raw[len(raw)-1] == 'd' && n > 0 {
		return time.Duration(n) * 24 * time.Hour
	}
	return def
}

// parseDebugSinceStrict parses the coverage endpoint's user-supplied
// lookback window without silently changing a malformed request into a
// different query. Empty input keeps the endpoint's documented default;
// every supplied value must be a positive Go duration or positive day
// suffix. The coverage handler applies the plan-retention clamp after this
// validation, so a valid long window remains distinguishable from invalid
// input.
func parseDebugSinceStrict(raw string, def time.Duration) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d, nil
	}
	if len(raw) > 1 && raw[len(raw)-1] == 'd' {
		if n, err := strconv.Atoi(raw[:len(raw)-1]); err == nil && n > 0 {
			return time.Duration(n) * 24 * time.Hour, nil
		}
	}
	return 0, fmt.Errorf("since must be a positive duration (for example 30m, 24h, or 3d)")
}

// debugReplayHandler — POST /v1/apps/{slug}/debug/requests/{req_id}/replay.
//
// A request_telemetry row intentionally contains no raw body or credentials.
// Replay therefore queues a durable invocation carrying the safe request
// metadata and the mirror rule selected for the deployment that served it.
// schedd consumes that row through the gateway's mirror replay path; the
// resulting invocation id is also the customer's polling handle.
func (s *server) debugReplayHandler(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}
	var req api.DebugReplayRequest
	if err := decodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	result, problem := s.enqueueDebugReplay(r.Context(), app, acct, r.PathValue("req_id"), req.MirrorDeploymentID)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusAccepted, api.DebugReplayResponse{
		MirrorInvocationID: result.Invocation.ID,
		Status:             "queued",
		SourceDeploymentID: result.SourceDeploymentID,
		MirrorDeploymentID: result.MirrorDeploymentID,
	})
}

type debugReplayEnqueueResult struct {
	Invocation         state.Invocation
	SourceDeploymentID string
	MirrorDeploymentID string
}

// enqueueDebugReplay is the shared replay core for the JSON API and the
// session-authenticated dashboard form. Keeping the ownership, retention,
// mirror-rule, and metadata checks in one function prevents the browser
// surface from drifting into a less restrictive replay path.
func (s *server) enqueueDebugReplay(ctx context.Context, app state.App, acct state.Account, reqID, requestedMirrorDeploymentID string) (debugReplayEnqueueResult, *api.Problem) {
	identifier, err := normalizeDebugRequestIdentifier(reqID)
	if err != nil {
		return debugReplayEnqueueResult{}, api.ErrValidation(err.Error())
	}
	now := time.Now().UTC()
	retention := time.Duration(api.MustLimitsFor(acct.Plan).DebugTelemetryRetentionDays) * 24 * time.Hour
	row, err := s.store.GetRequestTelemetryByAppAndIdentifier(ctx, sqlc.GetRequestTelemetryByAppAndIdentifierParams{
		AppID:         stringToPgUUID(app.ID),
		Identifier:    identifier,
		ReceivedFrom:  pgtype.Timestamptz{Time: now.Add(-retention), Valid: true},
		ReceivedUntil: pgtype.Timestamptz{Time: now, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return debugReplayEnqueueResult{}, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "request telemetry not found")
	}
	if err != nil {
		return debugReplayEnqueueResult{}, api.ErrCapacity("get debug replay request")
	}
	depID := uuidFromPg(row.DeploymentID)
	requestedMirrorDeploymentID = strings.TrimSpace(requestedMirrorDeploymentID)
	if requestedMirrorDeploymentID != "" {
		if _, err := uuid.Parse(requestedMirrorDeploymentID); err != nil {
			return debugReplayEnqueueResult{}, api.ErrValidation("mirror_deployment_id must be a UUID")
		}
	}
	rules, err := s.store.ListMirrorRules(ctx, app.ID)
	if err != nil {
		return debugReplayEnqueueResult{}, api.ErrCapacity("find debug replay mirror rule")
	}
	rule, found := selectDebugReplayMirrorRule(rules, depID, requestedMirrorDeploymentID)
	if !found {
		message := "the request's serving deployment has no enabled mirror rule; enable a mirror rule for that deployment before replaying"
		if requestedMirrorDeploymentID != "" {
			message = "the requested mirror deployment is not an enabled target for the request's serving deployment"
		}
		return debugReplayEnqueueResult{}, api.NewProblem(http.StatusConflict,
			api.CodeDebugReplayUnsupported,
			"Debug replay is unavailable",
			message)
	}
	sourceRequestID := uuidFromPg(row.ID)
	if row.TraceID.Valid && strings.TrimSpace(row.TraceID.String) != "" {
		sourceRequestID = strings.TrimSpace(row.TraceID.String)
	}
	metadata := map[string]string{
		api.DebugReplayRequestIDHeader:     sourceRequestID,
		api.DebugReplayDeploymentIDHeader:  depID,
		api.DebugReplayMirrorRuleIDHeader:  rule.ID,
		api.DebugReplaySourceStatusHeader:  strconv.Itoa(int(row.Status)),
		api.DebugReplaySourceLatencyHeader: strconv.Itoa(int(row.LatencyMs)),
	}
	if row.TraceID.Valid && row.TraceID.String != "" {
		metadata[api.DebugReplayTraceIDHeader] = row.TraceID.String
	}
	headerBytes, err := json.Marshal(metadata)
	if err != nil {
		return debugReplayEnqueueResult{}, api.ErrCapacity("build debug replay envelope")
	}
	inv, err := s.store.EnqueueInvocation(ctx, state.Invocation{
		AppID:     app.ID,
		AccountID: acct.ID,
		Source:    state.InvocationReplay,
		Method:    row.Method,
		Path:      row.Route,
		Payload:   json.RawMessage("{}"),
		Headers:   headerBytes,
		DueAt:     now,
	})
	if err != nil {
		return debugReplayEnqueueResult{}, api.ErrCapacity("enqueue debug replay")
	}
	return debugReplayEnqueueResult{
		Invocation:         inv,
		SourceDeploymentID: depID,
		MirrorDeploymentID: rule.MirrorDeploymentID,
	}, nil
}

// selectDebugReplayMirrorRule keeps replay target selection constrained to an
// enabled rule whose source is the deployment that served the retained
// request. An empty target preserves the legacy first-match behavior.
func selectDebugReplayMirrorRule(rules []state.MirrorRule, sourceDeploymentID, requestedMirrorDeploymentID string) (state.MirrorRule, bool) {
	for _, rule := range rules {
		if rule.Enabled && rule.SourceDeploymentID == sourceDeploymentID &&
			(requestedMirrorDeploymentID == "" || rule.MirrorDeploymentID == requestedMirrorDeploymentID) {
			return rule, true
		}
	}
	return state.MirrorRule{}, false
}
