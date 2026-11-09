package gateway

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// A WebSocket, server-sent event stream or other long-lived response used to
// be recorded in gateway_request_duration_seconds with its whole lifetime: a
// chat socket open for ten minutes counted as one ten-minute request. Latency
// panels, p95 alerts and latency SLOs then read an app that serves streams
// as slow, and a latency SLO could not be met (ADR-20261109153000). A
// long-lived response's latency is now the time to its response headers:
// how long the client waited for the stream to start. Its request still
// counts once, so request counts and error rates are unchanged.

type responseRecorderKey struct{}

func withResponseRecorder(ctx context.Context, rec *statusRecorder) context.Context {
	return context.WithValue(ctx, responseRecorderKey{}, rec)
}

// observedLatency is the latency observe records for a response that took
// elapsed in all: the time to its headers for a long-lived response, else
// elapsed.
func observedLatency(r *http.Request, status int, elapsed time.Duration) time.Duration {
	if r == nil {
		return elapsed
	}
	rec, _ := r.Context().Value(responseRecorderKey{}).(*statusRecorder)
	if rec == nil || rec.headerAt.IsZero() || !longLivedResponse(rec, status) {
		return elapsed
	}
	if toHeaders := rec.headerAt.Sub(startTime(r)); toHeaders >= 0 && toHeaders < elapsed {
		return toHeaders
	}
	return elapsed
}

// longLivedResponse reports a response that stays open after its headers:
// an upgrade, a forward the edge marked long-lived, or an event stream.
func longLivedResponse(rec *statusRecorder, status int) bool {
	if rec.trafficResponseLongLived != nil && rec.trafficResponseLongLived(status) {
		return true
	}
	mediaType, _, _ := strings.Cut(rec.ContentType, ";")
	return status >= http.StatusOK && status < http.StatusMultipleChoices &&
		strings.EqualFold(strings.TrimSpace(mediaType), "text/event-stream")
}
