// ratelimit_central_iface_test.go — compile-time assertion that
// state.PGRateLimitBackend implements gateway.CentralBackend
// (ADR-104 amendment 5, issue #881 Phase 4 C3).
// adr: 104
//
// The production CentralBackend lives in pkg/state (alongside
// pgstore.go, sharing the pgxpool). pkg/state does NOT import
// pkg/gateway (the layering is one-way: gateway → state), so the
// compile-time interface check must live in a file that imports
// both. This file is the seam.
//
// Future refactors that break the interface (signature drift,
// field renames) surface here as a vet error, not at boot.
package gateway

import (
	"github.com/onebox-faas/faas/pkg/state"
)

// Compile-time check: *state.PGRateLimitBackend satisfies
// CentralBackend. The struct lives in pkg/state and is wired by
// cmd/gatewayd-internal/run.go iff [ratelimit] mode = "central".
var _ CentralBackend = (*state.PGRateLimitBackend)(nil)

// A second compile-time check via a constructor — the production
// build uses state.NewPGRateLimitBackend; this exercises the
// signature without spinning up a pgxpool.
var _ func() = func() {
	// _ is intentional — nil pool is fine for the iface check.
	_ = state.NewPGRateLimitBackend(nil)
}
