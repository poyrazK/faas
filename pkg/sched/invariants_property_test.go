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

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
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
			_, err := e.Wake(context.Background(), app.ID, "")
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

	// State assertions on the store. PR-C (issue #462): the
	// wake-gate admitGate short-circuits BEFORE CreateInstance,
	// so denied wakes leave no instances-table footprint. The
	// invariant §6.2-1 still holds (≤ maxConc in {WAKING,
	// COLD_BOOTING, RUNNING}); the row count is now exactly
	// maxConc, not goroutines. The legacy "every Wake leaves a
	// row, success or fail" pattern from the pre-PR-C
	// ledger-rejection path no longer applies — gate-denied
	// wakes never reach the ledger or the instances table.
	rows, err := store.ListInstancesForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("ListInstancesForApp: %v", err)
	}
	if len(rows) != maxConc {
		t.Errorf("len(rows) = %d, want %d (admitted; gate-denied wakes leave no row)", len(rows), maxConc)
	}
	var running int
	for _, ins := range rows {
		if ins.State == string(state.StateRunning) {
			running++
		}
	}
	if running != maxConc {
		t.Errorf("running = %d, want %d", running, maxConc)
	}

	// Ledger assertion — the cap gate, not the lock, must be the
	// mechanism (the lock just serialises).
	if got := e.Ledger().Concurrency(app.ID); got != maxConc {
		t.Errorf("ledger.Concurrency(%s) = %d, want %d", app.ID, got, maxConc)
	}
}

// TestProperty_EngineAdmitInstance_RespectsMaxConcurrency (issue #168).
// AdmitInstance is the schedule scale-out primitive: it bypasses Wake's
// Phase-1 fast-path so each call either admits a new instance or
// returns WakeResult{AtCapacity: true} when the app is already at
// effective max_concurrency. The gateway squeezes this until it
// reaches the cap, then treats the no-op as benign when it already
// has ≥1 cached target.
//
// Properties the test asserts (parallel to the Wake variant):
//
//   - exactly maxConc AdmitInstance calls return WakeResult with
//     AtCapacity=false and a non-empty InstanceID (the cap is
//     admitted, not "any positive <6")
//   - the remaining goroutines-maxConc calls return WakeResult with
//     AtCapacity=true (the typed "no more slots" result);
//     errors.As(err, &*api.Problem{}) must be false — these are
//     not errors, the ledger refusal is lifted into a typed result
//   - state.ListInstancesForApp returns exactly maxConc rows, all
//     RUNNING (no FAILED rows — the typed capacity branch deletes
//     the unattached row, it never writes FAILED)
//   - the ledger.Concurrency(appID) returns exactly maxConc
//
// fakeVMM.sleepFor=10ms holds the per-app lock long enough that the
// contention window is real (same as the Wake variant).
func TestProperty_EngineAdmitInstance_RespectsMaxConcurrency(t *testing.T) {
	store := state.NewMemStore()
	const maxConc = 3
	_, app, _ := seedApp(t, store, api.PlanPro, 128, maxConc)
	vmm := &fakeVMM{sleepFor: 10 * time.Millisecond}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	const goroutines = 6
	results := make(chan struct {
		res WakeResult
		err error
	}, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			res, err := e.AdmitInstance(context.Background(), app.ID, "")
			results <- struct {
				res WakeResult
				err error
			}{res, err}
		}()
	}

	var admitted, atCap int
	seenInstances := make(map[string]bool)
	for i := 0; i < goroutines; i++ {
		r := <-results
		if r.err != nil {
			t.Errorf("AdmitInstance error = %v; want nil (typed capacity is NOT an error)", r.err)
			continue
		}
		if r.res.AtCapacity {
			atCap++
			continue
		}
		if r.res.InstanceID == "" {
			t.Errorf("admitted result with empty InstanceID")
			continue
		}
		if seenInstances[r.res.InstanceID] {
			t.Errorf("duplicate instance id across admits: %q", r.res.InstanceID)
		}
		seenInstances[r.res.InstanceID] = true
		admitted++
	}

	if admitted != maxConc {
		t.Errorf("admitted = %d, want %d (cap)", admitted, maxConc)
	}
	if atCap != goroutines-maxConc {
		t.Errorf("atCap = %d, want %d", atCap, goroutines-maxConc)
	}

	rows, err := store.ListInstancesForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("ListInstancesForApp: %v", err)
	}
	if len(rows) != maxConc {
		t.Errorf("len(rows) = %d, want %d (typed capacity deletes unattached rows)", len(rows), maxConc)
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
	if failed != 0 {
		t.Errorf("failed = %d, want 0 (typed capacity must not write FAILED rows)", failed)
	}

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
		_, appAErr = e.Wake(context.Background(), appA.ID, "")
	}()
	// Wait for app A's wake to enter Phase 3 (signal arrived on bootStarted).
	<-bootStarted

	go func() {
		defer wg.Done()
		_, appBErr = e.Wake(context.Background(), appB.ID, "")
	}()

	// Release app A's boot WITHOUT a fixed sleep on app B — close(bootRelease)
	// releases both boots simultaneously (the channel is unbuffered, so both
	// receivers fire at the same instant). A CI-loaded host where app B
	// hasn't reached its bootRPC yet by the 5ms mark would flap the test;
	// simultaneous release makes the assertion independent of scheduling.
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

