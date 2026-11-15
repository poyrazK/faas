package gateway

import (
	"net/http"
)

// A response that passes the plan's body cap after its status went out
// cannot become a 413 any more: the gateway aborts the stream so the client
// sees a transport error (Hunt 11 H11-36). It used to be recorded as the 200
// that was sent, with the time to first byte as its latency, so request logs,
// error rates and alerts showed a success the client never got (prod CLI e2e
// 2026-11-15 #10). Such a request is recorded as 502, its log line carries
// response_truncated=true and the status that was sent, and its latency runs
// to the abort.

// markResponseTruncated records that the response w writes was cut at the
// body cap after commit. w is the writer the cap wraps; the request's
// statusRecorder is found by unwrapping it.
func markResponseTruncated(w http.ResponseWriter) {
	for w != nil {
		if rec, ok := w.(*statusRecorder); ok {
			rec.responseTruncated = true
			return
		}
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return
		}
		w = u.Unwrap()
	}
}

// truncatedResponse returns the request's recorder when its response was
// cut at the body cap after commit, else nil.
func truncatedResponse(r *http.Request) *statusRecorder {
	if r == nil {
		return nil
	}
	rec, _ := r.Context().Value(responseRecorderKey{}).(*statusRecorder)
	if rec == nil || !rec.responseTruncated {
		return nil
	}
	return rec
}
