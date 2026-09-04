// Scheduler is the seam between the gateway and schedd (spec §4.1, §4.2).
// The single call that crosses this boundary is AdmitInstance(appID)
// (issue #168); schedd's gRPC implementation lives in pkg/scheddgrpc
// (ADR-018). This file ships:
//
//  1. The Scheduler interface — the method set schedd must implement.
//     PGBackend holds a Scheduler and Backend.Admit delegates to it
//     (pkg/gateway/pgbackend.go).
//  2. FakeScheduler — an in-process scheduler that returns configurable
//     deterministic identity values. pgbackend_test.go uses it to
//     exercise multi-instance + eviction flows without standing up
//     schedd.
//  3. NoopScheduler — wires up when no scheduler is configured (e.g.
//     unit tests that exercise the routing/wake path independently
//     of schedd semantics).
package gateway

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// WakeMethod is the narrow gateway-facing mirror of the on-the-wire
// scheddpb.WakeMethod (PR scale-out readiness). The Scheduler
// implementations translate the protobuf enum into this type so the
// gateway code never reaches into the protobuf package directly. The
// closed set is intentionally small: only the two outcomes that affect
// the wake-locality classifier. Any unknown wire value maps to
// WakeMethodColdBoot — the safer (slow but always-correct) fallback,
// matching scheddgrpc.mapMethod's default branch.
type WakeMethod int

// Wire-shape int32 values for WakeMethod. Defined next to the type so
// the wake-method switching in this file and the proto enum in
// api/proto/onebox/faas/schedd/v1/schedd.proto:132-135 share a single
// source of truth. The proto file is the authority — TestWireWakeMethod
// in scheduler_test.go asserts these constants match the proto enum
// numbers so a future proto reordering trips CI instead of silently
// regressing the wake-locality classifier.
//
// Exported so tests in this package can reference the wire values by
// name (e.g. `gateway.WireWakeRestore`) instead of magic numbers. The
// package's runtime code does not need to cross an import boundary to
// use them.
const (
	WireWakeColdBoot int32 = 0
	WireWakeRestore  int32 = 1
)

const (
	// WakeMethodUnspecified is the zero value; it represents the
	// outcome schedd surfaces when a request takes the Phase-1
	// fast-path (returning an already-RUNNING instance) and no fresh
	// admit happened. The gateway should never observe this on the
	// admitted path; it is here so the type is zero-value-safe.
	WakeMethodUnspecified WakeMethod = iota
	// WakeMethodSnapshotRestore — the admitted instance was restored
	// from a snapshot.
	WakeMethodSnapshotRestore
	// WakeMethodColdBoot — the admitted instance was cold-booted
	// (no usable snapshot, snapshot restore failed, or fall-back
	// path).
	WakeMethodColdBoot
)

// String renders the outcome in the metric-label form. The closed
// set keeps the gateway_wake_locality_total{outcome} cardinality
// bounded.
//
// The translation from raw wire int32 to WakeMethod happens in
// scheddWakeMethodToGateway (below) before this switch is reached,
// so the default branch is unreachable in normal flow. It is
// belt-and-braces: a future WakeMethod addition that the switch
// misses still renders as "local_coldboot" rather than minting a
// frozen unexpected label tuple.
func (m WakeMethod) String() string {
	switch m {
	case WakeMethodSnapshotRestore:
		return "local_snapshot"
	case WakeMethodColdBoot:
		return "local_coldboot"
	default:
		// WakeMethodUnspecified, plus any future addition that
		// drifts from this switch → local_coldboot.
		return "local_coldboot"
	}
}

// WakeMethodFromSchedd is the translation seam from the protobuf
// scheddpb.WakeMethod into the gateway type. Exposed as a function
// rather than a method so callers don't need to import the protobuf
// package; pkg/scheddgrpc.Client.AdmitInstance uses this internally.
//
// Wire values:
//   - WAKE_RESTORE      → WakeMethodSnapshotRestore
//   - WAKE_COLD_BOOT    → WakeMethodColdBoot
//   - everything else   → WakeMethodColdBoot (default branch)
//
// Mirrors scheddgrpc.mapMethod's default-to-cold-boot defense.
func scheddWakeMethodToGateway(m int32) WakeMethod {
	// The closed enum has exactly two values (WireWakeColdBoot = 0,
	// WireWakeRestore = 1 in the proto). Anything outside falls
	// through to cold boot — same logic as the server-side
	// mapMethod in pkg/scheddgrpc/server.go.
	if m == WireWakeRestore {
		return WakeMethodSnapshotRestore
	}
	return WakeMethodColdBoot
}

