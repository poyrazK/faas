package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// newCrashCaptureRequestCounter counts ADR-733 5xx capture requests by
// result: requested (a capture was queued), refused (no opt-in, one in
// flight, or cooldown: the normal case) or error (the store call failed).
func newCrashCaptureRequestCounter() *prometheus.CounterVec {
	c := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gatewayd_crash_capture_requests_total",
		Help: "ADR-733 crash capture requests sent after a 5xx, by result (requested, refused, error).",
	}, []string{"result"})
	for _, result := range []string{"requested", "refused", "error"} {
		c.WithLabelValues(result)
	}
	return c
}

// crashCaptureRequester returns the ADR-733 5xx trigger, or nil when crash
// snapshots are not enabled on this node (flag != "1"). A refused request
// (no opt-in, one in flight, cooldown) is the normal case and is silent.
func crashCaptureRequester(store state.CrashCaptureStore, flag string, requests *prometheus.CounterVec) func(context.Context, string, string, int, string) {
	if strings.TrimSpace(flag) != "1" || store == nil {
		return nil
	}
	return func(ctx context.Context, appID, instanceID string, statusCode int, route string) {
		_, err := store.RequestHTTPCrashCapture(ctx, appID, instanceID, statusCode, route, api.CrashCaptureCooldown, time.Now())
		result := "requested"
		switch {
		case errors.Is(err, state.ErrCrashCaptureRefused):
			result = "refused"
		case err != nil:
			result = "error"
		}
		if requests != nil {
			requests.WithLabelValues(result).Inc()
		}
	}
}
