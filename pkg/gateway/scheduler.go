// Scheduler is the seam between the gateway and schedd (spec §4.1, §4.2).
// The only call that crosses this boundary right now is AdmitInstance(appID)
// (issue #168); the long-term schedd daemon (M5) provides a gRPC
// implementation that lives in cmd/schedd. Today this file ships:
//
//  1. The Scheduler interface — the method set schedd must implement.
//  2. A fake implementation — returns configurable deterministic identity
//     values so the gateway remains testable in isolation.
//  3. A noop implementation — wires up when no scheduler is configured
//     (e.g. unit tests that exercise the routing/wake path independently
//     of schedd semantics).
//
// M5 wiring swaps FakeScheduler for the gRPC client (see
// vmmdgrpc/server.go pattern with bufconn tests).
//
// IMPORTANT: The hot-path Backend interface (handler.go) is the existing
// seam and stays untouched in this PR. The Scheduler here is the proper
// shape for M5 to consume — Backend is slated to delegate its Admit method
// to a Scheduler once schedd has a gRPC server. Until then, FakeScheduler
// sits unused but available.
package gateway

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Scheduler is what the gateway needs from schedd. AdmitInstance blocks
// until schedd has either admitted + dispatched a NEW instance or decided
// the app is already at max_concurrency (atCapacity=true).
//
// Implementations should:
//   - respect ctx for cancel propagation (the leader of the WakeGate is
//     detached from the triggering request's ctx).
//   - return an *api.Problem-shaped error so the gateway can map it to the
//     right RFC 7807 status without re-classifying strings.
type Scheduler interface {
	// AdmitInstance attempts to admit ONE additional instance for appID
	// (issue #168). Unlike the legacy Wake primitive, this RPC skips
	// the Phase-1 "return newest RUNNING" shortcut so a gateway can
	// demand a new instance even when others are already running.
	// Three outcomes:
	//
	//   - admitted: instanceID/nodeID/wakeID non-empty, atCapacity=false
	//   - at_capacity: instanceID/nodeID/wakeID empty, atCapacity=true.
	//     The gateway treats this as a benign no-op when it already
	//     has ≥1 cached target.
	//   - failure: non-nil err. Real admission failures (RAM headroom,
	//     chooser, store) travel as *api.Problem. The benign
	//     app_concurrency_reached outcome is NEVER lifted to an error —
	//     it surfaces as atCapacity=true so the gateway can treat it
	//     as a no-op.
	AdmitInstance(ctx context.Context, appID string) (instanceID, nodeID, wakeID string, atCapacity bool, err error)
}

// ErrSchedulerUnconfigured is returned by NoopScheduler.AdmitInstance.
var ErrSchedulerUnconfigured = errors.New("gateway: scheduler not configured (M5)")

// NoopScheduler is the default when nothing is wired — every AdmitInstance
// returns an ErrSchedulerUnconfigured. Useful for unit tests that don't
// need the wake path.
type NoopScheduler struct{}

func (NoopScheduler) AdmitInstance(context.Context, string) (string, string, string, bool, error) {
	return "", "", "", false, ErrSchedulerUnconfigured
}

// FakeScheduler is the in-process scheduler used by handler/cmd/gatewayd
// tests. It records every AdmitInstance call and returns a stable fake
// identity per call; configurable LatencyMs simulates a cold wake.
//
// Identity generation: every call mints a fresh instance id
// (format "i-<seq>") and a stable node id (the `nodeID` field). The wake
// id mirrors the instance id unless WithWakeID overrides it. Tests that
// need a fixed identity set `WithInstanceID`/`WithNodeID` once.
type FakeScheduler struct {
	mu         sync.Mutex
	latencyMs  int
	nodeID     string // stable per-FakeScheduler; reused as the synthetic compute_node.id
	instanceID string // fixed override (default: empty → mint per call)
	wakeID     string // fixed override (default: empty → mirror instance id)
	errOnAdmit error

	// nextID is the per-call instance id counter when instanceID override is unset.
	nextID atomic.Uint64

	// admitsByApp tracks per-app AdmitInstance call counts; useful for the
	// wake-coalesce + multi-instance tests.
	admitsByApp map[string]int
	// totalCalls is the global AdmitInstance counter.
	totalCalls atomic.Uint64
}

func NewFakeScheduler(nodeID string) *FakeScheduler {
	if nodeID == "" {
		nodeID = "node-fake"
	}
	return &FakeScheduler{
		nodeID:      nodeID,
		instanceID:  "", // empty → mint per call
		wakeID:      "", // empty → mirror instance id
		admitsByApp: map[string]int{},
	}
}

// WithInstanceID sets a fixed instance id AdmitInstance returns (default:
// empty → mint "i-N" per call where N is a global sequence counter).
func (f *FakeScheduler) WithInstanceID(id string) *FakeScheduler {
	f.instanceID = id
	return f
}

// WithNodeID overrides the per-FakeScheduler node id (default: the
// constructor argument, defaulting to "node-fake"). Tests that want
// multiple fake nodes construct multiple FakeScheduler instances.
func (f *FakeScheduler) WithNodeID(id string) *FakeScheduler {
	f.mu.Lock()
	f.nodeID = id
	f.mu.Unlock()
	return f
}

// WithWakeID sets a fixed wake id AdmitInstance returns (default: empty
// → mirror the instance id).
func (f *FakeScheduler) WithWakeID(id string) *FakeScheduler {
	f.wakeID = id
	return f
}

// WithLatency sets the simulated cold-wake latency in milliseconds.
func (f *FakeScheduler) WithLatency(ms int) *FakeScheduler {
	f.latencyMs = ms
	return f
}

// WithErr causes every subsequent AdmitInstance to return err (testing failure paths).
func (f *FakeScheduler) WithErr(err error) *FakeScheduler {
	f.errOnAdmit = err
	return f
}

// Calls returns the number of AdmitInstance() calls made (test assertion hook).
func (f *FakeScheduler) Calls() int {
	return int(f.totalCalls.Load())
}

// AdmitsFor returns the number of admit calls for a specific app.
func (f *FakeScheduler) AdmitsFor(appID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.admitsByApp[appID]
}

func (f *FakeScheduler) AdmitInstance(ctx context.Context, appID string) (string, string, string, bool, error) {
	f.mu.Lock()
	f.admitsByApp[appID]++
	latency := time.Duration(f.latencyMs) * time.Millisecond
	err := f.errOnAdmit
	nodeID := f.nodeID
	instanceOverride := f.instanceID
	wakeOverride := f.wakeID
	f.mu.Unlock()

	if latency > 0 {
		select {
		case <-time.After(latency):
		case <-ctx.Done():
			return "", "", "", false, ctx.Err()
		}
	}

	seq := f.nextID.Add(1)
	f.totalCalls.Add(1)

	instanceID := instanceOverride
	if instanceID == "" {
		instanceID = "i-" + itoa(seq)
	}
	wakeID := wakeOverride
	if wakeID == "" {
		wakeID = instanceID
	}
	return instanceID, nodeID, wakeID, false, err
}

// itoa renders a uint64 as a base-10 string without importing strconv into
// the hot-path file. Kept tiny on purpose.
func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