// tierFromWakeMethod (issue #470 / PR #470-FU-B) maps the
// existing WakeMethod to the closest snapshot-tier label for
// the gateway_wake_snapshot_tier_total counter. Today
// WakeMethod is a 2-value enum (snapshot vs cold-boot); the
// tier refinement is a 3-value set (warm, init, cold). The
// bridge is coarser than the eventual PR #470-FU-A feed
// (which will thread the real tier through the Admit
// response) but it ensures the counter has a non-zero baseline
// before PR A lands. Mapping:
//
//   - WakeMethodSnapshotRestore → "init"
//     (today every restored snapshot is init; warm is a
//     refinement that PR A will surface as a distinct value)
//   - WakeMethodColdBoot → "cold"
//   - WakeMethodUnspecified → "init" (zero-value fallback)
//
// This is a deliberate temporary bridge. PR #470-FU-A
// replaces the call site with the engine's actual tier field
// — the metric's empty-string fallback in
// ObserveWakeSnapshotTier covers the transition seam.
func tierFromWakeMethod(m WakeMethod) string {
	switch m {
	case WakeMethodSnapshotRestore:
		return "init"
	case WakeMethodColdBoot:
		return "cold"
	default:
		// WakeMethodUnspecified and any future addition — the
		// counter's empty-string fallback ("init") is the safe
		// default; mirror it here.
		return "init"
	}
}

