package gateway

import (
	"context"
	"errors"
	"net/http"
)

// statusClientClosedRequest is nginx's 499: the client went away before any
// response was written. It counts as a client error, never as a success or a
// server error.
const statusClientClosedRequest = 499

// observedStatus is the status observe records. The response recorder starts
// at 200, so a request the client abandoned before the app answered (a
// browser navigating away, a synthetic check timing out) was recorded as a
// successful 200 with the whole wait as its latency, and counted as good in
// error rates and SLOs (ADR-20261110090000). Such a request is recorded as
// 499 instead. A response that was written keeps its status.
func observedStatus(r *http.Request, status int) int {
	if r == nil {
		return status
	}
	if status == http.StatusOK && r.Context().Value(mcpPlanRefusedKey{}) == true {
		// A plan refusal answered in JSON-RPC is the 429 it replaces
		// (ADR-20261125230541): metrics, telemetry and diagnostics agree.
		return http.StatusTooManyRequests
	}
	rec, _ := r.Context().Value(responseRecorderKey{}).(*statusRecorder)
	if rec != nil && rec.responseTruncated {
		// The body was cut at the cap after the status went out
		// (truncated_response_telemetry.go): the client never got this response.
		return http.StatusBadGateway
	}
	if rec != nil && rec.ResponseWriter != nil && rec.wroteHeader && status == http.StatusOK {
		if mapped, ok := grpcObservedStatus(rec.Header()); ok {
			return mapped
		}
	}
	if rec == nil || rec.wroteHeader {
		return status
	}
	if errors.Is(r.Context().Err(), context.Canceled) {
		return statusClientClosedRequest
	}
	return status
}
