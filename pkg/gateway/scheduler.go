// Scheduler is the seam between the gateway and schedd (spec §4.1, §4.2).
// The only call that crosses this boundary right now is Wake(appID); the
// long-term schedd daemon (M5) provides a gRPC implementation that lives in
// cmd/schedd. Today this file ships:
//
//  1. The Scheduler interface — the method set schedd must implement.
//  2. A fake implementation — returns a configurable deterministic
//     instance address so the gateway remains testable in isolation.
//  3. A noop implementation — wires up when no scheduler is configured
//     (e.g. unit tests that exercise the routing/wake path independently
//     of schedd semantics).
//
// M5 wiring swaps FakeScheduler for the gRPC client (see
// vmmdgrpc/server.go pattern with bufconn tests).
//
// IMPORTANT: The hot-path Backend interface (handler.go) is the existing
// seam and stays untouched in this PR. The Scheduler here is the proper
// shape for M5 to consume — Backend is slated to delegate its Wake method
// to a Scheduler once schedd has a gRPC server. Until then, FakeScheduler
// sits unused but available.
package gateway

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Scheduler is what the gateway needs from schedd. Wake blocks until schedd
// has either admitted + dispatched an instance or decided not to (the request
// is held under the WakeGate cap during this time, spec §4.1).
//
// Implementations should:
//   - respect ctx for cancel propagation (the leader of the WakeGate is
//     detached from the triggering request's ctx).
//   - return an *api.Problem-shaped error so the gateway can map it to the
//     right RFC 7807 status without re-classifying strings.
type Scheduler interface {
	// Wake ensures an instance for appID is running and returns the
	// instance id + compute_node.id it lives on (issue #98 / ADR-028)
	// + the per-wake-attempt correlation handle (gaps analysis
	// 2026-07-23).
	//
	//   - instanceID lets the gateway attribute last_request_at
	//     touches back to the right row (spec §4.1, ADR-018).
	//   - nodeID lets the gateway look up the per-node vmmd gRPC
	//     client in its routing cache and forward via the vmmd
	//     ForwardHTTP RPC.
	//   - wakeID is propagated to the client as x-faas-wake-id. Empty
	//     on the Phase-1 fast-path return where the existing RUNNING
	//     instance was brought up by an earlier wake.
	//
	// The error wraps an *api.Problem so the gateway's writeWakeError
	// can map it directly.
	Wake(ctx context.Context, appID string) (instanceID, nodeID, wakeID string, err error)
}

// ErrSchedulerUnconfigured is returned by NoopScheduler.Wake.
var ErrSchedulerUnconfigured = errors.New("gateway: scheduler not configured (M5)")

// NoopScheduler is the default when nothing is wired — every Wake returns an
// ErrSchedulerUnconfigured. Useful for unit tests that don't need the wake
// path.
type NoopScheduler struct{}

func (NoopScheduler) Wake(context.Context, string) (string, string, string, error) {
	return "", "", "", ErrSchedulerUnconfigured
}

// FakeScheduler is the in-process scheduler used by handler/cmd/gatewayd
// tests. It records every Wake call and returns a stable fake node id
// per app; configurable LatencyMs simulates a cold wake. The "fake
// addr" name is retained as the parameter name for call-site compat
// with older tests — the value flows back through Wake as if it were
// the compute_node id (in tests anything string-shaped works).
type FakeScheduler struct {
	mu         sync.Mutex
	calls      int
	latencyMs  int
	addr       string // reused as the synthetic node id in tests
	instanceID string
	wakeID     string
	errOnWake  error

	// wakesByApp tracks per-app wake counts; useful for the wake-coalesce tests.
	wakesByApp map[string]int
}

func NewFakeScheduler(addr string) *FakeScheduler {
	if addr == "" {
		addr = "node-fake"
	}
	return &FakeScheduler{
		addr:       addr,
		instanceID: "i-fake",
		wakeID:     "wake-fake",
		wakesByApp: map[string]int{},
	}
}

// WithInstanceID sets the instance id Wake returns (default "i-fake").
func (f *FakeScheduler) WithInstanceID(id string) *FakeScheduler {
	f.instanceID = id
	return f
}

// WithWakeID sets the wake id Wake returns (default "wake-fake").
func (f *FakeScheduler) WithWakeID(id string) *FakeScheduler {
	f.wakeID = id
	return f
}

// WithLatency sets the simulated cold-wake latency in milliseconds.
func (f *FakeScheduler) WithLatency(ms int) *FakeScheduler {
	f.latencyMs = ms
	return f
}

// WithErr causes every subsequent Wake to return err (testing failure paths).
func (f *FakeScheduler) WithErr(err error) *FakeScheduler {
	f.errOnWake = err
	return f
}

// Calls returns the number of Wake() calls made (test assertion hook).
func (f *FakeScheduler) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// WakesFor returns the number of wake calls for a specific app.
func (f *FakeScheduler) WakesFor(appID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.wakesByApp[appID]
}

func (f *FakeScheduler) Wake(ctx context.Context, appID string) (string, string, string, error) {
	f.mu.Lock()
	f.calls++
	f.wakesByApp[appID]++
	latency := time.Duration(f.latencyMs) * time.Millisecond
	err := f.errOnWake
	addr := f.addr
	instanceID := f.instanceID
	wakeID := f.wakeID
	f.mu.Unlock()

	if latency > 0 {
		select {
		case <-time.After(latency):
		case <-ctx.Done():
			return "", "", "", ctx.Err()
		}
	}
	return instanceID, addr, wakeID, err
}