// Scheduler is what the gateway needs from schedd. AdmitInstance blocks
// until schedd has either admitted + dispatched a NEW instance or decided
// the app is already at max_concurrency (atCapacity=true).
//
// Implementations should:
//   - respect ctx for cancel propagation (the leader of the WakeGate is
//     detached from the triggering request's ctx).
//   - return an *api.Problem-shaped error so the gateway can map it to the
//     right RFC 7807 status without re-classifying strings.
//
// method is the raw wire value schedd returned (PR scale-out readiness).
// The translation to WakeMethod happens in pgbackend.go via
// scheddWakeMethodToGateway so this package doesn't have to depend on
// the protobuf package — the same boundary scheddgrpc.Client already
// crosses.
//
// deploymentID (issue #556 / PR-B) is the live deployment id the new
// instance was admitted for. The gateway caches it on Target so the
// per-deployment weighted picker (PGBackend.Pick) routes subsequent
// requests to the right deployment bucket. Empty on the at-capacity
// path; "" pre-PR-B for any caller that hasn't been refreshed to
// thread the field — the gateway treats empty as "single-deployment
// legacy mode" (Target.DeploymentID empty, picker collapses to
// today's behaviour).
type Scheduler interface {
	// AdmitInstance attempts to admit ONE additional instance for appID
	// (issue #168). Unlike the legacy Wake primitive, this RPC skips
	// the Phase-1 "return newest RUNNING" shortcut so a gateway can
	// demand a new instance even when others are already running.
	// Three outcomes:
	//
	//   - admitted: instanceID/nodeID/deploymentID/wakeID non-empty,
	//     atCapacity=false, method reflects what schedd actually did
	//     (1 = restore, 0/2+ = cold boot, see scheddWakeMethodToGateway).
	//   - at_capacity: instanceID/nodeID/deploymentID/wakeID empty,
	//     atCapacity=true. The gateway treats this as a benign no-op
	//     when it already has ≥1 cached target. method is 0.
	//   - failure: non-nil err. Real admission failures (RAM headroom,
	//     chooser, store) travel as *api.Problem. The benign
	//     app_concurrency_reached outcome is NEVER lifted to an error —
	//     it surfaces as atCapacity=true so the gateway can treat it
	//     as a no-op. method is 0.
	// deploymentID (issue #556 / PR-C): the optional
	// per-deployment wake hint for the wake-fan-out path. Empty
	// falls through to schedd's default (newest live deployment)
	// — the legacy single-deployment behaviour. Non-empty asks
	// schedd to admit on that specific live deployment. Additive
	// per ADR-016.
	//
	// scope (issue #272 / ADR-095 / PR-B): the preview scope
	// (`pr-{N}`) the gateway parsed from the inbound Host header.
	// Empty = prod (legacy single-deployment behaviour). Threaded
	// through schedd's WakeRequest.scope wire field.
	//
	// trigger (ADR-127): the wake-boot trigger enum value forwarded
	// to schedd's AdmitInstance RPC and stamped on the emitted
	// wake.boot_started / wake.boot_completed events. The gateway
	// always passes "gateway" (pkg/sched.TriggerGateway); future
	// gateway-side caller surfaces (synth handler, replay worker)
	// can pass a distinct closed-enum value.
	AdmitInstance(ctx context.Context, appID, deploymentID, scope, trigger string) (instanceID, nodeID, deploymentIDOut, wakeID string, method int32, atCapacity bool, port int, err error)
	// EnsureWake (ADR-098) is the schedd-side single-flight wake
	// entry. Schedd coalesces every concurrent EnsureWake for the
	// same app into one virtual boot; followers see the leader's
	// outcome. Pre-ADR-098 callers continue to use Wake / AdmitInstance
	// on the legacy wire — this method is additive per ADR-016.
	//
	// On the at-cap path the leader's ledger returns an admitted
	// row pointing at the last live slot; the WakeGate pre-filter
	// above (an in-process cache) keeps the RPC from firing when
	// the gateway already has a live instance cached for the app.
	//
	// trigger (ADR-127): forwarded to schedd's leader so the
	// emitted wake.boot_started / wake.boot_completed events stamp
	// the cause (gateway / floor / cron / scaleup / etc.).
	EnsureWake(ctx context.Context, appID, trigger string) (instanceID, nodeID, deploymentIDOut, wakeID string, method int32, port int, err error)
	// AdmitMirrorInstance (issue #72 / ADR-124 / ADR-125 PR-A3) is
	// the mirror-VM admission sibling to AdmitInstance. Schedd
	// stamps mode='mirror' on the new instances row (PR-A1's 00385)
	// and the per-rule concurrent-mirror-VM cap (default 5,
	// sched.MirrorMaxConcurrentPerRule) gates the dispatch.
	//
	// Outcomes:
	//   - admitted:    wakeID + instanceID non-empty, err=nil
	//   - cap-at-max:  wakeID + instanceID empty, err wraps
	//                  sched.ErrMirrorSlotAtCapacity — gateway maps
	//                  to ledger row with status_diff=true +
	//                  metric result="cap_at_max".
	//   - real failure: err is *api.Problem-shaped; gateway treats
	//                   as a real failure (no ledger row, just log).
	AdmitMirrorInstance(ctx context.Context, appID, mirrorDeploymentID, mirrorRuleID string) (instanceID, wakeID string, err error)
}

// burstScheduler is an optional extension implemented by the production
// schedd client. The callback shape keeps the gateway package independent of
// schedd protobuf result types while preserving all fields needed to cache a
// target. Implementations must call report once for the first admission and
// once for each bounded continuation, including an error result.
type burstScheduler interface {
	AdmitInstances(ctx context.Context, appID, scope, trigger string, count int, report func(instanceID, nodeID, deploymentID, wakeID string, method int32, atCapacity bool, port int, err error)) error
}

// ErrSchedulerUnconfigured is returned by NoopScheduler.AdmitInstance.
var ErrSchedulerUnconfigured = errors.New("gateway: scheduler not configured (M5)")

// NoopScheduler is the default when nothing is wired — every AdmitInstance
// returns an ErrSchedulerUnconfigured. Useful for unit tests that don't
// need the wake path.
type NoopScheduler struct{}

