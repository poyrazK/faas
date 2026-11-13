package apidserver

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/debugger"
	"github.com/onebox-faas/faas/pkg/state"
)

// debugEvidenceLogs attaches the log lines one request's app emitted,
// matched by its trace ID within a bounded window around the request. Only
// runtime rows are read: they come from the app's OTel logs through the
// in-guest bridge (ADR-213 runtime adapter); the HTTP access projection
// would only repeat the request itself. Like the other enrichments it is
// best-effort: a failed read returns no lines and never fails the response.
func (s *server) debugEvidenceLogs(ctx context.Context, accountID, appID string, request api.DebugTelemetryRequestItem) ([]api.DebugEvidenceLogLine, bool) {
	lines := []api.DebugEvidenceLogLine{}
	if request.TraceID == nil || *request.TraceID == "" {
		return lines, false
	}
	since, until, ok := debugEvidenceLogWindow(request)
	if !ok {
		return lines, false
	}
	events, more, err := s.store.ListLogEvents(ctx, state.LogEventFilter{
		AccountID: accountID,
		AppID:     appID,
		Source:    state.LogEventSourceRuntime,
		TraceID:   *request.TraceID,
		Since:     since,
		Until:     until,
		Limit:     debugger.EvidenceLogMaxLines,
	})
	if err != nil {
		if s.log != nil {
			s.log.Warn("debug evidence logs unavailable", "app_id", appID, "err", err)
		}
		return lines, false
	}
	inputs := make([]debugger.EvidenceLogInput, 0, len(events))
	for _, event := range events {
		inputs = append(inputs, debugger.EvidenceLogInput{
			OccurredAt: event.OccurredAt, Source: string(event.Source),
			Level: event.Level, Stream: event.Stream, Message: event.Message,
		})
	}
	return debugger.EvidenceLogs(inputs), more
}

// debugEvidenceLogWindow bounds the log read for one request. The gateway
// stamps a row when the request finishes, so the request started its latency
// earlier: a stream that ran for ten minutes logged from its start, not from a
// minute before its end (ADR-127 amendment 2026-11-05).
func debugEvidenceLogWindow(request api.DebugTelemetryRequestItem) (time.Time, time.Time, bool) {
	finished, err := time.Parse(time.RFC3339Nano, request.ReceivedAt)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	latency := time.Duration(max(request.LatencyMS, 0)) * time.Millisecond
	return finished.Add(-latency - debugger.EvidenceLogWindowBefore), finished.Add(debugger.EvidenceLogWindowAfter), true
}

// debugRequestsNotRetainedSettle leaves the newest minutes out of the
// comparison: a gateway retries a row the ingest cap turned away, so a
// recent shortfall may still be filled.
const debugRequestsNotRetainedSettle = 2 * time.Minute

// debugRequestsNotRetained says how many requests in the window the app's
// request analytics counted but the debugger did not keep, because the
// plan's per-minute debugger row limit turned them away (ADR-127 amendment
// 2026-11-05). It compares whole minutes inside the window, so it is a lower
// bound, and answers nil when analytics do not cover the window or the store
// cannot tell.
func (s *server) debugRequestsNotRetained(ctx context.Context, appID string, from, until time.Time) *int64 {
	counter, ok := s.store.(state.RequestAnalyticsCounter)
	if !ok {
		return nil
	}
	bucketFrom, bucketUntil, ok := debugRequestsNotRetainedMinutes(from, until)
	if !ok {
		return nil
	}
	missing, covered, err := counter.RequestsNotRetained(ctx, appID, bucketFrom, bucketUntil)
	if err != nil {
		if s.log != nil {
			s.log.Warn("debug coverage: requests not retained unavailable", "app_id", appID, "err", err)
		}
		return nil
	}
	if !covered {
		return nil
	}
	return &missing
}

// debugRequestsNotRetainedMinutes is the whole, settled minutes inside a
// window.
func debugRequestsNotRetainedMinutes(from, until time.Time) (time.Time, time.Time, bool) {
	bucketFrom := from.Truncate(time.Minute)
	if bucketFrom.Before(from) {
		bucketFrom = bucketFrom.Add(time.Minute)
	}
	bucketUntil := until.Add(-debugRequestsNotRetainedSettle).Truncate(time.Minute)
	return bucketFrom, bucketUntil, bucketUntil.After(bucketFrom)
}
