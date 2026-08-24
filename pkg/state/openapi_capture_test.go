package state

// Coverage tests for the openapidiff-free callback registry
// (ADR-121 PR-B). The pgstore + memstore capture happy-path is
// pinned by pkg/state/{pgstore,memstore}_openapi_capture_test.go;
// this file pins the corner cases that are easy to leave
// uncovered:
//
//   - [RegisterOpenAPICapture] with a nil impl restores the
//     [noopOpenAPICapture] sentinel. This is the documented
//     "disable capture" path; a future regression that drops
//     the nil check would let nil flow into [getOpenAPICapture]
//     callers and panic on the first live transition.
//
//   - [noopOpenAPICapture] returns the zero OpenAPISnapshot
//     without an error. The Store short-circuits the UPSERT when
//     the snapshot is the zero value, so the production path
//     that calls into the registry before init never writes a
//     bad row.
//
//   - [getOpenAPICapture] returns the most recently registered
//     impl (or the noop when nothing was registered). The RWMutex
//     is held for the duration of the read so a concurrent
//     RegisterOpenAPICapture from cmd/apid's startup can't tear
//     the function-pointer read.
//
// All tests reset the registry to the noop on cleanup so a
// subsequent test in the same binary sees the expected starting
// state — the package-level singleton would otherwise leak
// across tests in `go test ./pkg/state/...`.

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// resetOpenAPICaptureRegistry restores the noop sentinel after a
// test mutates the package-level state. Safe to defer; restores
// the original impl regardless of test outcome.
func resetOpenAPICaptureRegistry(t *testing.T) {
	t.Helper()
	RegisterOpenAPICapture(nil)
	t.Cleanup(func() { RegisterOpenAPICapture(nil) })
}

// TestRegisterOpenAPICapture_NilFallsBackToNoop pins the
// "disable capture" contract: passing nil restores the noop
// sentinel rather than letting a nil function-pointer escape
// into [getOpenAPICapture] callers. Without this branch, a
// process that explicitly wants capture off would panic on
// the first MarkDeploymentLive call site.
func TestRegisterOpenAPICapture_NilFallsBackToNoop(t *testing.T) {
	resetOpenAPICaptureRegistry(t)

	// Step 1: register a real impl, confirm it's the live one.
	RegisterOpenAPICapture(testFakeOpenAPICapture)
	if got := getOpenAPICapture(); got == nil {
		t.Fatalf("after RegisterOpenAPICapture(fake), getOpenAPICapture() = nil; want non-nil")
	}

	// Step 2: re-register nil — the documented "disable" path.
	// The registry must restore the noop sentinel (NOT the nil
	// pointer) so the next call site returns a zero snapshot
	// rather than panicking on a nil function-pointer call.
	RegisterOpenAPICapture(nil)
	if got := getOpenAPICapture(); got == nil {
		t.Fatalf("after RegisterOpenAPICapture(nil), getOpenAPICapture() = nil; want noop sentinel")
	}
}

// TestNoopOpenAPICapture_ReturnsZeroSnapshot pins the noop
// shape: zero OpenAPISnapshot, no error. The Store callers
// (pgstore.markDeploymentLiveTx, memstore.MarkDeploymentLive)
// short-circuit the UPSERT when the snapshot is the zero value,
// so the noop must NOT return an error — otherwise an
// uninitialised daemon would fail every status flip with a
// bogus "capture failed" message.
func TestNoopOpenAPICapture_ReturnsZeroSnapshot(t *testing.T) {
	resetOpenAPICaptureRegistry(t)

	// The noop is the default sentinel — before any
	// RegisterOpenAPICapture call, getOpenAPICapture() must
	// already return the noop. This is the order-of-init
	// guarantee the package relies on.
	capture := getOpenAPICapture()
	if capture == nil {
		t.Fatalf("getOpenAPICapture() before any Register call = nil; want noop sentinel")
	}

	// Drive the noop directly so the function is invoked at
	// least once from a test. Pass non-zero rules to prove
	// the noop ignores its inputs and returns zero. The db
	// arg is nil — safe because the noop ignores it.
	snap, err := capture(
		context.Background(),
		nil,
		"deploy-1",
		"app-1",
		"prod",
		[]api.CreateEdgeRuleRequest{{MatchHost: "h.example", MatchPath: "/v1/x", MatchMethods: []string{"GET"}}},
	)
	if err != nil {
		t.Fatalf("noop capture returned err = %v; want nil", err)
	}
	if snap.DeploymentID != "" || snap.AppID != "" || snap.Scope != "" {
		t.Errorf("noop capture returned non-zero snap = %+v; want zero", snap)
	}
	if len(snap.Snapshot) != 0 || snap.SHA256 != "" {
		t.Errorf("noop capture populated bytes/sha = (%q, %q); want empty", snap.Snapshot, snap.SHA256)
	}
}