func (NoopScheduler) AdmitInstance(context.Context, string, string, string, string) (string, string, string, string, int32, bool, int, error) {
	return "", "", "", "", 0, false, 0, ErrSchedulerUnconfigured
}

func (NoopScheduler) EnsureWake(context.Context, string, string) (string, string, string, string, int32, int, error) {
	return "", "", "", "", 0, 0, ErrSchedulerUnconfigured
}

// AdmitMirrorInstance (issue #72 / ADR-124 PR-A3) — stub matches
// NoopScheduler's "no scheduler wired" contract: returns
// ErrSchedulerUnconfigured so the mirror goroutine logs + drops
// the request without writing a misleading ledger row.
func (NoopScheduler) AdmitMirrorInstance(context.Context, string, string, string) (string, string, error) {
	return "", "", ErrSchedulerUnconfigured
}

// FakeScheduler is the in-process scheduler used by handler/cmd/gatewayd-internal/
// tests. It records every AdmitInstance call and returns a stable fake
// identity per call; configurable LatencyMs simulates a cold wake.
//
// Identity generation: every call mints a fresh instance id
// (format "i-<seq>") and a stable node id (the `nodeID` field). The wake
// id mirrors the instance id unless WithWakeID overrides it. Tests that
// need a fixed identity set `WithInstanceID`/`WithNodeID` once.
//
// WakeMethod defaults to WakeMethodColdBoot (the gateway-side
// expectation of a fresh admission); tests that exercise the
// snapshot-restore path use WithWakeMethod.
type FakeScheduler struct {
	mu         sync.Mutex
	latencyMs  int
	nodeID     string // stable per-FakeScheduler; reused as the synthetic compute_node.id
	instanceID string // fixed override (default: empty → mint per call)
	wakeID     string // fixed override (default: empty → mirror instance id)
	method     WakeMethod
	errOnAdmit error

	// nextID is the per-call instance id counter when instanceID override is unset.
	nextID atomic.Uint64

	// port (PR-C, issue #460 / ADR-053) is the per-deployment
	// override port the fake scheduler returns. 0 = legacy 8080
	// (vmmd wire boundary default). Set via WithPort.
	port int

	// deploymentID (issue #556 / PR-B) is the deployment id the fake
	// scheduler returns. Default: empty (the gateway treats that as
	// "single-deployment legacy mode" — see Target.DeploymentID doc).
	// Set via WithDeploymentID for tests that exercise the
	// per-deployment weighted picker.
	deploymentID string

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
		method:      WakeMethodColdBoot,
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

// WithWakeMethod sets the wake-method AdmitInstance returns (default:
// WakeMethodColdBoot on construction). Tests that exercise the
// restore-outcome path of the wake-locality classifier use this.
func (f *FakeScheduler) WithWakeMethod(m WakeMethod) *FakeScheduler {
	f.mu.Lock()
	f.method = m
	f.mu.Unlock()
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

// WithPort (PR-C, issue #460 / ADR-053) sets the per-deployment
// override port the fake scheduler reports on AdmitInstance. 0
// (the default) preserves the legacy wire shape — vmmd's bridge
// defaults 0 to netns.AppPort (8080) at the server boundary.
func (f *FakeScheduler) WithPort(p int) *FakeScheduler {
	f.port = p
	return f
}

// WithDeploymentID (issue #556 / PR-B) sets the deployment id the
// fake scheduler reports on AdmitInstance. Default is empty — the
// gateway treats that as "single-deployment legacy mode" (Target.
// DeploymentID is empty; the per-deployment picker collapses to
// today's behaviour). Tests that exercise multi-deployment splits
// set WithDeploymentID per-admit.
func (f *FakeScheduler) WithDeploymentID(id string) *FakeScheduler {
	f.deploymentID = id
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

func (f *FakeScheduler) AdmitInstance(ctx context.Context, appID, deploymentIDHint, scope, trigger string) (string, string, string, string, int32, bool, int, error) {
	f.mu.Lock()
	f.admitsByApp[appID]++
	latency := time.Duration(f.latencyMs) * time.Millisecond
	err := f.errOnAdmit
	nodeID := f.nodeID
	instanceOverride := f.instanceID
	wakeOverride := f.wakeID
	method := f.method
	// PR-C (issue #460 / ADR-053): per-deployment override port.
	// Defaults to 0 (legacy 8080 at the wire boundary); tests that
	// need a non-default port set WithPort.
	port := f.port
	// PR-B (issue #556): per-deployment id the gateway caches on
	// Target. Defaults to empty (legacy single-deployment mode);
	// tests that exercise the picker set WithDeploymentID.
	deploymentID := f.deploymentID
	f.mu.Unlock()

	if latency > 0 {
		select {
		case <-time.After(latency):
		case <-ctx.Done():
			return "", "", "", "", 0, false, 0, ctx.Err()
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
	// Translate the typed WakeMethod to the wire-shape int32 the
	// Scheduler interface returns. The mapping mirrors the protobuf
	// wire values (WAKE_COLD_BOOT = 0, WAKE_RESTORE = 1) so a test
	// that sets WithWakeMethod(WakeMethodSnapshotRestore) reaches
	// pgbackend.go's scheddWakeMethodToGateway as WireWakeRestore
	// and resolves to WakeMethodSnapshotRestore on the gateway side.
	var rawMethod int32
	switch method {
	case WakeMethodSnapshotRestore:
		rawMethod = WireWakeRestore
	default:
		rawMethod = WireWakeColdBoot
	}
	return instanceID, nodeID, deploymentID, wakeID, rawMethod, false, port, err
}

// EnsureWake (ADR-098): the FakeScheduler isn't a real single-flight
// coalescer — every call returns a fresh identity, which is what
// handler unit tests want (they're not exercising the schedd-side
// leader/follower contract; the property-based test at C5 pins that
// on the Engine side). The Scheduler interface keeps the WakeGate
// pre-filter layer intact, so concurrent handler calls still
// coalesce in-process.
func (f *FakeScheduler) EnsureWake(ctx context.Context, appID, trigger string) (string, string, string, string, int32, int, error) {
	f.mu.Lock()
	f.admitsByApp[appID]++
	latency := time.Duration(f.latencyMs) * time.Millisecond
	err := f.errOnAdmit
	nodeID := f.nodeID
	instanceOverride := f.instanceID
	wakeOverride := f.wakeID
	method := f.method
	port := f.port
	deploymentID := f.deploymentID
	f.mu.Unlock()

	if latency > 0 {
		select {
		case <-time.After(latency):
		case <-ctx.Done():
			return "", "", "", "", 0, 0, ctx.Err()
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
	var rawMethod int32
	switch method {
	case WakeMethodSnapshotRestore:
		rawMethod = WireWakeRestore
	default:
		rawMethod = WireWakeColdBoot
	}
	return instanceID, nodeID, deploymentID, wakeID, rawMethod, port, err
}

// AdmitMirrorInstance (issue #72 / ADR-124 PR-A3) is the in-process
// mirror test fake. Returns synthetic identity (mirrors
// AdmitInstance's per-call sequence), or errOnAdmit if WithErr is
// set. The wakeID and instanceID match AdmitInstance's shape so
// tests exercising the mirror hot path can pin a single fixed
// identity by calling WithWakeID/WithInstanceID once.
func (f *FakeScheduler) AdmitMirrorInstance(ctx context.Context, appID, mirrorDeploymentID, mirrorRuleID string) (string, string, error) {
	f.mu.Lock()
	f.admitsByApp[appID]++
	latency := time.Duration(f.latencyMs) * time.Millisecond
	err := f.errOnAdmit
	instanceOverride := f.instanceID
	wakeOverride := f.wakeID
	f.mu.Unlock()

	if latency > 0 {
		select {
		case <-time.After(latency):
		case <-ctx.Done():
			return "", "", ctx.Err()
		}
	}

	seq := f.nextID.Add(1)
	f.totalCalls.Add(1)

	instanceID := instanceOverride
	if instanceID == "" {
		instanceID = "mirror-" + itoa(seq)
	}
	wakeID := wakeOverride
	if wakeID == "" {
		wakeID = instanceID
	}
	return instanceID, wakeID, err
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
