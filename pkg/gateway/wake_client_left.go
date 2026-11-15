package gateway

import (
	"context"
	"errors"
	"net/http"
)

// clientLeftDuringWake answers a wake error when the client had already gone
// (a browser that navigated away, an edge that gave up). The wake itself is
// detached and keeps booting for the next request, so this is not a wake
// failure: it is recorded as 499, never written as a 503, and never marks the
// app's health as failed. On 2026-11-13 a 6 s restore (an evicted snapshot
// drive fetched from GCS) made a visitor give up after 2.75 s; the gateway
// logged "wake failed", recorded a 503 and marked retail-ui unhealthy, while
// the same wake served the next request 2 s later (ADR-20261115140000).
func (h *Handler) clientLeftDuringWake(r *http.Request, app App) bool {
	if r == nil || !errors.Is(r.Context().Err(), context.Canceled) {
		return false
	}
	// A traffic revocation also cancels the request; the client is still
	// there and must get the revocation's 403 problem, not a silent 499.
	if trafficRevocationCause(r.Context()) != nil {
		return false
	}
	if h.log != nil {
		h.log.Info("gateway: client left during wake; the wake continues", "app_id", app.ID)
	}
	h.observe(r, statusClientClosedRequest, app.ID, string(app.Plan), false, Target{})
	return true
}
