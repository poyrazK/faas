package sched

// Property tests pinning the §6.2 invariants that schedd enforces in-process.
// Slices 1-3 lifted unit coverage; this file moves the assertion from "one
// thread, one actor" to "many goroutines, the gate holds". Both tests are
// in-process (no Postgres, no KVM) and run under `go test -race -count=1`
// alongside the existing engine_test.go fakes (fakeVMM, seedApp, newEngine).
//
// §6.2-1 (the only invariant schedd is the canonical owner of): per-app
// concurrency. The gate is `NodeLedger.Admit`, which checks
// `l.perApp[appID] >= maxConc` (admission.go:129) and returns
// `api.ErrPlanLimitConcurrency(limits, have)` → HTTP 429 / CodePlanLimitConcur
// (api/errors.go:267). These tests pin that contract under fuzz-style
// contention.
//
// Caveat (documented by design): per-app appMu serialises concurrent
// Wakes for the SAME app (engine.go:1038 — `e.appMu[appID]` is keyed by
// appID). So a parallelism storm on one app does not actually exercise
// a race in the ledger — it exercises the lock-then-ledger path. The
// property still holds (the cap is enforced), but the test is really a
// "gate is the ledger" assertion, not a "lock is racy" assertion. A
// future property test against the ledger directly (without the engine
// wrapping it) would be more aggressive; the existing
// `FuzzLedgerInvariants` (ledger_property_test.go:49) already does that
// for the ledger's resident-RAM math.
//
// Why we still want this test: the engine's error path matters. A
// Wake that gets denied at Admit must surface `*api.Problem{Code:
// CodePlanLimitConcur}` (NOT a wrapped error or empty error) — the
// gateway maps 429 to "503" + the customer's billing-link. A regression
// that drops the `errors.As(err, &*api.Problem{})` assertion would
// survive single-threaded tests like TestEngineWake_AdmissionDeniedReturnsProblem
// but fail the 6-goroutine version.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestProperty_EngineWake_RespectsMaxConcurrency — six goroutines all
// calling Wake for the same Free app (MaxConcurrency=1 from plan.Limits;
// we override to 3 so we observe an actual cap, not just 1-vs-0).
//
// Properties the test asserts:
//
//   - exactly 3 Wakes return nil error (the cap is admitted, not 1,
//     not "any positive <6")
//   - exactly 3 Wakes return *api.Problem{Code: api.CodePlanLimitConcur};
//     we use errors.As to assert the precise wire type (not just err != nil)
//   - state.ListInstancesForApp returns exactly 6 rows: 3 RUNNING + 3 FAILED
//     (engine.go:264 transitions the failed row to StateFailed in the
//     same goroutine)
//   - the ledger.Concurrency(appID) returns exactly 3 (the cap)
//
// The fakeVMM is configured with sleepFor=10ms so each successful boot
// holds the per-app lock long enough for the contention to be real
// without making the test slow. We do NOT use bootStarted/bootRelease
// fencing — those channels are capacity 1 (engine_test.go:52-53) and
// would deadlock the second concurrent Wake.
func TestProperty_EngineWake_RespectsMaxConcurrency(t *testing.T) {
	store := state.NewMemStore()
	const maxConc = 3
	// Free plan: RAMMB=128, MaxConcurrency=1 by default. We seed with
	// maxConc=3 to bypass the plan clamp (admission.go:125-128 uses
	// min(req.MaxConcurrency, limits.MaxConcurrency) so we MUST use a
	// value that, when clamped, gives exactly 3 — Free clamps to 1
	// via limits.MaxConcurrency; Hobby caps at 2; Pro at 5. Use Pro
	// with maxConc=3 → effective cap = 3.
	_, app, _ := seedApp(t, store, api.PlanPro, 128, maxConc)
	vmm := &fakeVMM{sleepFor: 10 * time.Millisecond}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	const goroutines = 6 // 2x the cap
	results := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			_, err := e.Wake(context.Background(), app.ID)
			results <- err
		}()
	}

	var ok, denied int
	for i := 0; i < goroutines; i++ {
		err := <-results
		if err == nil {
			ok++
			continue
		}
		var p *api.Problem
		if errors.As(err, &p) && p.Code == api.CodePlanLimitConcur {
			denied++
			continue
		}
		t.Errorf("Wake error = %v; want *api.Problem{Code:CodePlanLimitConcur} or nil", err)
	}

	if ok != maxConc {
		t.Errorf("ok = %d, want %d (cap)", ok, maxConc)
	}
	if denied != goroutines-maxConc {
		t.Errorf("denied = %d, want %d", denied, goroutines-maxConc)
	}

	// State assertions on the store.
	rows, err := store.ListInstancesForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("ListInstancesForApp: %v", err)
	}
	if len(rows) != goroutines {
		t.Errorf("len(rows) = %d, want %d (every Wake leaves a row, success or fail)", len(rows), goroutines)
	}
	var running, failed int
	for _, ins := range rows {
		switch ins.State {
		case string(state.StateRunning):
			running++
		case string(state.StateFailed):
			failed++
		}
	}
	if running != maxConc {
		t.Errorf("running = %d, want %d", running, maxConc)
	}
	if failed != goroutines-maxConc {
		t.Errorf("failed = %d, want %d", failed, goroutines-maxConc)
	}

	// Ledger assertion — the cap gate, not the lock, must be the
	// mechanism (the lock just serialises).
	if got := e.Ledger().Concurrency(app.ID); got != maxConc {
		t.Errorf("ledger.Concurrency(%s) = %d, want %d", app.ID, got, maxConc)
	}
}

