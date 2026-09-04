// engine_pure_test.go — fill pkg/sched/engine.go coverage of the pure
// helpers that don't require a store or a ledger: bootTimeout,
// prefixesToCIDRStrings, budgetFor / budgetForWake (with override),
// planAllowsWarm, expectedStateForReason, terminalStateForReason,
// cooldownSRemaining.
//
// Whitebox `package sched`.

package sched

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// --- bootTimeout --------------------------------------------------

func TestBootTimeout_KnownStates(t *testing.T) {
	cases := map[state.State]time.Duration{
		state.StateWaking:       WakingTimeout,
		state.StateColdBooting:  ColdBootTimeout,
		state.StateSnapshotting: ColdBootTimeout, // default branch
	}
	for s, want := range cases {
		if got := bootTimeout(s); got != want {
			t.Errorf("bootTimeout(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestBootTimeout_UnknownDefaultsToColdBoot(t *testing.T) {
	for _, s := range []state.State{"", state.StateRunning, state.StateParked, "bogus"} {
		if got := bootTimeout(s); got != ColdBootTimeout {
			t.Errorf("bootTimeout(%q) = %v, want %v", s, got, ColdBootTimeout)
		}
	}
}

// --- prefixesToCIDRStrings ---------------------------------------

func TestPrefixesToCIDRStrings_Empty(t *testing.T) {
	if got := prefixesToCIDRStrings(nil); got != nil {
		t.Errorf("nil: %v, want nil", got)
	}
	if got := prefixesToCIDRStrings([]netip.Prefix{}); got != nil {
		t.Errorf("empty: %v, want nil", got)
	}
}

func TestPrefixesToCIDRStrings_V4(t *testing.T) {
	in := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("192.168.0.0/16"),
	}
	got := prefixesToCIDRStrings(in)
	if len(got) != 2 || got[0] != "10.0.0.0/8" || got[1] != "192.168.0.0/16" {
		t.Errorf("got %v", got)
	}
}

func TestPrefixesToCIDRStrings_V6(t *testing.T) {
	in := []netip.Prefix{netip.MustParsePrefix("::1/128")}
	got := prefixesToCIDRStrings(in)
	if len(got) != 1 || got[0] != "::1/128" {
		t.Errorf("got %v", got)
	}
}

// --- budgetFor / budgetForWake (override path) -------------------

func TestEngine_BudgetFor_NoOverride(t *testing.T) {
	e := &Engine{}
	if got := e.budgetFor(state.StateWaking); got != WakingTimeout {
		t.Errorf("WAKING: got %v, want %v", got, WakingTimeout)
	}
	if got := e.budgetFor(state.StateColdBooting); got != ColdBootTimeout {
		t.Errorf("COLD_BOOTING: got %v, want %v", got, ColdBootTimeout)
	}
}

func TestEngine_BudgetFor_WithOverride(t *testing.T) {
	e := &Engine{
		bootBudget: func(s state.State) time.Duration {
			return 123 * time.Millisecond
		},
	}
	if got := e.budgetFor(state.StateWaking); got != 123*time.Millisecond {
		t.Errorf("override: got %v", got)
	}
}

func TestEngine_BudgetForWake_NoOverrideWithSnap(t *testing.T) {
	// haveSnap=true → cold-boot budget regardless of initState.
	e := &Engine{}
	got := e.budgetForWake(bootInput{initState: state.StateWaking, haveSnap: true, snapKey: "snap-1"})
	if got != ColdBootTimeout {
		t.Errorf("with snap: got %v, want ColdBootTimeout", got)
	}
}

func TestEngine_BudgetForWake_NoOverrideNoSnap(t *testing.T) {
	// haveSnap=false → fall through to budgetFor(initState).
	e := &Engine{}
	got := e.budgetForWake(bootInput{initState: state.StateWaking})
	if got != WakingTimeout {
		t.Errorf("no snap WAKING: got %v, want WakingTimeout", got)
	}
}

func TestEngine_BudgetForWake_OverrideWins(t *testing.T) {
	// bootBudget is authoritative even with haveSnap set.
	e := &Engine{
		bootBudget: func(s state.State) time.Duration {
			return 50 * time.Millisecond
		},
	}
	got := e.budgetForWake(bootInput{initState: state.StateWaking, haveSnap: true, snapKey: "snap-1"})
	if got != 50*time.Millisecond {
		t.Errorf("override + snap: got %v", got)
	}
}

// --- planAllowsWarm ----------------------------------------------

func TestPlanAllowsWarm(t *testing.T) {
	cases := map[string]bool{
		string(api.PlanFree):  false,
		string(api.PlanHobby): false,
		string(api.PlanPro):   true,
		string(api.PlanScale): true,
		"unknown_plan":        false,
		"":                    false,
	}
	for plan, want := range cases {
		if got := planAllowsWarm(plan); got != want {
			t.Errorf("planAllowsWarm(%q) = %v, want %v", plan, got, want)
		}
	}
}

// --- expectedStateForReason / terminalStateForReason -------------

func TestExpectedStateForReason(t *testing.T) {
	cases := map[StuckReason]state.State{
		StuckWakingTimeout:   state.StateWaking,
		StuckColdBootTimeout: state.StateColdBooting,
		StuckSnapshotTimeout: state.StateSnapshotting,
		"unknown":            "",
	}
	for r, want := range cases {
		if got := expectedStateForReason(r); got != want {
			t.Errorf("expectedStateForReason(%q) = %q, want %q", r, got, want)
		}
	}
}

func TestTerminalStateForReason(t *testing.T) {
	cases := map[StuckReason]state.State{
		StuckWakingTimeout:   state.StateColdBooting,
		StuckColdBootTimeout: state.StateFailed,
		StuckSnapshotTimeout: state.StateStopped,
		"unknown":            "",
	}
	for r, want := range cases {
		if got := terminalStateForReason(r); got != want {
			t.Errorf("terminalStateForReason(%q) = %q, want %q", r, got, want)
		}
	}
}

// --- cooldownSRemaining ------------------------------------------

func TestCooldownSRemaining_NilStampReturnsOne(t *testing.T) {
	app := &state.App{}
	if got := cooldownSRemaining(app, time.Now()); got != 1 {
		t.Errorf("nil stamp: got %d, want 1", got)
	}
}

func TestCooldownSRemaining_NilPolicyReturnsOne(t *testing.T) {
	now := time.Now()
	stamp := now.Add(-10 * time.Second)
	app := &state.App{
		LastScaleOutAt: &stamp,
		// ScalingPolicy == nil
	}
	if got := cooldownSRemaining(app, now); got != 1 {
		t.Errorf("nil policy: got %d, want 1", got)
	}
}

func TestCooldownSRemaining_PolicyZeroCooldownReturnsOne(t *testing.T) {
	now := time.Now()
	stamp := now.Add(-10 * time.Second)
	app := &state.App{
		LastScaleOutAt: &stamp,
		ScalingPolicy:  &state.ScalingPolicy{ScaleOutCooldownS: 0},
	}
	if got := cooldownSRemaining(app, now); got != 1 {
		t.Errorf("zero cooldown: got %d, want 1", got)
	}
}

func TestCooldownSRemaining_FutureElapsedReturnsOne(t *testing.T) {
	now := time.Now()
	stamp := now.Add(-100 * time.Second) // already expired
	app := &state.App{
		LastScaleOutAt: &stamp,
		ScalingPolicy:  &state.ScalingPolicy{ScaleOutCooldownS: 60},
	}
	if got := cooldownSRemaining(app, now); got != 1 {
		t.Errorf("expired: got %d, want 1", got)
	}
}

func TestCooldownSRemaining_ActiveCooldownReturnsSeconds(t *testing.T) {
	now := time.Now()
	stamp := now.Add(-10 * time.Second)
	app := &state.App{
		LastScaleOutAt: &stamp,
		ScalingPolicy:  &state.ScalingPolicy{ScaleOutCooldownS: 60},
	}
	got := cooldownSRemaining(app, now)
	// 60 - 10 = 50s (with sub-second floor, must be >= 49).
	if got < 49 || got > 51 {
		t.Errorf("active: got %d, want ~50", got)
	}
}

func TestScaleOutBurstContinuationOnlyBypassesCooldown(t *testing.T) {
	stamp := time.Now()
	app := &state.App{
		ID:             "app-1",
		AccountID:      "acct-1",
		MaxConcurrency: 4,
		LastScaleOutAt: &stamp,
		ScalingPolicy:  &state.ScalingPolicy{ScaleOutCooldownS: 60},
	}
	e := &Engine{ledger: NewNodeLedger()}
	if err := e.ledger.Admit(Request{
		Instance:       "instance-1",
		AppID:          app.ID,
		Plan:           api.PlanPro,
		RAMMB:          128,
		VCPU:           1,
		MaxConcurrency: app.MaxConcurrency,
	}); err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
	limits := api.MustLimitsFor(api.PlanPro)
	if got, _, _, _, _ := e.admitGate(context.Background(), app, limits); got != wakeCooldownHeld {
		t.Fatalf("ordinary admission outcome = %v, want cooldown held", got)
	}
	if got, _, _, _, _ := e.admitGate(withScaleOutBurstContinuation(context.Background()), app, limits); got != wakeAdmit {
		t.Fatalf("burst continuation outcome = %v, want admit", got)
	}
}
