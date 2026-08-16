package main

// CP-1: GET /v1/compute-nodes/events. Operator-facing SSE stream
// of pg_notify frames on the compute_nodes_changed channel. Mirror
// of the dashboard's /v1/events handler but admin-only and
// unfiltered (operators want raw fleet upserts, not per-account
// frames).
//
// Auth chain: authLimited → requireMFA → requireScope(ScopesAdminOnly)
// → adminAllows (in handler). The middleware unwraps the acct
// directly into the handler signature; the eventsHandler pattern
// (cookie + bearer dual-mode) is NOT used here because the
// /v1/compute-nodes routes are admin-only and the dashboard cookie
// path is not exposed to them. The handler signature is the same
// (w, r, acct) as the rest of compute_nodes.go.

import (
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apislogs"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// computeNodeEventsHandler handles GET /v1/compute-nodes/events.
// It opens a per-request subscription to compute_nodes_changed and
// streams each frame as an SSE event. The 15s `:` heartbeat (per
// the dashboard's /v1/events shape) keeps the connection alive
// across idle ticks; clients SHOULD reconnect on disconnect because
// the in-DB channel is fire-and-forget and an idle tail never
// catches up.
func (s *server) computeNodeEventsHandler(w http.ResponseWriter, r *http.Request, acct state.Account) {
	// Auth gate is INSIDE the handler — the chain the middleware
	// ran only proves the caller has the admin scope; the email
	// allowlist is inlined because the existing
	// /v1/compute-nodes routes do the same (compute_nodes.go:74).
	if ok, prob := s.adminAllows(acct); !ok {
		api.WriteProblem(w, prob)
		return
	}

	// SSE connection counter (Move 3 / §12). Increment before the
	// subscribe so the connection-count gauge reflects the wire
	// count even if the subscribe fails. nil-safe — production
	// wires OpsMetrics, but unit tests don't.
	if s.ops != nil {
		s.ops.SSEClients().Inc()
		defer s.ops.SSEClients().Dec()
	}

	apislogs.StartSSE(w)
	flusher, _ := w.(http.Flusher)

	ch, cancel, err := s.notif.Subscribe(r.Context(), []string{db.NotifyComputeNodesChanged})
	if err != nil {
		// Surface the failure on the wire as a single error
		// frame, then close the connection. The dashboard's
		// /v1/events does the same — clients should reconnect
		// on a future poll.
		_, _ = fmt.Fprintf(w, "event: error\ndata: {\"message\":%q}\n\n", err.Error())
		if flusher != nil {
			flusher.Flush()
		}
		return
	}
	defer cancel()

	beat := time.NewTicker(15 * time.Second)
	defer beat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case n, ok := <-ch:
			if !ok {
				return
			}
			writeSSEFrame(w, n)
			if flusher != nil {
				flusher.Flush()
			}
		case <-beat.C:
			_, _ = fmt.Fprint(w, ":\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}
