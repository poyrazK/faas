package apidserver

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/debugger"
	"github.com/onebox-faas/faas/pkg/state"
)

// debugEvidenceLogs attaches the log lines one request emitted, matched by
// its trace ID within a bounded window around the request. Like the other
// enrichments it is best-effort: a failed read returns no lines and never
// fails the evidence response.
func (s *server) debugEvidenceLogs(ctx context.Context, accountID, appID string, request api.DebugTelemetryRequestItem) ([]api.DebugEvidenceLogLine, bool) {
	lines := []api.DebugEvidenceLogLine{}
	if request.TraceID == nil || *request.TraceID == "" {
		return lines, false
	}
	received, err := time.Parse(time.RFC3339Nano, request.ReceivedAt)
	if err != nil {
		return lines, false
	}
	events, more, err := s.store.ListLogEvents(ctx, state.LogEventFilter{
		AccountID: accountID,
		AppID:     appID,
		TraceID:   *request.TraceID,
		Since:     received.Add(-debugger.EvidenceLogWindowBefore),
		Until:     received.Add(time.Duration(request.LatencyMS)*time.Millisecond + debugger.EvidenceLogWindowAfter),
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