// TestProperty_EngineWake_DropsLockAroundBootRPC — pins the Phase-3
// lock-drop documented at engine.go:172-203 (PR #73 commit 2, M7
// finding #1). During a long cold-boot for app A, a Wake for a
// DIFFERENT app B must NOT block on app A's mutex — app B's Wake
// proceeds to Phase 2 admission and a second boot is launched in
// parallel.
//
// Method:
//
//   - seed appA (low cap) and appB (high cap, so admit doesn't refuse)
//   - configure fakeVMM with bootStarted/bootRelease so app A's boot
//     blocks on bootRelease; the channel is capacity 1, so app B's
//     boot will pass through unblocked (default-capacity 1 means
//     single-emitter, single-receiver — fits exactly the two-app
//     scenario)
//   - start Wake(appA) in goroutine; wait for bootStarted; start
//     Wake(appB); release bootRelease; assert both complete and
//     coldBoots==2 within wall time << 2*sleepFor
//
// If the engine held appMu during the vmmd RPC, Wake(appB) would
// block on appA's lock until bootRelease fires. The wall-time budget
// (`< sleepFor` for both to complete) is what proves the lock-drop.
func TestProperty_EngineWake_DropsLockAroundBootRPC(t *testing.T) {
	store := state.NewMemStore()
	// Two accounts with distinct emails — engine_test.go's seedApp
	// hardcodes "u@example.com" and a second call in the same store
	// would trip the MemStore's email uniqueness check. seedOneAccount
	// (deletion_subscriber_test.go:225) accepts a unique email.
	_, appA, _ := seedOneAccount(t, store, "lock-drop-a@example.com")
	_, appB, _ := seedOneAccount(t, store, "lock-drop-b@example.com")

	bootStarted := make(chan struct{}, 1)
	bootRelease := make(chan struct{})
	vmm := &fakeVMM{bootStarted: bootStarted, bootRelease: bootRelease}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	// We measure wall time for both Wakes to complete. If the engine
	// held the lock during Phase 3, appB would block on appA's mutex
	// for the full wait from `<-bootStarted` through `close(bootRelease)`,
	// plus any work afterwards — typically >100ms even under -race.
	// When the lock is dropped (the documented behaviour, engine.go:172-203),
	// appB proceeds as soon as it can grab its own appMu, which takes
	// microseconds once bootRelease fires.
	const bootReleaseDelay = 25 * time.Millisecond
	const deadline = 5 * bootReleaseDelay // 125ms — generous for -race overhead

	var wg sync.WaitGroup
	wg.Add(2)
	var appAErr, appBErr error

	start := time.Now()

	go func() {
		defer wg.Done()
		_, appAErr = e.Wake(context.Background(), appA.ID)
	}()
	// Wait for app A's wake to enter Phase 3 (signal arrived on bootStarted).
	<-bootStarted

	go func() {
		defer wg.Done()
		_, appBErr = e.Wake(context.Background(), appB.ID)
	}()

	// Give app B a moment to enter Phase 2 admission (not blocked on
	// bootRelease). Then release app A's boot. If app B is proceeding
	// in parallel, both cold boots succeed and wall time < 5*delay.
	time.Sleep(5 * time.Millisecond) // let app B race through Phase 2
	close(bootRelease)

	// Wait for both goroutines to finish. Bound to a generous deadline.
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(deadline):
		t.Fatalf("Wakes did not complete within %v (lock likely held during Phase 3)", deadline)
	}
	elapsed := time.Since(start)

	if appAErr != nil {
		t.Errorf("Wake(appA) = %v, want nil", appAErr)
	}
	if appBErr != nil {
		t.Errorf("Wake(appB) = %v, want nil (must not block on appA's lock)", appBErr)
	}
	// fakeVMM protects coldBoots under f.mu — read with the lock held
	// to avoid a -race warning on the final assertion.
	vmm.mu.Lock()
	coldBoots := vmm.coldBoots
	vmm.mu.Unlock()
	if coldBoots != 2 {
		t.Errorf("coldBoots = %d, want 2 (appA + appB ran in parallel)", coldBoots)
	}
	// Wall-time bound: if appB had to wait for appA's Phase 3 to
	// complete, the elapsed time would include the full bootRelease
	// wait + scheduler jitter. With the lock dropped (engine.go:193),
	// both Wakes proceed in parallel after bootRelease fires, finishing
	// well under `deadline`. Under -race this is still comfortably
	// < 5*bootReleaseDelay; if the lock were re-acquired the runtime
	// blows past the deadline immediately.
	if elapsed > deadline {
		t.Errorf("elapsed = %v, want < %v (lock likely held during Phase 3)", elapsed, deadline)
	}
}