// TestProperty_EngineWake_RespectsCooldown (PR-C, issue #462) —
// pins the wake-gate cooldown enforcement under contention. The
// plan calls for "6 goroutines wake the same app in quick
// succession; first inserts, remaining 5 hit cooldown_held" but
// that pattern races on the per-app appMu and the post-insert
// stamp ordering. The deterministic shape pre-stamps the app's
// LastScaleOutAt to 1s ago + primes the ledger to Concurrency=1
// so EVERY goroutine sees the cooldown state; this asserts the
// load-bearing Concurrency > 0 discriminator. PR-D will add the
// CodeWaitForWarm constant; for now we assert on the metric
// counter increment + the absence of any successful wake.
//
// Properties the test asserts:
//
//   - all goroutines return *api.Problem (none succeed) — the
//     wake-gate short-circuits BEFORE the ledger check and the
//     instances INSERT, so a cooldown_held wake leaves no row.
//   - state.ListInstancesForApp returns 0 rows (gate-denied
//     wakes never reach the instances table).
//   - the wake-gate outcome metric
//     schedd_scale_up_decisions_total{outcome=cooldown_held} is
//     incremented once per cooldown_held decision. The exact
//     value is goroutine-count (no race: admitGate is the only
//     writer and the per-app appMu serialises the calls).
//   - the ledger.Concurrency(appID) stays at 1 (no admit
//     happened, no increment).
//
// Direction this test pins: a request-driven wake that races
// with a freshly-stamped scale-out MUST NOT admit. The
// Concurrency > 0 discriminator is the gate — a cold-start
// wake (Concurrency == 0) bypasses cooldown even when the
// stamp is fresh (TestAdmitGate_Outcomes/cold_start_bypass_cooldown
// covers that branch).
func TestProperty_EngineWake_RespectsCooldown(t *testing.T) {
	store := state.NewMemStore()
	const maxConc = 5
	_, app, _ := seedApp(t, store, api.PlanPro, 128, maxConc)
	// Set the customer-facing ScalingPolicy (ScaleOutCooldownS=60)
	// and stamp LastScaleOutAt = 1s ago so the consult fires.
	_, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{
		ScalingPolicy:    &state.ScalingPolicy{ScaleOutCooldownS: 60},
		SetScalingPolicy: true,
	})
	if err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	stamp := time.Now().Add(-1 * time.Second)
	if err := store.StampAppScaleOut(context.Background(), app.ID); err != nil {
		t.Fatalf("StampAppScaleOut: %v", err)
	}
	// Re-stamp the deterministic time. The production path uses
	// time.Now(); the helper is a whitebox seam for the test.
	store.SetLastScaleOutAt(app.ID, stamp)
	// Prime the ledger to Concurrency=1 so the Concurrency > 0
	// discriminator in admitGate fires.
	vmm := &fakeVMM{}
	ops := wire.NewOpsMetrics("schedd")
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
	if err := e.Ledger().Admit(Request{Instance: uuid.NewString(), AppID: app.ID, RAMMB: 128, Plan: api.PlanPro}); err != nil {
		t.Fatalf("prime ledger: %v", err)
	}

	const goroutines = 6
	results := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			_, err := e.Wake(context.Background(), app.ID, "")
			results <- err
		}()
	}
	denied := 0
	for i := 0; i < goroutines; i++ {
		err := <-results
		if err == nil {
			t.Errorf("Wake error = nil; want *api.Problem (cooldown_held)")
			continue
		}
		var p *api.Problem
		if errors.As(err, &p) && p.Code == api.CodeWaitForWarm {
			// PR-D (issue #462): the cooldown_held branch
			// surfaces as 503 + Retry-After
			// (CodeWaitForWarm). Pre-PR-D this was a
			// CodePlanLimitConcur 429; the wire shape split
			// is the v1 contract for the customer's
			// "rate-limit scale-outs" knob.
			if got := p.HasHeader("Retry-After"); len(got) != 1 {
				t.Errorf("Retry-After = %v, want [1 value]; cooldown remaining must always be a positive integer", got)
			}
			denied++
			continue
		}
		t.Errorf("Wake error = %v; want *api.Problem{Code:CodeWaitForWarm} (cooldown_held)", err)
	}
	if denied != goroutines {
		t.Errorf("denied = %d, want %d (every wake hits cooldown_held)", denied, goroutines)
	}

	// State assertions: gate-denied wakes leave no instances
	// footprint. This is the §6.2-1 invariant expressed under
	// the cooldown path: the cap is the upper bound on
	// (WAKING + COLD_BOOTING + RUNNING) and the cooldown_held
	// outcome never creates an instance to count.
	rows, err := store.ListInstancesForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("ListInstancesForApp: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("len(rows) = %d, want 0 (gate-denied wakes never INSERT)", len(rows))
	}
	// Ledger stays at 1 — no admit happened.
	if got := e.Ledger().Concurrency(app.ID); got != 1 {
		t.Errorf("ledger.Concurrency(%s) = %d, want 1 (no admit)", app.ID, got)
	}
	// Metric counter: one cooldown_held emission per goroutine.
	// admitGate runs under the per-app appMu, so the increments
	// are serialised; the total is exactly goroutines.
	if n := readScaleUp(t, ops, app.ID, "cooldown_held"); n != goroutines {
		t.Errorf("cooldown_held = %d, want %d", n, goroutines)
	}
}