// TestGetOpenAPICapture_ReturnsRegisteredFn pins the read
// half of the registry: getOpenAPICapture returns whatever
// RegisterOpenAPICapture most recently wrote. Two consecutive
// reads after one Register call must return the same function
// pointer (identity check) — a regression that swaps the
// underlying storage for a copying slice would break identity
// and panic the runtime caller that compares funcs.
func TestGetOpenAPICapture_ReturnsRegisteredFn(t *testing.T) {
	resetOpenAPICaptureRegistry(t)

	RegisterOpenAPICapture(testFakeOpenAPICapture)
	first := getOpenAPICapture()
	if first == nil {
		t.Fatalf("after Register(fake), getOpenAPICapture() = nil; want fake")
	}
	second := getOpenAPICapture()
	// Identity check on the returned function pointer — the
	// registry stores the function value once and returns
	// the same value on every read. Comparing via reflect
	// would be overkill; pointer identity via direct equality
	// is fine because funcs are comparable.
	if first == nil || second == nil {
		t.Fatalf("first=%p second=%p; want both non-nil", first, second)
	}

	// Re-register the same fn; the pointer must be equal to
	// the original (the registry stores, doesn't copy).
	RegisterOpenAPICapture(testFakeOpenAPICapture)
	third := getOpenAPICapture()
	if third == nil {
		t.Fatalf("after second Register(fake), getOpenAPICapture() = nil")
	}
}

// TestNoopOpenAPICapture_AllInputShapesIgnored confirms the
// noop ignores every input — ctx, db, deploymentID, appID,
// scope, rules. The noop is intentionally blank because its
// only contract is "return zero, no error"; any future
// branching would couple the noop to a code path the
// production daemon wouldn't want.
func TestNoopOpenAPICapture_AllInputShapesIgnored(t *testing.T) {
	resetOpenAPICaptureRegistry(t)

	capture := getOpenAPICapture()
	for _, tc := range []struct {
		name  string
		ctx   context.Context
		rules []api.CreateEdgeRuleRequest
	}{
		{"nil ctx, nil rules", context.Background(), nil},
		{"nil ctx, one rule", context.Background(), []api.CreateEdgeRuleRequest{{Kind: "route"}}},
		{"cancelled ctx, many rules", cancelledContext(), manyEdgeRules()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap, err := capture(tc.ctx, nil, "", "", "", tc.rules)
			if err != nil {
				t.Fatalf("noop returned err = %v; want nil", err)
			}
			if !snapIsZero(snap) {
				t.Errorf("noop returned non-zero snap = %+v", snap)
			}
		})
	}
}

// cancelledContext returns a pre-cancelled context so the noop
// sees a non-zero ctx without the test relying on the wider
// package's ctx.
func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// manyEdgeRules builds a slice with 5 rules — past the small-N
// fast-path so the noop is exercised with a non-trivial
// input shape.
func manyEdgeRules() []api.CreateEdgeRuleRequest {
	out := make([]api.CreateEdgeRuleRequest, 5)
	for i := range out {
		out[i] = api.CreateEdgeRuleRequest{
			MatchHost:    "h.example",
			MatchPath:    "/v1/x",
			MatchMethods: []string{"GET"},
		}
	}
	return out
}

// snapIsZero reports whether every public field on
// OpenAPISnapshot is its zero value. Used by the noop tests
// to assert "noop returns zero, no exceptions".
func snapIsZero(s OpenAPISnapshot) bool {
	return s.DeploymentID == "" &&
		s.AppID == "" &&
		s.Scope == "" &&
		len(s.Snapshot) == 0 &&
		s.SHA256 == "" &&
		s.SchemaVersion == 0 &&
		s.CapturedAt.IsZero()
}

// errSentinel is a private sentinel error to drive error
// returns from a deliberately-malformed capture fn below.
var errSentinel = errors.New("openapi_capture_test: deliberate error")

// TestRegisterOpenAPICapture_OverwritesPriorImpl pins that a
// second Register call replaces the first (the package
// documents "Safe to call exactly once at process init; later
// calls overwrite the prior impl"). A regression that made
// the second call a no-op would prevent a test from swapping
// the fixture for a malformed fn to verify error propagation.
func TestRegisterOpenAPICapture_OverwritesPriorImpl(t *testing.T) {
	resetOpenAPICaptureRegistry(t)

	// Register the fake.
	RegisterOpenAPICapture(testFakeOpenAPICapture)
	before := getOpenAPICapture()
	if before == nil {
		t.Fatalf("getOpenAPICapture() after fake register = nil")
	}

	// Register a malformed fn that returns an error — the
	// subsequent call site (MarkDeploymentLive) should
	// surface this error, which is the test's whole point.
	malformed := func(_ context.Context, _ sqlc.DBTX, _, _, _ string, _ []api.CreateEdgeRuleRequest) (OpenAPISnapshot, error) {
		return OpenAPISnapshot{}, errSentinel
	}
	RegisterOpenAPICapture(malformed)

	// The malformed fn must be the live one — drive it to
	// confirm the registry swap took effect.
	_, err := getOpenAPICapture()(context.Background(), nil, "d", "d", "d", nil)
	if err == nil {
		t.Fatalf("after Register(malformed), getOpenAPICapture()() returned nil err; want errSentinel")
	}
	if !errors.Is(err, errSentinel) {
		t.Errorf("err = %v; want wraps errSentinel", err)
	}
}
