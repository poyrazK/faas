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
	rec, _ := r.Context().Value(responseRecorderKey{}).(*statusRecorder)
	if rec == nil || rec.wroteHeader {
		return status
	}
	if errors.Is(r.Context().Err(), context.Canceled) {
		return statusClientClosedRequest
	}
	return status
}
