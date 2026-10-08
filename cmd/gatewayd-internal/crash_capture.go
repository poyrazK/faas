package main

import (
	"context"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// crashCaptureRequester returns the ADR-733 5xx trigger, or nil when crash
// snapshots are not enabled on this node (flag != "1"). A refused request
// (no opt-in, one in flight, cooldown) is the normal case and is silent.
func crashCaptureRequester(store state.CrashCaptureStore, flag string) func(context.Context, string, string, int, string) {
	if strings.TrimSpace(flag) != "1" || store == nil {
		return nil
	}
	return func(ctx context.Context, appID, instanceID string, statusCode int, route string) {
		_, _ = store.RequestHTTPCrashCapture(ctx, appID, instanceID, statusCode, route, api.CrashCaptureCooldown, time.Now())
	}
}
