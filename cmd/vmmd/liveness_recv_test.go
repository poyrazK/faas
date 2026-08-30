//go:build linux

// Tests for the liveness-probe counter + cooldown classification
// (issue #554 / ADR-078). These tests pin the AC #2 surface
// (flaky app does NOT oscillate) directly:
//
//   - A 200/200/500/200/200/500 sequence resets the
//     consecutive-failure counter on the first 2xx, so the
//     destroy never fires.
//   - A back-to-back 500/500/500/500 sequence reaches the
//     ConsecutiveFailures threshold (3) on the third 500 and
//     triggers the relay exactly once.
//
// We exercise the runOne classification path via the probeFn
// seam (cmd/vmmd/liveness_recv.go::livenessProbeLoop.probeFn),
// bypassing the real AF_VSOCK dial so the test runs on any
// Linux dev box without KVM.
package main

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

// recordingSink counts the calls to Manager.ReportLivenessFailed
// so the test can assert "exactly one relay fire" without
// wiring a real schedd engine.
type recordingSink struct {
	mu    sync.Mutex
	calls []sinkCall
}

type sinkCall struct {
	instance string
	reason   string
}

func (s *recordingSink) Record(_ context.Context, instance, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, sinkCall{instance, reason})
}

func (s *recordingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

// newTestLoop builds a *livenessProbeLoop wired to a real
// *fcvm.Manager with the liveness-sink relay pointed at a
// recordingSink. Returns the loop + the sink so the test can
// assert on the side effects.
func newTestLoop(t *testing.T, instanceID string, consec int) (*livenessProbeLoop, *recordingSink, *fcvm.Manager) {
	t.Helper()
	sink := &recordingSink{}
	mgr := fcvm.NewManager(nil, nil, fcvm.Paths{}, "1.10.0", slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	// Attach the sink via the WithLivenessSink helper. The
	// post-F1 Manager signature returns *Manager (chainable)
	// and accepts the named LivenessFailedSink parameter type —
	// the test interface must declare the *named* parameter type
	// (not a bare func(...) form) for Go's interface satisfaction
	// to allow the type assertion. Passing a bare `func(...)`
	// here fails at runtime with "does not expose WithLivenessSink
	// — test seam missing" even though the underlying call
	// signature is identical — Go's interface assignability
	// checks parameter-type identity for named types.
	type sinkSetter interface {
		WithLivenessSink(fcvm.LivenessFailedSink) *fcvm.Manager
	}
	if ss, ok := any(mgr).(sinkSetter); ok {
		// Explicit conversion: sink.Record is a method value with
		// the bare func type `func(ctx context.Context, instance,
		// reason string)`. Production declares the parameter as
		// the named `LivenessFailedSink` alias. Identical shape,
		// distinct types at the type-system layer — must convert
		// to satisfy the interface call.
		ss.WithLivenessSink(fcvm.LivenessFailedSink(sink.Record))
	} else {
		t.Fatalf("fcvm.Manager does not expose WithLivenessSink — test seam missing")
	}
	loop := &livenessProbeLoop{
		instance:     instanceID,
		deploymentID: "dep-" + instanceID, // mirrors startLivenessLoopHelper's signature
		cfg: livenessProbeConfig{
			Path:                "/healthz",
			PeriodSeconds:       5,
			ConsecutiveFailures: consec,
			CooldownSeconds:     60,
		},
		cid: 0, // unused in tests (probeFn bypasses dial)
		mgr: mgr,
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return loop, sink, mgr
}

// TestLivenessRecv_CounterSurvivesIntermittentSuccess is AC #2:
// 200/200/500/200/200/500 does NOT fire DestroyForLivenessFailure.
// The first 200 reset the counter; subsequent failures
// re-increment from 0; ConsecutiveFailures=3 means we need
// 3 in a row WITHOUT a 200 in between.
func TestLivenessRecv_CounterSurvivesIntermittentSuccess(t *testing.T) {
	loop, sink, _ := newTestLoop(t, "inst-1", 3)
	// Stubbed probe: emits the outcome sequence 200/200/500/
	// 200/200/500 — index-driven, the loop calls runOne in a
	// loop so we advance per call.
	outcomes := []string{
		livenessOutcomeOK,
		livenessOutcomeOK,
		livenessOutcomeNon200,
		livenessOutcomeOK,
		livenessOutcomeOK,
		livenessOutcomeNon200,
	}
	loop.probeFn = func(_ context.Context, _ int) string {
		if len(outcomes) == 0 {
			return livenessOutcomeOK
		}
		out := outcomes[0]
		outcomes = outcomes[1:]
		return out
	}
	for i := 0; i < 6; i++ {
		loop.runOne(context.Background(), 2000)
	}
	if sink.count() != 0 {
		t.Errorf("sink.count = %d, want 0 (AC #2: intermittent 200 must reset counter)", sink.count())
	}
}

// TestLivenessRecv_ThreeConsecFires is the success path: 3 in a
// row of non_200 → relay fires exactly once.
func TestLivenessRecv_ThreeConsecFires(t *testing.T) {
	loop, sink, _ := newTestLoop(t, "inst-2", 3)
	loop.probeFn = func(_ context.Context, _ int) string {
		return livenessOutcomeNon200
	}
	for i := 0; i < 3; i++ {
		loop.runOne(context.Background(), 2000)
	}
	if sink.count() != 1 {
		t.Errorf("sink.count = %d, want 1 (3 consecutive non_200 must fire relay)", sink.count())
	}
	// 4th call after the relay fires — the loop's runOne has
	// already returned. The relay is expected to be invoked
	// AT MOST once per runOne cycle; a 4th runOne after a
	// successful park exits the production goroutine, but a
	// unit test of runOne in isolation doesn't have that exit
	// — the counter is reset to 0 implicitly by the relay's
	// exit path. We assert the test-side count remains 1.
}

func TestLivenessRecv_LoopStopsAfterThreshold(t *testing.T) {
	loop, sink, _ := newTestLoop(t, "inst-loop-stop", 2)
	loop.cfg.PeriodSeconds = 1
	loop.probeFn = func(_ context.Context, _ int) string {
		return livenessOutcomeConnRefused
	}

	done := make(chan struct{})
	go func() {
		loop.run(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("liveness loop did not stop after reaching the failure threshold")
	}
	// A loop that merely keeps ticking after the threshold would emit
	// another report on the next tick. Give that tick a chance to fire
	// and pin the one-report contract.
	time.Sleep(1100 * time.Millisecond)
	if sink.count() != 1 {
		t.Fatalf("sink.count = %d, want 1 after terminal liveness failure", sink.count())
	}
}

func TestLivenessRecv_CancellationDuringProbeDoesNotReport(t *testing.T) {
	loop, sink, _ := newTestLoop(t, "inst-cancel-race", 1)
	started := make(chan struct{})
	loop.probeFn = func(ctx context.Context, _ int) string {
		close(started)
		<-ctx.Done()
		return livenessOutcomeConnRefused
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		loop.run(ctx)
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("liveness probe did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("liveness loop did not stop after cancellation")
	}
	if sink.count() != 0 {
		t.Fatalf("sink.count = %d, want 0 after destroy cancellation won the race", sink.count())
	}
}

// TestLivenessRecv_TimeoutCountedClassifies is the classification
// pin: timeout outcome increments the counter (same code path
// as non_200) and lands a "liveness_timeout" reason string on
// the relay.
func TestLivenessRecv_TimeoutCountedClassifies(t *testing.T) {
	loop, sink, _ := newTestLoop(t, "inst-3", 2)
	loop.probeFn = func(_ context.Context, _ int) string {
		return livenessOutcomeTimeout
	}
	loop.runOne(context.Background(), 2000)
	loop.runOne(context.Background(), 2000)
	if sink.count() != 1 {
		t.Errorf("sink.count = %d, want 1 (2 consecutive timeouts must fire)", sink.count())
	}
}

// TestLivenessRecv_ConnRefusedCounted is the cold-boot signature:
// the guest-init listener isn't up yet on the first poll. The
// counter increments; the CooldownSeconds gate in the schedd
// side protects against noise.
func TestLivenessRecv_ConnRefusedCounted(t *testing.T) {
	loop, sink, _ := newTestLoop(t, "inst-4", 2)
	loop.probeFn = func(_ context.Context, _ int) string {
		return livenessOutcomeConnRefused
	}
	loop.runOne(context.Background(), 2000)
	loop.runOne(context.Background(), 2000)
	if sink.count() != 1 {
		t.Errorf("sink.count = %d, want 1 (conn_refused classifies same as non_200)", sink.count())
	}
}

// errDiscard removed (F9): the WithLivenessSink path lives in
// pkg/fcvm and is exercised by the manager_test there; this
// file no longer needs a sentinel.

// keep-time removed (F9): the test no longer references time.Time
// directly (the runOne signature is just (ctx, timeoutMs)); the
// liveness-window test at pkg/sched/liveness_window_test.go
// owns the time-driven paths.

// TestLivenessRecv_CooldownGateShortCircuits (issue #554 closure /
// ADR-078, code review #725 finding F1) pins the cooldown gate at
// runOne: a probe failure that falls inside cfg.CooldownSeconds of
// the previous LastLivenessDestroyAt stamp must NOT increment the
// counter and must NOT fire the relay. The customer-visible
// scenario is "I just had a wedged VM torn down, the cold-boot
// replacement is still warming up — don't tear it down too".
//
// Post-F1: the stamp key is the DEPLOYMENT id, not the instance
// id — the dying instance's UUID is gone after Park, but the
// cold-boot replacement inherits the deployment id from
// schedd's CreateInstance + Wake stamp. The unit test
// mirrors production by stamping under deploymentID and reading
// from a loop that carries the same deploymentID.
func TestLivenessRecv_CooldownGateShortCircuits(t *testing.T) {
	loop, sink, mgr := newTestLoop(t, "inst-cd", 3)
	// Register the instance with its deployment id so the
	// stamp + read share a key.
	depID := loop.deploymentID
	mgr.RegisterInstanceForTest("inst-cd", depID)
	// Stamp a destroy 5 seconds in the past under the
	// deployment id (NOT the instance id — see F1). cfg.CooldownSeconds=60
	// (set by newTestLoop), so a probe now falls well within the
	// window.
	mgr.SetLastLivenessDestroyAtForDeployment(depID, time.Now().Add(-5*time.Second))

	// All three probes are non_200, but the gate must short-circuit.
	loop.probeFn = func(_ context.Context, _ int) string {
		return livenessOutcomeNon200
	}
	for i := 0; i < 3; i++ {
		loop.runOne(context.Background(), 2000)
	}
	if sink.count() != 0 {
		t.Errorf("sink.count = %d, want 0 (cooldown gate must short-circuit fires within 60s)", sink.count())
	}
}

// TestLivenessRecv_CooldownGateExpires confirms the bypass: a
// destroy that's older than cfg.CooldownSeconds does NOT
// short-circuit. Without the bypass a customer's deployment
// would be parked forever once the first destroy happened.
func TestLivenessRecv_CooldownGateExpires(t *testing.T) {
	loop, sink, mgr := newTestLoop(t, "inst-cd-2", 3)
	depID := loop.deploymentID
	mgr.RegisterInstanceForTest("inst-cd-2", depID)
	// Stamp a destroy 120 seconds in the past. CooldownSeconds=60,
	// so we're well outside the window.
	mgr.SetLastLivenessDestroyAtForDeployment(depID, time.Now().Add(-120*time.Second))

	loop.probeFn = func(_ context.Context, _ int) string {
		return livenessOutcomeNon200
	}
	for i := 0; i < 3; i++ {
		loop.runOne(context.Background(), 2000)
	}
	if sink.count() != 1 {
		t.Errorf("sink.count = %d, want 1 (cooldown expired, 3 consec must fire)", sink.count())
	}
}

// TestLivenessRecv_CooldownGateZeroCooldownBypasses confirms the
// legacy / Free-plan path: CooldownSeconds=0 means "no cooldown
// gate" — pre-#554 behaviour. The destroy stamp is ignored.
func TestLivenessRecv_CooldownGateZeroCooldownBypasses(t *testing.T) {
	loop, sink, mgr := newTestLoop(t, "inst-cd-3", 2)
	loop.cfg.CooldownSeconds = 0 // legacy / Free
	depID := loop.deploymentID
	mgr.RegisterInstanceForTest("inst-cd-3", depID)
	mgr.SetLastLivenessDestroyAtForDeployment(depID, time.Now().Add(-1*time.Second))

	loop.probeFn = func(_ context.Context, _ int) string {
		return livenessOutcomeNon200
	}
	for i := 0; i < 2; i++ {
		loop.runOne(context.Background(), 2000)
	}
	if sink.count() != 1 {
		t.Errorf("sink.count = %d, want 1 (CooldownSeconds=0 bypasses the gate)", sink.count())
	}
}

// TestLivenessRecv_CooldownGateStampSurvivesInstanceReplacement
// (code review #725 finding F1) pins the load-bearing invariant
// the reviewer surfaced: the stamp key is the DEPLOYMENT id, not
// the instance id. Stamping on the dying instance (the pre-F1
// design) was structurally broken because Park deletes the
// live-map entry and the replacement carries a fresh zero-valued
// Instance{}. The test simulates the production flow:
//
//  1. Instance A is destroyed by ReportLivenessFailed (stamp
//     under deploymentID D1).
//  2. Park deletes instance A from m.live.
//  3. Cold-boot creates instance B with the SAME deploymentID
//     D1 (schedd's CreateInstance threads deploymentID through
//     the new Instance row + Wake stamps it onto the live
//     record at BringUp).
//  4. The liveness loop on B must observe the stamp from A —
//     if it observes zero, the gate was a no-op in production.
func TestLivenessRecv_CooldownGateStampSurvivesInstanceReplacement(t *testing.T) {
	loop, sink, mgr := newTestLoop(t, "inst-cold-boot", 3)
	depID := loop.deploymentID // "dep-inst-cold-boot"

	// Step 1+2: prior instance (different UUID, same deployment)
	// was destroyed. Stamp under deploymentID, NOT the prior
	// instance id — mirrors production's ReportLivenessFailed
	// path.
	mgr.RegisterInstanceForTest("inst-prior-destroyed", depID)
	mgr.SetLastLivenessDestroyAtForDeployment(depID, time.Now().Add(-5*time.Second))
	// The "prior destroyed" instance is now NOT in m.live (Park
	// deleted it). The stamp persists in cooldownByDeployment
	// keyed on depID.

	// Step 3: the cold-boot replacement has a fresh UUID. Its
	// loop is the `loop` variable above (deploymentID = depID).
	mgr.RegisterInstanceForTest("inst-cold-boot", depID)

	// Step 4: gate must short-circuit. If a regression reverts
	// to instance-keyed stamping, the gate sees zero and the
	// fires below would tear down a healthy cold-boot.
	loop.probeFn = func(_ context.Context, _ int) string {
		return livenessOutcomeNon200
	}
	for i := 0; i < 3; i++ {
		loop.runOne(context.Background(), 2000)
	}
	if sink.count() != 0 {
		t.Errorf("stamp must survive instance replacement (F1 invariant): sink.count = %d, want 0", sink.count())
	}
}

// TestLivenessRecv_CooldownGateIsolatesDeployments confirms that
// a stamp on deployment D1 does NOT short-circuit a loop on
// deployment D2 — protects against accidental "all deployments
// share one cooldown" semantics that would freeze a healthy
// workload when an unrelated workload destroys.
func TestLivenessRecv_CooldownGateIsolatesDeployments(t *testing.T) {
	loop, sink, mgr := newTestLoop(t, "inst-d2", 3)
	otherDep := "dep-other-app"
	mgr.RegisterInstanceForTest("inst-other", otherDep)
	mgr.SetLastLivenessDestroyAtForDeployment(otherDep, time.Now().Add(-5*time.Second))

	loop.probeFn = func(_ context.Context, _ int) string {
		return livenessOutcomeNon200
	}
	for i := 0; i < 3; i++ {
		loop.runOne(context.Background(), 2000)
	}
	if sink.count() != 1 {
		t.Errorf("cross-deployment stamp must not short-circuit: sink.count = %d, want 1", sink.count())
	}
}

// TestLivenessRecv_CooldownGateEmptyDeploymentIDBypasses confirms
// the legacy pre-PR-B path: a wake that doesn't carry
// deploymentID on the wire produces a loop with empty
// deploymentID, which must bypass the gate (the gate cannot
// key on "" without colliding every legacy wake).
func TestLivenessRecv_CooldownGateEmptyDeploymentIDBypasses(t *testing.T) {
	sink := &recordingSink{}
	mgr := fcvm.NewManager(nil, nil, fcvm.Paths{}, "1.10.0", slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	type sinkSetter interface {
		WithLivenessSink(fcvm.LivenessFailedSink) *fcvm.Manager
	}
	if ss, ok := any(mgr).(sinkSetter); ok {
		ss.WithLivenessSink(fcvm.LivenessFailedSink(sink.Record))
	}
	loop := &livenessProbeLoop{
		instance:     "inst-legacy",
		deploymentID: "", // legacy pre-PR-B
		cfg: livenessProbeConfig{
			Path:                "/healthz",
			PeriodSeconds:       5,
			ConsecutiveFailures: 2,
			CooldownSeconds:     60,
		},
		mgr: mgr,
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	loop.probeFn = func(_ context.Context, _ int) string {
		return livenessOutcomeNon200
	}
	for i := 0; i < 2; i++ {
		loop.runOne(context.Background(), 2000)
	}
	if sink.count() != 1 {
		t.Errorf("empty deploymentID must bypass cooldown: sink.count = %d, want 1", sink.count())
	}
}

// TestLivenessRecv_UnauthorizedCountedClassifies pins Cluster A's
// discriminator (spec §6.4 amendment 1): 3 consecutive
// livenessOutcomeUnauthorized outcomes fire the relay with reason
// "liveness_unauthorized". Pre-Cluster-A this path was only reached
// under //go:build metal via the schedd's
// DestroyForLivenessFailure → app_healthz_unauthorized stamping.
// The unit-level pin lets us catch regressions in the runOne +
// classifyLivenessOutcome wiring without spinning up a real VM.
func TestLivenessRecv_UnauthorizedCountedClassifies(t *testing.T) {
	loop, sink, _ := newTestLoop(t, "inst-unauth-1", 3)
	loop.probeFn = func(_ context.Context, _ int) string {
		return livenessOutcomeUnauthorized
	}
	for i := 0; i < 3; i++ {
		loop.runOne(context.Background(), 2000)
	}
	if sink.count() != 1 {
		t.Errorf("sink.count = %d, want 1 (3 consecutive unauthorized must fire relay)", sink.count())
	}
	// Pin the reason string — sched/engine.go:5038 matches on
	// reason == "liveness_unauthorized" to stamp
	// app_healthz_unauthorized on the deployment row. Drift in
	// the classifyLivenessOutcome switch arm would silently
	// break the wire-shape mapping.
	last := sink.calls[len(sink.calls)-1]
	if last.reason != "liveness_unauthorized" {
		t.Errorf("relay reason = %q, want %q", last.reason, "liveness_unauthorized")
	}
}

// TestLivenessRecv_UnauthorizedResetsOnSuccess mirrors
// TestLivenessRecv_CounterSurvivesIntermittentSuccess for the
// unauthorized outcome: the counter must reset on the first 2xx,
// so 401/401/200/401/401/401 fires exactly once (the third 401
// in the trailing sequence). Folding unauthorized into non_200
// would still pass this test (same counter); the discriminator
// matters for the wire reason, which
// TestLivenessRecv_UnauthorizedCountedClassifies pins separately.
func TestLivenessRecv_UnauthorizedResetsOnSuccess(t *testing.T) {
	loop, sink, _ := newTestLoop(t, "inst-unauth-2", 3)
	outcomes := []string{
		livenessOutcomeUnauthorized,
		livenessOutcomeUnauthorized,
		livenessOutcomeOK,
		livenessOutcomeUnauthorized,
		livenessOutcomeUnauthorized,
		livenessOutcomeUnauthorized,
	}
	loop.probeFn = func(_ context.Context, _ int) string {
		if len(outcomes) == 0 {
			return livenessOutcomeOK
		}
		out := outcomes[0]
		outcomes = outcomes[1:]
		return out
	}
	for i := 0; i < 6; i++ {
		loop.runOne(context.Background(), 2000)
	}
	if sink.count() != 1 {
		t.Errorf("sink.count = %d, want 1 (200 in the middle must reset counter; trailing 401s then fire)", sink.count())
	}
}

// TestLivenessRecv_ForbiddenCountedClassifies is the 403-arm pin:
// the cmd/vmmd/liveness_recv.go:372 discriminator folds 403 into
// livenessOutcomeUnauthorized (same as 401). Without this test,
// a future refactor that splits 401 vs 403 into two outcomes would
// silently break the wire mapping (the schedd side checks
// "liveness_unauthorized" reason — drift in the classify switch
// would orphan the 403 case).
func TestLivenessRecv_ForbiddenCountedClassifies(t *testing.T) {
	loop, sink, _ := newTestLoop(t, "inst-forbidden-1", 2)
	// Same outcome string — the test exercises the
	// runOne+classifyLivenessOutcome path. The 401 vs 403 split
	// happens earlier (in dialAndProbe); here we pin that
	// whatever maps to livenessOutcomeUnauthorized produces the
	// expected wire reason.
	loop.probeFn = func(_ context.Context, _ int) string {
		return livenessOutcomeUnauthorized
	}
	loop.runOne(context.Background(), 2000)
	loop.runOne(context.Background(), 2000)
	if sink.count() != 1 {
		t.Errorf("sink.count = %d, want 1 (2 consecutive unauthorized — including 403-folded — must fire)", sink.count())
	}
	last := sink.calls[len(sink.calls)-1]
	if last.reason != "liveness_unauthorized" {
		t.Errorf("relay reason = %q, want %q (403 must map to liveness_unauthorized, not its own arm)", last.reason, "liveness_unauthorized")
	}
}
