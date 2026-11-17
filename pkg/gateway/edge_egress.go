package gateway

import "net/http"

// recordEdgeEgress meters n body bytes the gateway served for app without an
// instance: a response-cache hit, a stale serve or a fixed edge response
// (ADR-20261104132753). meterd counts them as the app's egress, so they reach
// the plan's monthly egress ceiling and its overage like an instance's own
// bytes. A HEAD response sends no body and records nothing.
func (h *Handler) recordEdgeEgress(r *http.Request, app App, n int) {
	if h == nil || h.egressSink == nil || app.ID == "" || n <= 0 || (r != nil && r.Method == http.MethodHead) {
		return
	}
	h.egressSink.RecordEdgeBytes(app.ID, int64(n))
}
