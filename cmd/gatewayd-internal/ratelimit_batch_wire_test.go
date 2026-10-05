// adr: 104 — the production central backend batches queued consults.
package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestCentralRateLimitBackendBatches keeps the production backend on the
// coalesced path. Without CentralBatchBackend the gateway falls back to one
// pool connection per request, which starved gatewayd-internal's 8-connection
// pool on production-us.
func TestCentralRateLimitBackendBatches(t *testing.T) {
	backend, _ := buildCentralRateLimitBackend(nil, nil)
	if backend != nil {
		t.Fatalf("nil pool built a backend")
	}
	var central gateway.CentralBackend = state.NewPGRateLimitBackend(nil)
	if _, ok := central.(gateway.CentralBatchBackend); !ok {
		t.Fatal("state.PGRateLimitBackend no longer implements gateway.CentralBatchBackend")
	}
}
