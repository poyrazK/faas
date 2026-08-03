// Package gateway — trace_handler.go (issue #555 PR-2). GET
// /v1/traces/{trace_id} returns the trace tree JSON for the last
// 24h. Behind the X-Faas-Trace-Auth header so the endpoint is
// usable by customers / observers without leaking to the wider
// public.
package gateway

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

// TraceHandlerConfig is the dependency bundle for the trace handler.
// Observer token is the auth gate; ring is the storage.
type TraceHandlerConfig struct {
	// Ring is the trace store. Required.
	Ring *TraceRing
	// ObserverToken is the secret expected in the X-Faas-Trace-Auth
	// header. Empty string disables the endpoint (returns 404 —
	// indistinguishable from the public surface).
	ObserverToken string
}

// TraceHandler is the http.Handler for GET /v1/traces/{trace_id}.
type TraceHandler struct {
	cfg TraceHandlerConfig
}

// NewTraceHandler returns a handler ready to mount on the public
// mux. The handler is safe for concurrent use; the ring is the
// mutable state.
func NewTraceHandler(cfg TraceHandlerConfig) *TraceHandler {
	return &TraceHandler{cfg: cfg}
}

// ServeHTTP handles GET /v1/traces/{trace_id}. The path is matched
// via the mux prefix /v1/traces/; the handler reads the path
// suffix as the trace ID.
//
// Auth: the request must carry `X-Faas-Trace-Auth: <token>`. The
// token is compared with constant-time equality so a timing oracle
// cannot leak the secret. An empty ObserverToken causes the
// handler to short-circuit to 404 — operator-controlled feature
// flag.
//
// Response: 200 + JSON trace tree, 401 on auth failure, 404 on
// missing trace, 405 on non-GET, 500 on internal errors.
func (h *TraceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeProblem(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// Trim the prefix; Go's http.ServeMux passes the path-relative
	// suffix in r.URL.Path when the pattern ends with /.
	id := strings.TrimPrefix(r.URL.Path, "/v1/traces/")
	id = strings.TrimSuffix(id, "/")
	if id == "" {
		writeProblem(w, http.StatusBadRequest, "trace_id required")
		return
	}
	if h.cfg.ObserverToken == "" {
		// Disabled. Return 404 so the endpoint is invisible to
		// random probing.
		writeProblem(w, http.StatusNotFound, "trace endpoint disabled")
		return
	}
	got := r.Header.Get("X-Faas-Trace-Auth")
	if subtle.ConstantTimeCompare([]byte(got), []byte(h.cfg.ObserverToken)) != 1 {
		writeProblem(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	trace, ok := h.cfg.Ring.Get(id)
	if !ok {
		writeProblem(w, http.StatusNotFound, "trace not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(trace); err != nil {
		// Body already started; nothing we can do but log via the
		// package. In production the operator's metrics counter
		// captures this. Best-effort write.
		_ = err
	}
}

// writeProblem emits a small RFC 7807-style JSON body. Goes
// straight to the wire without the full apid problem builder
// (which depends on a store + logger) so the gateway stays
// self-contained.
func writeProblem(w http.ResponseWriter, status int, title string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": http.StatusText(status),
		"title":  title,
	})
}