// TestProperty_EngineWake_OverageCapReached (issue #561) — the
// spend-cap pause-workload branch under contention. Pins the
// contracts that the unit test only covers one-on-one:
//
//   - every goroutine returns *api.Problem{Code:
//     CodeAdmissionRefused} (the customer-facing 402 surface,
//     errors.go:CodeAdmissionRefused)
//   - the wake-gate short-circuits BEFORE the ledger or the
//     instances INSERT, so state.ListInstancesForApp returns 0
//     rows and the ledger.Concurrency stays at 0
//   - the per-(app,outcome) counter
//     schedd_scale_up_decisions_total{outcome=overage_cap_reached}
//     increments exactly once per goroutine (admitGate runs under
//     the per-app appMu, so the increments are serialised)
//   - the OverageChecker.RecordReached audit count is exactly
//     goroutines for the seeded account (the gate calls
//     RecordReached the same number of times it returns
//     wakeOverageCapReached)
//
// The OverageChecker is a stubChecker that returns OverageReached
// for the seeded account and OverageOK for any other account id.
// This lets a future test that seeds two accounts in one store
// exercise the per-account routing without colliding with the
// rest of the suite.
func TestProperty_EngineWake_OverageCapReached(t *testing.T) {
	store := state.NewMemStore()
	acct, app, _ := seedApp(t, store, api.PlanPro, 128, 5)
	ops := wire.NewOpsMetrics("schedd")

	// reachCounter is shared across goroutines via the mockChecker
	// mutex; reading it after Wait avoids a -race surface.
	stub := newMockChecker(func(_ context.Context, accountID string) (OverageStatus, int64, int64, error) {
		if accountID == acct.ID {
			return OverageReached, 5000, 4000, nil
		}
		return OverageOK, 0, 0, nil
	})
	// Resolve to *mockChecker so we can read the reached map.
	mc, ok := stub.(*mockChecker)
	if !ok {
		t.Fatalf("newMockChecker did not return *mockChecker; got %T", stub)
	}

	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").
		WithOpsMetrics(ops).
		WithOverageChecker(stub)

	const goroutines = 6
	results := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			_, err := e.Wake(context.Background(), app.ID, "")
			results <- err
		}()
	}

	denied := 0
	for i := 0; i < goroutines; i++ {
		err := <-results
		if err == nil {
			t.Errorf("Wake error = nil; want *api.Problem{Code:CodeAdmissionRefused}")
			continue
		}
		var p *api.Problem
		if errors.As(err, &p) && p.Code == api.CodeAdmissionRefused {
			denied++
			continue
		}
		t.Errorf("Wake error = %v; want *api.Problem{Code:CodeAdmissionRefused}", err)
	}
	if denied != goroutines {
		t.Errorf("denied = %d, want %d (every wake hits overage_cap_reached)", denied, goroutines)
	}

	// State assertions: gate-denied wakes leave no instances
	// footprint. Mirrors the invariants_property_test.go cooldown
	// pattern but the cap-hit branch is the new evidence site.
	rows, err := store.ListInstancesForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("ListInstancesForApp: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("len(rows) = %d, want 0 (overage-cap wakes never INSERT)", len(rows))
	}
	if got := e.Ledger().Concurrency(app.ID); got != 0 {
		t.Errorf("ledger.Concurrency(%s) = %d, want 0 (no admit)", app.ID, got)
	}
	// Metric counter: one overage_cap_reached emission per goroutine.
	// admitGate runs under the per-app appMu, so the increments
	// are serialised; the total is exactly goroutines.
	if n := readScaleUp(t, ops, app.ID, "overage_cap_reached"); n != goroutines {
		t.Errorf("overage_cap_reached = %d, want %d", n, goroutines)
	}
	// Audit emit counter: the gate calls RecordReached once per
	// refused wake. UTC-day dedupe is the production checker's
	// concern (overage_test.go); this stub counts every call so
	// the wire surface stays bounded to goroutines.
	mc.reachedMu.Lock()
	reached := mc.reached[acct.ID]
	mc.reachedMu.Unlock()
	if reached != goroutines {
		t.Errorf("RecordReached count = %d, want %d", reached, goroutines)
	}
}

