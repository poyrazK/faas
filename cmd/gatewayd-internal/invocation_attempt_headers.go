package main

import (
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Apply after persisted headers and platform identity. Headers cannot author counters.
func applyInvocationAttemptHeaders(headers http.Header, inv state.Invocation) {
	for key := range headers {
		if api.IsInvocationAttemptHeader(key) {
			delete(headers, key)
		}
	}
	if (inv.Source != state.InvocationAsyncInvoke && inv.Source != state.InvocationReplay) || inv.Attempts <= 0 || inv.ReplayGeneration < 0 {
		return
	}
	headers.Set(api.InvocationAttemptHeader, strconv.Itoa(inv.Attempts))
	headers.Set(api.InvocationReplayGenerationHeader, strconv.FormatInt(inv.ReplayGeneration, 10))
	if inv.ReplayedFromInvocationID != "" {
		headers.Set(api.InvocationReplayedFromHeader, inv.ReplayedFromInvocationID)
	}
	if inv.ReplayRootInvocationID != "" {
		headers.Set(api.InvocationReplayRootHeader, inv.ReplayRootInvocationID)
	}
}