// TestProperty_EnsureWake_BurstCoalescesToOneBoot (ADR-098 §6.2-1):
//
// A burst of N concurrent EnsureWake calls for one parked app must
// collapse into exactly ONE virtual boot (one CreateColdBoot on the
// fakeVMM). Pre-ADR-098 the per-app appMu serialised Wakes, so a
// burst of N produced N sequential Boots — each Wake raced the
// ledger and one became the winner, but N-1 still booted and
// tore themselves down in Phase 4. The single-flight coordinator
// collapses the burst before the leader's Wake call lands.
//
// Properties the test asserts:
//
//   - exactly one fakeVMM.CreateColdBoot fires (the coordinator's
//     leader is the only goroutine that runs Engine.Wake)
//   - all N goroutines see a non-nil CoordOutcome.Instance
//     (the leader's outcome is propagated to every follower)
//   - all N CoordOutcome.Instance values share the same InstanceID
//     (followers must inherit the leader's identity verbatim —
//     not a fresh mint per follower)
//   - the wake-coord entry is gone after the last follower releases
//     (no leaked entries; the Forget path is exercised on shutdown)
//
// fakeVMM.CreateColdBoot sleeps sleepFor=10ms so the leader is
// observable as a leader (followers queue behind, the leader's
// outcome is shared). The test holds N=8 — comfortably above the
// appMu-cascade floor of 1 and below the wake-coord cap (512).
func TestProperty_EnsureWake_BurstCoalescesToOneBoot(t *testing.T) {
	store := state.NewMemStore()
	const maxConc = 8
	_, app, _ := seedApp(t, store, api.PlanPro, 128, maxConc)

	vmm := &fakeVMM{sleepFor: 10 * time.Millisecond}
	notif := &fakeNotifier{}
	engine := newEngine(t, store, vmm, notif, "1.0")

	const goroutines = 8
	var (
		wg      sync.WaitGroup
		insMu   sync.Mutex
		gotInst []string
		errs    []error
	)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			out, err := engine.EnsureWake(context.Background(), app.ID)
			insMu.Lock()
			defer insMu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			if out.Instance == nil {
				errs = append(errs, errors.New("nil CoordOutcome.Instance"))
				return
			}
			gotInst = append(gotInst, out.Instance.InstanceID)
		}()
	}
	wg.Wait()

	if len(errs) != 0 {
		t.Fatalf("goroutine errors: %v", errs)
	}
	if len(gotInst) != goroutines {
		t.Fatalf("got %d InstanceIDs, want %d", len(gotInst), goroutines)
	}
	if bootCount := vmm.coldBoots; bootCount != 1 {
		t.Fatalf("CreateColdBoot fired %d times, want exactly 1 (single-flight)", bootCount)
	}
	// All followers must see the leader's instance identity.
	first := gotInst[0]
	for i, id := range gotInst {
		if id != first {
			t.Errorf("outcome[%d].InstanceID = %q, want %q (leader's)", i, id, first)
		}
	}
	// The wake-coord entry must be gone — no leaked map rows.
	if _, ok := engine.wakeCoord.inflight[app.ID]; ok {
		t.Errorf("wake-coord entry for %q still present after final release", app.ID)
	}
}
