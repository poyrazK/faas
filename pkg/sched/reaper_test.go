package sched

import (
	"bytes"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestEffectiveIdleTimeout(t *testing.T) {
	tests := []struct {
		plan       api.Plan
		configured int
		want       int
	}{
		{api.PlanFree, 0, 30},    // default
		{api.PlanPro, 0, 300},    // default
		{api.PlanPro, 120, 120},  // in-bounds override
		{api.PlanPro, 5, 10},     // below floor → 10
		{api.PlanPro, 9999, 600}, // above ceiling (300×2) → 600
		{api.PlanFree, 100, 60},  // Free ceiling = 30×2
	}
	for _, tt := range tests {
		if got := EffectiveIdleTimeoutS(tt.plan, tt.configured); got != tt.want {
			t.Errorf("EffectiveIdleTimeoutS(%s, %d) = %d, want %d", tt.plan, tt.configured, got, tt.want)
		}
	}
}

func TestReapIdle(t *testing.T) {
	now := time.Now()
	instances := []InstanceInfo{
		// Pro default 300s; idle 400s → reap.
		{Instance: "idle", Plan: api.PlanPro, State: state.StateRunning, LastRequest: now.Add(-400 * time.Second)},
		// Pro; idle 100s → keep.
		{Instance: "busy", Plan: api.PlanPro, State: state.StateRunning, LastRequest: now.Add(-100 * time.Second)},
		// Idle but not running → not reapable.
		{Instance: "waking", Plan: api.PlanPro, State: state.StateWaking, LastRequest: now.Add(-999 * time.Second)},
		// Free 30s; idle 45s → reap.
		{Instance: "free-idle", Plan: api.PlanFree, State: state.StateRunning, LastRequest: now.Add(-45 * time.Second)},
	}
	got := ReapIdle(now, instances, nil, nil)
	if !equalSet(got, []string{"idle", "free-idle"}) {
		t.Errorf("ReapIdle = %v, want [idle free-idle]", got)
	}
}

func TestReapIdleUsesStartedAtWhenLastRequestIsMissing(t *testing.T) {
	now := time.Now()
	instances := []InstanceInfo{
		{
			Instance: "newly-started",
			Plan:     api.PlanPro,
			State:    state.StateRunning,
			Started:  now.Add(-5 * time.Second),
		},
		{
			Instance: "old-never-requested",
			Plan:     api.PlanPro,
			State:    state.StateRunning,
			Started:  now.Add(-time.Hour),
		},
		{
			Instance: "unknown-age",
			Plan:     api.PlanPro,
			State:    state.StateRunning,
		},
	}
	got := ReapIdle(now, instances, nil, nil)
	if !equalSet(got, []string{"old-never-requested"}) {
		t.Fatalf("ReapIdle = %v, want only old-never-requested", got)
	}
}

// TestReapIdleSkipsInstanceWithOpenConns pins spec §17 G7: an instance
// with OpenConns > 0 is considered active regardless of LastRequest
// staleness. Without this, a parked app mid-WebSocket would be reaped
// on the next idle tick (60 s on Hobby) and the connection would
// close. The five cases fence the behaviour on every side:
//
//   - open=0 + stale LastRequest → reaped (regression: old rule fires)
//   - open>0 + stale LastRequest → NOT reaped (the G7 fix)
//   - open>0 + recent LastRequest → not reaped (no double-counting)
//   - open>0 + zero LastRequest (never-seen) → not reaped
//   - open=0 + recent LastRequest → not reaped (control)
func TestReapIdleSkipsInstanceWithOpenConns(t *testing.T) {
	now := time.Now()
	instances := []InstanceInfo{
		// G7 fix.
		{Instance: "open-stale", Plan: api.PlanPro, State: state.StateRunning,
			LastRequest: now.Add(-time.Hour), OpenConns: 3},
		// No regression: still reaped when no flow + stale.
		{Instance: "idle", Plan: api.PlanPro, State: state.StateRunning,
			LastRequest: now.Add(-time.Hour)},
		// Active + open flows: not reaped.
		{Instance: "open-fresh", Plan: api.PlanPro, State: state.StateRunning,
			LastRequest: now.Add(-time.Second), OpenConns: 1},
		// Never-seen + open: not reaped (TCP active w/ no HTTP).
		{Instance: "open-zero-last", Plan: api.PlanHobby, State: state.StateRunning,
			OpenConns: 2},
		// Active + no flow: not reaped.
		{Instance: "fresh", Plan: api.PlanPro, State: state.StateRunning,
			LastRequest: now.Add(-time.Second)},
	}
	got := ReapIdle(now, instances, nil, nil)
	if !equalSet(got, []string{"idle"}) {
		t.Errorf("ReapIdle = %v, want [idle] only", got)
	}
}

// TestReapIdleSkipsInstanceWithTailCount pins issue #667 / ADR-078
// §"Reaper gate": an instance with active waitUntil tasks
// (TailCount > 0) is alive — the runner is in the tail-host drain
// phase, not idle — so ReapIdle must skip it the same way it skips
// OpenConns > 0. The wake is parked only when the runner drains
// (TailCount returns to 0) or the 5s snapshotAndPark watchdog
// fires. Mirrors TestReapIdleSkipsInstanceWithOpenConns.
func TestReapIdleSkipsInstanceWithTailCount(t *testing.T) {
	now := time.Now()
	instances := []InstanceInfo{
		// Issue #667 / ADR-078: stale AppID with active tail tasks
		// is NOT idle — the runner is draining.
		{Instance: "tail-stale", Plan: api.PlanPro, State: state.StateRunning,
			LastRequest: now.Add(-time.Hour), TailCount: 3},
		// No regression: still reaped when no tail + stale.
		{Instance: "idle", Plan: api.PlanPro, State: state.StateRunning,
			LastRequest: now.Add(-time.Hour)},
		// Active + tails: not reaped.
		{Instance: "tail-fresh", Plan: api.PlanPro, State: state.StateRunning,
			LastRequest: now.Add(-time.Second), TailCount: 1},
		// Tails but no LastRequest stamp: still not reaped (the
		// runner is alive regardless of HTTP activity).
		{Instance: "tail-zero-last", Plan: api.PlanHobby, State: state.StateRunning,
			TailCount: 2},
		// Active + no tail: not reaped.
		{Instance: "fresh", Plan: api.PlanPro, State: state.StateRunning,
			LastRequest: now.Add(-time.Second)},
	}
	got := ReapIdle(now, instances, nil, nil)
	if !equalSet(got, []string{"idle"}) {
		t.Errorf("ReapIdle = %v, want [idle] only", got)
	}
}

// TestReapAggressiveSkipsInstanceWithTailCount pins the same
// gate for the aggressive reaper (issue #667 / ADR-078). The
// aggressive reaper evicts instances under RAM pressure regardless
// of MinInstanceAge, but still honors the activity gates
// (WorkloadClass, OpenConns, TailCount). Without this gate, a
// Scale-app RPS burst could evict a wake whose runner is still
// draining waitUntil tasks — the customer loses the side effects
// of those tasks (email send, log flush, etc.) silently.
func TestReapAggressiveSkipsInstanceWithTailCount(t *testing.T) {
	now := time.Now()
	instances := []InstanceInfo{
		// Stale + has tails: must NOT enter the candidate set.
		{Instance: "tail-stale", AppID: "app1", Plan: api.PlanPro, State: state.StateRunning,
			LastRequest: now.Add(-time.Hour), TailCount: 3},
		// Fresh + has tails: must NOT enter the candidate set.
		{Instance: "tail-fresh", AppID: "app1", Plan: api.PlanPro, State: state.StateRunning,
			LastRequest: now.Add(-time.Second), TailCount: 1},
		// Stale, no tails: candidate.
		{Instance: "idle", AppID: "app1", Plan: api.PlanPro, State: state.StateRunning,
			LastRequest: now.Add(-time.Hour)},
		// Fresh, no tails: candidate.
		{Instance: "fresh", AppID: "app1", Plan: api.PlanPro, State: state.StateRunning,
			LastRequest: now.Add(-time.Second)},
	}
	// The aggressive reaper evicts the LRU end of the candidate set
	// up to running - (desired+1). With desired=0, limit=1, that
	// picks the top 3 of the candidates. The test asserts the
	// load-bearing property: tail-stale and tail-fresh are NEVER
	// in the result regardless of how many total slots the
	// aggressive reaper decides to fill. The bare presence of `idle`
	// in the result is incidental — what matters is the absence of
	// the two tail-bearing instances.
	const testAppID = "app1"
	desiredByApp := map[string]int{testAppID: 0}
	got := ReapAggressive(now, instances, desiredByApp, nil, nil)
	if containsString(got, "tail-stale") {
		t.Errorf("ReapAggressive picked tail-stale (TailCount=3, stale); the tail-count gate is not honored; got %v", got)
	}
	if containsString(got, "tail-fresh") {
		t.Errorf("ReapAggressive picked tail-fresh (TailCount=1, fresh); the tail-count gate is not honored; got %v", got)
	}
}

func TestSelectEvictionsBelowThresholdNoop(t *testing.T) {
	got := SelectEvictions(EvictionThresholdMB, time.Now(), []InstanceInfo{
		{Instance: "x", Plan: api.PlanPro, State: state.StateRunning},
	})
	if got != nil {
		t.Errorf("no eviction expected at/below threshold, got %v", got)
	}
}

func TestSelectEvictionsLRUAndScaleLast(t *testing.T) {
	now := time.Now()
	old := now.Add(-time.Hour)
	// Resident well over threshold; each Pro instance is 520 MB (512+8).
	instances := []InstanceInfo{
		{Instance: "scale-oldest", Plan: api.PlanScale, State: state.StateRunning, RAMMB: 1024, LastRequest: old.Add(-time.Hour), Started: old},
		{Instance: "pro-newest", Plan: api.PlanPro, State: state.StateRunning, RAMMB: 512, LastRequest: now.Add(-time.Minute), Started: old},
		{Instance: "pro-oldest", Plan: api.PlanPro, State: state.StateRunning, RAMMB: 512, LastRequest: old, Started: old},
	}
	// Just 1 MB over threshold → evict exactly one; must be the LRU non-Scale.
	got := SelectEvictions(EvictionThresholdMB+1, now, instances)
	if len(got) != 1 || got[0] != "pro-oldest" {
		t.Errorf("expected [pro-oldest] (LRU non-Scale), got %v", got)
	}
}

func TestSelectEvictionsProtectsYoungInstances(t *testing.T) {
	now := time.Now()
	instances := []InstanceInfo{
		// Over threshold but only a 5s-old instance exists → protected.
		{Instance: "fresh", Plan: api.PlanPro, State: state.StateRunning, RAMMB: 512, LastRequest: now.Add(-time.Hour), Started: now.Add(-5 * time.Second)},
	}
	got := SelectEvictions(EvictionThresholdMB+1000, now, instances)
	if len(got) != 0 {
		t.Errorf("instance younger than %s must not be evicted, got %v", MinInstanceAge, got)
	}
}

func TestSelectEvictionsSkipsServiceReplicas(t *testing.T) {
	now := time.Now()
	old := now.Add(-time.Hour)
	instances := []InstanceInfo{
		{Instance: "service-replica", AppID: "svc", Plan: api.PlanPro, State: state.StateRunning,
			Mode: string(state.InstanceModeService), RAMMB: 512, LastRequest: old, Started: old},
		{Instance: "request-instance", AppID: "fn", Plan: api.PlanPro, State: state.StateRunning,
			Mode: string(state.InstanceModeNormal), RAMMB: 512, LastRequest: old, Started: old},
	}

	got := SelectEvictions(EvictionThresholdMB+1, now, instances)
	if len(got) != 1 || got[0] != "request-instance" {
		t.Fatalf("service replica must be skipped while request instance remains evictable, got %v", got)
	}

	got = SelectEvictions(EvictionThresholdMB+1, now, instances[:1])
	if len(got) != 0 {
		t.Fatalf("service-only pressure must not park a replica, got %v", got)
	}
}

func TestSelectEvictionsEvictsEnough(t *testing.T) {
	now := time.Now()
	old := now.Add(-time.Hour)
	var instances []InstanceInfo
	for i := 0; i < 10; i++ {
		instances = append(instances, InstanceInfo{
			Instance: string(rune('a' + i)), Plan: api.PlanPro, State: state.StateRunning,
			RAMMB: 512, LastRequest: old.Add(time.Duration(i) * time.Minute), Started: old,
		})
	}
	// 2080 MB over threshold; each frees 520 MB → need 4 evictions.
	got := SelectEvictions(EvictionThresholdMB+2080, now, instances)
	if len(got) != 4 {
		t.Errorf("expected 4 evictions to clear 2080 MB at 520 MB each, got %d (%v)", len(got), got)
	}
}

func equalSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, s := range a {
		m[s]++
	}
	for _, s := range b {
		m[s]--
	}
	for _, v := range m {
		if v != 0 {
			return false
		}
	}
	return true
}

// containsString returns true if x is present in s. Used by the
// tail-count / OpenConns reaper-gate tests where the assertion is
// "the gated instance is NOT in the result" — we don't need an
// exact-result equality, just membership.
func containsString(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}

// TestReapIdleRespectsMinInstancesFloor pins ux_spec §6.5: when an
// app's MinInstances > 0, the reaper must keep at least that many
// RUNNING instances alive regardless of idle timeout. Direction:
// drop the FRESHEST candidates (not the most-idle ones) so the
// freshly-woken instance that just served a user stays resident.
//
// Layout: 4 stale Pro instances of the same app. Floor 0 → all 4
// reaped (matches TestReapIdle behaviour). Floor 2 → 2 reaped
// (the two oldest by LastRequest). Floor 4 → 0 reaped. Floor
// larger than running → 0 reaped (degenerate but bounded).
func TestReapIdleRespectsMinInstancesFloor(t *testing.T) {
	now := time.Now()
	mkApp := func(id string, lastSeen time.Duration) InstanceInfo {
		return InstanceInfo{
			Instance: id, AppID: "app1", Plan: api.PlanPro,
			State: state.StateRunning, LastRequest: now.Add(-lastSeen),
		}
	}
	instances := []InstanceInfo{
		mkApp("oldest", time.Hour), // most idle → reap first
		mkApp("older", 45*time.Minute),
		mkApp("newer", 30*time.Minute),
		mkApp("newest", 15*time.Minute), // freshest → reap last
	}
	for _, in := range instances {
		in.MinInstances = 0 // start with no floor
	}
	// floor 0 → reap all 4.
	got := ReapIdle(now, instances, nil, nil)
	if !equalSet(got, []string{"oldest", "older", "newer", "newest"}) {
		t.Fatalf("floor 0: got %v, want all 4", got)
	}
	// floor 2 → reap 2 oldest.
	for i := range instances {
		instances[i].MinInstances = 2
	}
	got = ReapIdle(now, instances, nil, nil)
	if !equalSet(got, []string{"oldest", "older"}) {
		t.Fatalf("floor 2: got %v, want [oldest older] (drop freshest)", got)
	}
	// floor 4 → reap 0.
	for i := range instances {
		instances[i].MinInstances = 4
	}
	got = ReapIdle(now, instances, nil, nil)
	if len(got) != 0 {
		t.Fatalf("floor 4 (== running): got %v, want empty", got)
	}
	// floor 99 (degenerate; per-row) → reap 0. allowed = running(4) - 99 < 0.
	for i := range instances {
		instances[i].MinInstances = 99
	}
	got = ReapIdle(now, instances, nil, nil)
	if len(got) != 0 {
		t.Fatalf("floor 99 (>running): got %v, want empty (allowed clamps to 0)", got)
	}
}

// TestReapIdleFloorDoesNotCrossApps locks in that the floor is
// per-app: app1's floor 2 must not reduce app2's park count and
// vice versa. Two stale instances of app1, three of app2 — both
// app1 and app2 have floor 1 → app1 reaps 1 (keeps 1), app2
// reaps 2 (keeps 1).
func TestReapIdleFloorDoesNotCrossApps(t *testing.T) {
	now := time.Now()
	mkApp := func(app, id string, lastSeen time.Duration) InstanceInfo {
		return InstanceInfo{
			Instance: id, AppID: app, Plan: api.PlanPro,
			State: state.StateRunning, LastRequest: now.Add(-lastSeen),
		}
	}
	instances := []InstanceInfo{
		mkApp("a1", "a1-old", time.Hour),
		mkApp("a1", "a1-new", 30*time.Minute),
		mkApp("a2", "a2-old", time.Hour),
		mkApp("a2", "a2-mid", 45*time.Minute),
		mkApp("a2", "a2-new", 15*time.Minute),
	}
	for i := range instances {
		instances[i].MinInstances = 1
	}
	got := ReapIdle(now, instances, nil, nil)
	if !equalSet(got, []string{"a1-old", "a2-old", "a2-mid"}) {
		t.Fatalf("got %v, want [a1-old a2-old a2-mid] (1 fresh per app kept)", got)
	}
}

// TestSelectEvictionsIgnoresMinInstances pins R5 from the plan:
// RAM-pressure eviction ignores the floor because the ceiling
// (inv §6.2-2) wins. Floor is budget, ceiling is physics.
func TestSelectEvictionsIgnoresMinInstances(t *testing.T) {
	now := time.Now()
	old := now.Add(-time.Hour)
	// 1 Pro instance, RAMMB 512, floor 1 → still evictable under
	// RAM pressure because SelectEvictions is intentionally
	// floor-blind (spec §6.2-2: ceiling wins).
	instances := []InstanceInfo{
		{Instance: "warm", AppID: "app1", Plan: api.PlanPro,
			State: state.StateRunning, RAMMB: 512,
			LastRequest: old, Started: old, MinInstances: 1},
	}
	got := SelectEvictions(EvictionThresholdMB+1, now, instances)
	if len(got) != 1 || got[0] != "warm" {
		t.Fatalf("RAM-pressure eviction must override the floor; got %v, want [warm]", got)
	}
}

// ---------------------------------------------------------------------
// ReapAggressive (issue #171): aggressive reaper scale-down based on
// the recent-load signal. The pure function takes a precomputed
// `desiredByApp` map and returns instance IDs to park.
//
// These tests are pure: a caller-supplied `now` and a hand-built
// []InstanceInfo drive every branch — no clock, no DB.
// ---------------------------------------------------------------------

// mkAggressive is a test helper: build an InstanceInfo with the
// fields ReapAggressive reads. Started is set to now-1h so the
// MinInstanceAge filter does not incidentally protect a row that
// the test means to be a reap candidate.
func mkAggressive(app, id string, lastSeen time.Duration, open int64, minInst int) InstanceInfo {
	now := time.Now()
	return InstanceInfo{
		Instance: id, AppID: app, Plan: api.PlanPro,
		State:        state.StateRunning,
		LastRequest:  now.Add(-lastSeen),
		Started:      now.Add(-time.Hour),
		OpenConns:    open,
		MinInstances: minInst,
	}
}

// TestReapAggressive_ParkToBuffer pins the headline acceptance
// scenario: 4 instances, desired=0, floor=1. The +1 hysteresis
// buffer keeps one warm, so 3 are parked; the freshest survives.
// (The 5-instance headline is covered end-to-end by
// TestLoopReaperAggressiveScalesDownOnDrop + the property test
// TestProperty_EngineReaper_BurstToIdle30s; the pure unit test
// sticks to 4 to keep the assertion small and explicit.)
func TestReapAggressive_ParkToBuffer(t *testing.T) {
	now := time.Now()
	instances := []InstanceInfo{
		mkAggressive("app1", "oldest", time.Hour, 0, 1),
		mkAggressive("app1", "older", 45*time.Minute, 0, 1),
		mkAggressive("app1", "newer", 30*time.Minute, 0, 1),
		mkAggressive("app1", "newest", 15*time.Minute, 0, 1),
	}
	got := ReapAggressive(now, instances, map[string]int{"app1": 0}, nil, nil)
	// limit = max(1, 0+1) = 1; extra = 4-1 = 3. Wait — 4 candidates, 3
	// extra → park 3, keep the freshest.
	if !equalSet(got, []string{"oldest", "older", "newer"}) {
		t.Fatalf("5-inst / desired=0 / floor=1: got %v, want [oldest older newer]", got)
	}
}

// TestReapAggressive_WithinBuffer: desired=2 means we want 2 warm.
// With +1 buffer, limit=3. running=3, extra=0 → no park.
func TestReapAggressive_WithinBuffer(t *testing.T) {
	now := time.Now()
	instances := []InstanceInfo{
		mkAggressive("app1", "a", time.Hour, 0, 0),
		mkAggressive("app1", "b", 45*time.Minute, 0, 0),
		mkAggressive("app1", "c", 30*time.Minute, 0, 0),
	}
	got := ReapAggressive(now, instances, map[string]int{"app1": 2}, nil, nil)
	if len(got) != 0 {
		t.Fatalf("3-inst / desired=2 / within buffer: got %v, want empty", got)
	}
}

// TestReapAggressive_JustAboveBuffer: desired=1, running=3,
// limit=max(0,1+1)=2, extra=1 → park 1 oldest.
func TestReapAggressive_JustAboveBuffer(t *testing.T) {
	now := time.Now()
	instances := []InstanceInfo{
		mkAggressive("app1", "oldest", time.Hour, 0, 0),
		mkAggressive("app1", "mid", 45*time.Minute, 0, 0),
		mkAggressive("app1", "newest", 30*time.Minute, 0, 0),
	}
	got := ReapAggressive(now, instances, map[string]int{"app1": 1}, nil, nil)
	if len(got) != 1 || got[0] != "oldest" {
		t.Fatalf("got %v, want [oldest]", got)
	}
}

// TestReapAggressive_OpenConnsProtect: G7 still wins. An instance
// with OpenConns > 0 counts toward running (it's live) but never
// enters the candidate set. Layout: 3 running, of which 1 has
// open flows. running=3, candidates=2, limit=max(0, 0+1)=1,
// extra=3-1=2 → park 2 oldest (BOTH flow-less instances). The
// open-conns one survives alone. The test pins that
// "open-conns protects" — the survivor set MUST include the
// flow-holding instance and the park set MUST NOT.
func TestReapAggressive_OpenConnsProtect(t *testing.T) {
	now := time.Now()
	instances := []InstanceInfo{
		mkAggressive("app1", "oldest", time.Hour, 0, 0),
		mkAggressive("app1", "open", 45*time.Minute, 3, 0),
		mkAggressive("app1", "newest", 30*time.Minute, 0, 0),
	}
	got := ReapAggressive(now, instances, map[string]int{"app1": 0}, nil, nil)
	if !equalSet(got, []string{"oldest", "newest"}) {
		t.Fatalf("got %v, want [oldest newest] (open-conns instance is the only survivor)", got)
	}
}

// TestReapAggressive_MinInstanceAgeProtects: a freshly-woken
// instance (Started = now-10s) must never be reaped by the
// aggressive path, even if the buffer says to. This is the same
// rule SelectEvictions enforces.
func TestReapAggressive_MinInstanceAgeProtects(t *testing.T) {
	now := time.Now()
	fresh := InstanceInfo{
		Instance: "fresh", AppID: "app1", Plan: api.PlanPro,
		State:        state.StateRunning,
		LastRequest:  now.Add(-time.Hour),
		Started:      now.Add(-10 * time.Second), // younger than MinInstanceAge (30s)
		MinInstances: 0,
	}
	instances := []InstanceInfo{
		fresh,
		mkAggressive("app1", "old", time.Hour, 0, 0),
	}
	got := ReapAggressive(now, instances, map[string]int{"app1": 0}, nil, nil)
	// fresh: not in candidates (too young). old: in candidates.
	// limit=max(0, 0+1)=1; extra=2-1=1; park 1 = "old".
	if !equalSet(got, []string{"old"}) {
		t.Fatalf("got %v, want [old] (young instance protected)", got)
	}
}

// TestReapAggressive_SingleInstanceApp: running=1 cannot exceed
// floor=0 + (desired+1) ≥ 1, so extra is always ≤ 0 → no park.
func TestReapAggressive_SingleInstanceApp(t *testing.T) {
	now := time.Now()
	instances := []InstanceInfo{mkAggressive("app1", "only", time.Hour, 0, 0)}
	got := ReapAggressive(now, instances, map[string]int{"app1": 0}, nil, nil)
	if len(got) != 0 {
		t.Fatalf("single instance must not be reaped, got %v", got)
	}
}

// TestReapAggressive_NilDesired_DefersToReapIdle: apps absent
// from desiredByApp are skipped entirely. Hobby/Free apps without
// autoscale configured must not be touched by the aggressive path.
func TestReapAggressive_NilDesired_DefersToReapIdle(t *testing.T) {
	now := time.Now()
	instances := []InstanceInfo{
		mkAggressive("hobby-app", "a", time.Hour, 0, 0),
		mkAggressive("hobby-app", "b", 45*time.Minute, 0, 0),
	}
	// desiredByApp has no entry for hobby-app.
	got := ReapAggressive(now, instances, nil, nil, nil)
	if len(got) != 0 {
		t.Fatalf("apps absent from desiredByApp must not be reaped, got %v", got)
	}
}

// TestReapAggressive_ZeroTarget_ClampsToFloor: desired=0,
// floor=2, running=5. limit=max(2, 0+1)=2, extra=3 → park 3
// oldest.
func TestReapAggressive_ZeroTarget_ClampsToFloor(t *testing.T) {
	now := time.Now()
	instances := []InstanceInfo{
		mkAggressive("app1", "oldest", time.Hour, 0, 2),
		mkAggressive("app1", "older", 45*time.Minute, 0, 2),
		mkAggressive("app1", "mid", 30*time.Minute, 0, 2),
		mkAggressive("app1", "newer", 15*time.Minute, 0, 2),
		mkAggressive("app1", "newest", 5*time.Minute, 0, 2),
	}
	got := ReapAggressive(now, instances, map[string]int{"app1": 0}, nil, nil)
	if !equalSet(got, []string{"oldest", "older", "mid"}) {
		t.Fatalf("got %v, want [oldest older mid] (floor 2 + 3 extra)", got)
	}
}

// TestReapAggressive_TwoApps: pins the per-app grouping loop and
// the per-app sort comparator. Two apps in the same snapshot, each
// with surplus. The function must park from each independently —
// no cross-app pollution from the candidate sort, and the output
// must be a union of both apps' parks (no double-counting, no
// missing row).
func TestReapAggressive_TwoApps(t *testing.T) {
	now := time.Now()
	instances := []InstanceInfo{
		// appA: 3 instances, desired=0, floor=1 → limit=1, extra=2.
		mkAggressive("appA", "appA_oldest", 2*time.Hour, 0, 1),
		mkAggressive("appA", "appA_older", 90*time.Minute, 0, 1),
		mkAggressive("appA", "appA_newest", 60*time.Minute, 0, 1),
		// appB: 4 instances, desired=1, floor=0 → limit=2, extra=2.
		mkAggressive("appB", "appB_oldest", 3*time.Hour, 0, 0),
		mkAggressive("appB", "appB_older", 2*time.Hour, 0, 0),
		mkAggressive("appB", "appB_newer", 90*time.Minute, 0, 0),
		mkAggressive("appB", "appB_newest", 60*time.Minute, 0, 0),
	}
	got := ReapAggressive(now, instances, map[string]int{
		"appA": 0,
		"appB": 1,
	}, nil, nil)
	want := []string{"appA_oldest", "appA_older", "appB_oldest", "appB_older"}
	if !equalSet(got, want) {
		t.Fatalf("two apps: got %v, want %v", got, want)
	}
}

// TestReapAggressive_FloorExceedsRunning: pins the "extra <= 0"
// branch. desired=0, floor=10, running=3. limit=max(10, 0+1)=10,
// extra=3-10=-7 → no park. The candidate sort is never invoked,
// but the function must not panic on the negative extra.
func TestReapAggressive_FloorExceedsRunning(t *testing.T) {
	now := time.Now()
	instances := []InstanceInfo{
		mkAggressive("app1", "a", time.Hour, 0, 10),
		mkAggressive("app1", "b", 45*time.Minute, 0, 10),
		mkAggressive("app1", "c", 30*time.Minute, 0, 10),
	}
	got := ReapAggressive(now, instances, map[string]int{"app1": 0}, nil, nil)
	if len(got) != 0 {
		t.Fatalf("floor (10) > running (3): got %v, want empty", got)
	}
}

// TestReapAggressive_EmptyCandidates: pins the "extra > len(candidates)"
// clamp. All instances are protected (OpenConns > 0 OR fresh).
// desired=0, floor=0, running=3, candidates=0. limit=max(0, 0+1)=1,
// extra=3-1=2, but len(candidates)=0 → no park. The function must
// NOT panic on the empty sort slice and must NOT return phantom IDs.
func TestReapAggressive_EmptyCandidates(t *testing.T) {
	now := time.Now()
	instances := []InstanceInfo{
		mkAggressive("app1", "open", 45*time.Minute, 3, 0),
		mkAggressive("app1", "fresh", time.Hour, 0, 0), // fresh is set by override below
	}
	// Override Started so the second instance is MinInstanceAge-young.
	instances[1].Started = now.Add(-10 * time.Second)
	got := ReapAggressive(now, instances, map[string]int{"app1": 0}, nil, nil)
	if len(got) != 0 {
		t.Fatalf("all candidates protected: got %v, want empty", got)
	}
}

// TestReapIdle_WorkerClassCarveOut pins ADR-051 PR-D: workers
// (cron workers, background consumers, no incoming HTTP) are
// reaper-exempt. A worker with stale LastRequest is NOT a
// candidate for idle parking — it has no per-request traffic,
// so LastRequest is meaningless. RAM pressure (SelectEvictions)
// is unchanged; this carve-out only affects ReapIdle and
// ReapAggressive.
func TestReapIdle_WorkerClassCarveOut(t *testing.T) {
	now := time.Now()
	// 1 worker + 1 http app, both stale LastRequest. Without the
	// carve-out, both would park. With it, only the http app parks.
	instances := []InstanceInfo{
		{
			Instance: "worker-1", AppID: "app1", Plan: api.PlanPro,
			State:         state.StateRunning,
			LastRequest:   now.Add(-10 * time.Minute), // stale
			Started:       now.Add(-time.Hour),
			WorkloadClass: state.WorkloadClassWorker,
		},
		{
			Instance: "http-1", AppID: "app2", Plan: api.PlanPro,
			State:         state.StateRunning,
			LastRequest:   now.Add(-10 * time.Minute), // stale
			Started:       now.Add(-time.Hour),
			WorkloadClass: state.WorkloadClassHTTP,
		},
	}
	got := ReapIdle(now, instances, nil, nil)
	if len(got) != 1 || got[0] != "http-1" {
		t.Fatalf("ReapIdle worker carve-out: got %v, want [http-1] (worker must be exempt)", got)
	}
}

// TestReapAggressive_WorkerClassCarveOut: the autoscale-driven
// path computes desired = ceil(rps/target). For a worker, RPS is
// undefined; if the loop used running+=1, the limit would compute
// extra = running - 1 and want to park everything above the first
// survivor. The worker carve-out must skip the addition entirely so
// workers don't inflate the running count and don't enter the
// candidate set.
func TestReapAggressive_WorkerClassCarveOut(t *testing.T) {
	now := time.Now()
	mk := func(id string, cls state.WorkloadClass) InstanceInfo {
		return InstanceInfo{
			Instance: id, AppID: "app1", Plan: api.PlanPro,
			State:         state.StateRunning,
			LastRequest:   now.Add(-time.Hour),
			Started:       now.Add(-time.Hour),
			WorkloadClass: cls,
		}
	}
	instances := []InstanceInfo{
		mk("worker-1", state.WorkloadClassWorker),
		mk("worker-2", state.WorkloadClassWorker),
		mk("http-1", state.WorkloadClassHTTP),
	}
	// desired=0, no autoscale signal → ReapAggressive defers to
	// ReapIdle. But here autoscale is on, so we want to see only
	// http-1 as a candidate. workers must not run, not enter the
	// candidate set, and not park.
	desired := map[string]int{"app1": 0}
	got := ReapAggressive(now, instances, desired, nil, nil)
	// Running=1 (only http-1), limit=max(0, 0+1)=1, extra=0 → no park.
	for _, id := range got {
		if id == "worker-1" || id == "worker-2" {
			t.Errorf("ReapAggressive carved-out worker; got %v, want no workers", id)
		}
	}
	// also with desired=10 — must NOT park workers under "running is 3".
	desired10 := map[string]int{"app1": 10}
	got = ReapAggressive(now, instances, desired10, nil, nil)
	for _, id := range got {
		if id == "worker-1" || id == "worker-2" {
			t.Errorf("ReapAggressive carved-out worker under desired=10; got %v", id)
		}
	}
}

// ---------------------------------------------------------------------
// Per-app scale-in cooldown consult (PR-C, issue #462).
//
// The reaper consults apps.LastScaleInAt + apps.ScalingPolicy.ScaleInCooldownS
// via the InstanceInfo carrier fields (LastScaleInAt *time.Time,
// ScaleInCooldownS int) populated by the loop wrapper at loop.go:799-801.
// When the consult fires (now - *LastScaleInAt < ScaleInCooldownS), the
// entire app is skipped: the candidates for that app are dropped from
// the park list. The "stamp missed" direction is safe — a nil
// LastScaleInAt bypasses the consult and the reaper proceeds normally.
// These tests pin both branches for ReapIdle and ReapAggressive.
// ---------------------------------------------------------------------

// TestReapIdleRespectsScaleInCooldownUnder pins the load-bearing
// consult: when now - LastScaleInAt < ScaleInCooldownS, ReapIdle
// returns an empty list (the app is in cooldown, all candidates
// are dropped). Layout: 2 stale Pro instances, cooldown=60s,
// lastScaleInAt=now-1s.
func TestReapIdleRespectsScaleInCooldownUnder(t *testing.T) {
	now := time.Now()
	last := now.Add(-1 * time.Second)
	instances := []InstanceInfo{
		{Instance: "idle-a", AppID: "app1", Plan: api.PlanPro, State: state.StateRunning,
			LastRequest: now.Add(-time.Hour), LastScaleInAt: &last, ScaleInCooldownS: 60},
		{Instance: "idle-b", AppID: "app1", Plan: api.PlanPro, State: state.StateRunning,
			LastRequest: now.Add(-45 * time.Minute), LastScaleInAt: &last, ScaleInCooldownS: 60},
	}
	got := ReapIdle(now, instances, nil, nil)
	if len(got) != 0 {
		t.Errorf("ReapIdle (cooldown 59s remaining) = %v, want [] (cooldown_held)", got)
	}
}

// TestReapIdleRespectsScaleInCooldownOver pins the bypass branch:
// when now - LastScaleInAt > ScaleInCooldownS, the consult
// doesn't fire and the reaper proceeds normally. Same layout as
// the under test but with lastScaleInAt=now-2m so 60s has elapsed.
func TestReapIdleRespectsScaleInCooldownOver(t *testing.T) {
	now := time.Now()
	last := now.Add(-2 * time.Minute)
	instances := []InstanceInfo{
		{Instance: "idle-a", AppID: "app1", Plan: api.PlanPro, State: state.StateRunning,
			LastRequest: now.Add(-time.Hour), LastScaleInAt: &last, ScaleInCooldownS: 60},
		{Instance: "idle-b", AppID: "app1", Plan: api.PlanPro, State: state.StateRunning,
			LastRequest: now.Add(-45 * time.Minute), LastScaleInAt: &last, ScaleInCooldownS: 60},
	}
	got := ReapIdle(now, instances, nil, nil)
	if !equalSet(got, []string{"idle-a", "idle-b"}) {
		t.Errorf("ReapIdle (cooldown elapsed) = %v, want [idle-a idle-b]", got)
	}
}

// TestReapIdleEmitsCooldownHeld (P1D) pins that when the per-app
// scale-in cooldown consult fires, ReapIdle emits
// schedd_scale_down_decisions_total{outcome="cooldown_held"} exactly
// once per app per tick (the per-row loop body would otherwise fire
// for every RUNNING instance in the app). Mirrors
// TestReapAggressiveEmitsCooldownHeld in loop_test.go but at the
// selector boundary so the metric contract is pinned independently
// of the loop wrapper.
func TestReapIdleEmitsCooldownHeld(t *testing.T) {
	now := time.Now()
	last := now.Add(-1 * time.Second)
	instances := []InstanceInfo{
		{Instance: "idle-a", AppID: "app1", Plan: api.PlanPro, State: state.StateRunning,
			LastRequest: now.Add(-time.Hour), LastScaleInAt: &last, ScaleInCooldownS: 60},
		{Instance: "idle-b", AppID: "app1", Plan: api.PlanPro, State: state.StateRunning,
			LastRequest: now.Add(-45 * time.Minute), LastScaleInAt: &last, ScaleInCooldownS: 60},
		{Instance: "idle-c", AppID: "app1", Plan: api.PlanPro, State: state.StateRunning,
			LastRequest: now.Add(-30 * time.Minute), LastScaleInAt: &last, ScaleInCooldownS: 60},
	}
	ops := wire.NewOpsMetrics("schedd")
	got := ReapIdle(now, instances, ops, nil)
	if len(got) != 0 {
		t.Errorf("ReapIdle (cooldown held) = %v, want []", got)
	}
	// Cooldown held → 1 observation (NOT 3, despite 3 RUNNING
	// instances — the cooldownEmitted flag is the load-bearing
	// contract here). `park` must NOT fire because the app was
	// skipped entirely.
	body := wireRenderMetrics(t, ops)
	if !bytes.Contains(body, []byte(`schedd_scale_down_decisions_total{app="app1",outcome="cooldown_held"} 1`)) {
		t.Errorf("missing cooldown_held=1 line in:\n%s", body)
	}
	if bytes.Contains(body, []byte(`schedd_scale_down_decisions_total{app="app1",outcome="park"}`)) {
		t.Errorf("unexpected park line for cooldown-held app in:\n%s", body)
	}
}

// TestReapIdleEmitsPark (P1D) pins that ReapIdle emits
// schedd_scale_down_decisions_total{outcome="park"} exactly once
// per app per tick when at least one instance is parked, even
// when multiple instances are parked for the same app in the
// same tick. Layout: 3 stale Pro instances, floor=0, no cooldown.
// All 3 are parked; one observation fires.
func TestReapIdleEmitsPark(t *testing.T) {
	now := time.Now()
	instances := []InstanceInfo{
		mkAggressive("app1", "a", time.Hour, 0, 0),
		mkAggressive("app1", "b", 45*time.Minute, 0, 0),
		mkAggressive("app1", "c", 30*time.Minute, 0, 0),
	}
	ops := wire.NewOpsMetrics("schedd")
	got := ReapIdle(now, instances, ops, nil)
	if !equalSet(got, []string{"a", "b", "c"}) {
		t.Errorf("ReapIdle = %v, want [a b c]", got)
	}
	body := wireRenderMetrics(t, ops)
	if !bytes.Contains(body, []byte(`schedd_scale_down_decisions_total{app="app1",outcome="park"} 1`)) {
		t.Errorf("missing park=1 line in:\n%s", body)
	}
	// And no `min_floor_already` for the same app — the floor was
	// zero, so the floor branch never fires.
	if bytes.Contains(body, []byte(`schedd_scale_down_decisions_total{app="app1",outcome="min_floor_already"}`)) {
		t.Errorf("unexpected min_floor_already line in:\n%s", body)
	}
}

// TestReapIdleEmitsMinFloorAlready (P1D) pins the floor-kept
// branch: when candidates exist but the floor blocks every one
// (allowed == 0 with floor > 0), ReapIdle emits
// schedd_scale_down_decisions_total{outcome="min_floor_already"}
// once per app per tick. Layout: 2 stale Pro instances, floor=2,
// no cooldown. Both candidates exist but neither can be parked
// because the floor says keep both alive. No `park` observation.
func TestReapIdleEmitsMinFloorAlready(t *testing.T) {
	now := time.Now()
	instances := []InstanceInfo{
		mkAggressive("app1", "a", time.Hour, 0, 2), // floor=2
		mkAggressive("app1", "b", 45*time.Minute, 0, 2),
	}
	ops := wire.NewOpsMetrics("schedd")
	got := ReapIdle(now, instances, ops, nil)
	if len(got) != 0 {
		t.Errorf("ReapIdle = %v, want [] (floor kept all candidates)", got)
	}
	body := wireRenderMetrics(t, ops)
	if !bytes.Contains(body, []byte(`schedd_scale_down_decisions_total{app="app1",outcome="min_floor_already"} 1`)) {
		t.Errorf("missing min_floor_already=1 line in:\n%s", body)
	}
	if bytes.Contains(body, []byte(`schedd_scale_down_decisions_total{app="app1",outcome="park"}`)) {
		t.Errorf("unexpected park line in:\n%s", body)
	}
}

// TestReapIdle_NilMetrics_NoPanic (P1D) pins the nil-safety
// contract on the new metrics parameter. The selector must
// return the same park slice it would have returned with a
// non-nil metrics (or with the no-metrics fixture default) and
// must NOT panic on the nil receiver dereferences that
// ObserveScaleDown would normally guard via its own nil-receiver
// check (reaper.go never calls ObserveScaleDown when metrics==nil,
// so this is the load-bearing test for that contract).
//
// Layout: two apps. app1 is in cooldown (cooldown_held would fire
// with non-nil metrics). app2 has no cooldown and one stale
// instance (park would fire with non-nil metrics). With nil
// metrics, neither emission runs but the pure return value is
// unchanged: app2's stale instance is parked, app1 contributes
// nothing.
func TestReapIdle_NilMetrics_NoPanic(t *testing.T) {
	now := time.Now()
	last := now.Add(-1 * time.Second)
	instances := []InstanceInfo{
		{Instance: "idle-a", AppID: "app1", Plan: api.PlanPro, State: state.StateRunning,
			LastRequest: now.Add(-time.Hour), LastScaleInAt: &last, ScaleInCooldownS: 60},
		mkAggressive("app2", "b", time.Hour, 0, 0),
	}
	got := ReapIdle(now, instances, nil, nil)
	if !equalSet(got, []string{"b"}) {
		t.Errorf("ReapIdle = %v, want [b]", got)
	}
}

// TestReapAggressiveRespectsScaleInCooldownUnder mirrors the
// ReapIdle consult for the aggressive path. Layout: 3 stale
// Pro instances, desired=0, cooldown=60s, lastScaleInAt=now-1s.
// All 3 candidates are dropped (cooldown_held); desired=0 would
// otherwise park all 3 + the +1 buffer (running=3, limit=1, extra=2).
func TestReapAggressiveRespectsScaleInCooldownUnder(t *testing.T) {
	now := time.Now()
	last := now.Add(-1 * time.Second)
	instances := []InstanceInfo{
		mkAggressiveWithStamp("app1", "a", time.Hour, 0, 0, &last, 60),
		mkAggressiveWithStamp("app1", "b", 45*time.Minute, 0, 0, &last, 60),
		mkAggressiveWithStamp("app1", "c", 30*time.Minute, 0, 0, &last, 60),
	}
	got := ReapAggressive(now, instances, map[string]int{"app1": 0}, nil, nil)
	if len(got) != 0 {
		t.Errorf("ReapAggressive (cooldown 59s remaining) = %v, want [] (cooldown_held)", got)
	}
}

// TestReapAggressiveRespectsScaleInCooldownOver pins the bypass
// branch for the aggressive path. Same layout but with
// lastScaleInAt=now-2m so the cooldown has elapsed and the
// reaper proceeds normally: running=3, desired=0, limit=1, extra=2 →
// park the 2 oldest.
func TestReapAggressiveRespectsScaleInCooldownOver(t *testing.T) {
	now := time.Now()
	last := now.Add(-2 * time.Minute)
	instances := []InstanceInfo{
		mkAggressiveWithStamp("app1", "a", time.Hour, 0, 0, &last, 60),
		mkAggressiveWithStamp("app1", "b", 45*time.Minute, 0, 0, &last, 60),
		mkAggressiveWithStamp("app1", "c", 30*time.Minute, 0, 0, &last, 60),
	}
	got := ReapAggressive(now, instances, map[string]int{"app1": 0}, nil, nil)
	if !equalSet(got, []string{"a", "b"}) {
		t.Errorf("ReapAggressive (cooldown elapsed) = %v, want [a b] (park 2 oldest)", got)
	}
}

// mkAggressiveWithStamp is the cooldown-aware cousin of mkAggressive.
// It also stamps the LastScaleInAt + ScaleInCooldownS carrier fields
// (PR-C, issue #462) so the per-app consult at reaper.go:160 / :271
// has the inputs it needs.
func mkAggressiveWithStamp(app, id string, lastSeen time.Duration, open int64, minInst int, lastScaleInAt *time.Time, cooldownS int) InstanceInfo {
	now := time.Now()
	return InstanceInfo{
		Instance: id, AppID: app, Plan: api.PlanPro,
		State:            state.StateRunning,
		LastRequest:      now.Add(-lastSeen),
		Started:          now.Add(-time.Hour),
		OpenConns:        open,
		MinInstances:     minInst,
		LastScaleInAt:    lastScaleInAt,
		ScaleInCooldownS: cooldownS,
	}
}

// TestSelectEvictions_TierOrdering (issue #475) pins the new
// comparator precedence: best_effort before reserved, then
// non-Scale before Scale, then LRU, then instance id. We construct
// candidates with identical LRU + plan + age so the only tiebreaker
// is the tier — the test only passes if the new comparator lands
// at rank 0.
func TestSelectEvictions_TierOrdering(t *testing.T) {
	now := time.Now()
	old := now.Add(-time.Hour)
	instances := []InstanceInfo{
		{Instance: "reserved", Plan: api.PlanPro, State: state.StateRunning, RAMMB: 512, LastRequest: old, Started: old, EvictionPriority: string(api.EvictionPriorityReserved)},
		{Instance: "best", Plan: api.PlanPro, State: state.StateRunning, RAMMB: 512, LastRequest: old, Started: old, EvictionPriority: string(api.EvictionPriorityBestEffort)},
	}
	got := SelectEvictions(EvictionThresholdMB+1, now, instances)
	if len(got) != 1 || got[0] != "best" {
		t.Errorf("tier comparator must pick best_effort first, got %v", got)
	}
}

// TestSelectEvictions_ReservedIsLastResort (issue #475) is the
// success criterion: under RAM pressure the reaper must drain every
// best_effort candidate before any reserved instance is parked.
// Construct a mix where best_effort accounts for ~3 GB of RAM and
// reserved accounts for ~1 GB; pick a target that only fits the
// best_effort side. The reserved instance must survive.
func TestSelectEvictions_ReservedIsLastResort(t *testing.T) {
	now := time.Now()
	old := now.Add(-time.Hour)
	instances := []InstanceInfo{
		{Instance: "best-e", Plan: api.PlanPro, State: state.StateRunning, RAMMB: 512, LastRequest: old, Started: old, EvictionPriority: string(api.EvictionPriorityBestEffort)},
		{Instance: "best-w", Plan: api.PlanPro, State: state.StateRunning, RAMMB: 512, LastRequest: old, Started: old, EvictionPriority: string(api.EvictionPriorityBestEffort)},
		{Instance: "reserved-only", Plan: api.PlanPro, State: state.StateRunning, RAMMB: 512, LastRequest: old, Started: old, EvictionPriority: string(api.EvictionPriorityReserved)},
	}
	// Just 1 MB over threshold → 1 park total. Must be a best_effort
	// instance, NOT the reserved one.
	got := SelectEvictions(EvictionThresholdMB+1, now, instances)
	if len(got) != 1 {
		t.Fatalf("expected 1 park, got %v", got)
	}
	if got[0] == "reserved-only" {
		t.Errorf("reserved instance must be parked last; got %v", got)
	}
}

// TestSelectEvictions_AllReservedEvictable (issue #475) is the
// safety net: when the box is fully out of best_effort candidates
// the reserved pool does fall through to eviction. A reserved app
// is NOT a Lambda-style provisioned-concurrency pool — it is
// protected from cross-account RAM pressure until every best_effort
// candidate is exhausted, then it participates in the same
// LRU-by-last_request_at ordering as everything else.
func TestSelectEvictions_AllReservedEvictable(t *testing.T) {
	now := time.Now()
	old := now.Add(-time.Hour)
	instances := []InstanceInfo{
		{Instance: "reserved-oldest", Plan: api.PlanPro, State: state.StateRunning, RAMMB: 512, LastRequest: old.Add(-time.Hour), Started: old, EvictionPriority: string(api.EvictionPriorityReserved)},
		{Instance: "reserved-newest", Plan: api.PlanPro, State: state.StateRunning, RAMMB: 512, LastRequest: now.Add(-time.Minute), Started: old, EvictionPriority: string(api.EvictionPriorityReserved)},
	}
	// Big eviction target — both reserved instances must participate.
	got := SelectEvictions(EvictionThresholdMB+2080, now, instances)
	if len(got) != 2 {
		t.Errorf("expected both reserved instances to park when best_effort is exhausted, got %v", got)
	}
	if len(got) == 2 && got[0] != "reserved-oldest" {
		t.Errorf("within reserved tier, LRU must still hold; got %v", got)
	}
}

// TestSelectEvictions_EmptyEvictionPriorityFallsThrough (issue #475)
// pins the pre-#475 behaviour for any pre-existing carrier stamp that
// has EvictionPriority == "". The sort comparator treats the empty
// string as !reserved, so the pre-#475 LRU ordering is preserved
// bit-for-bit. This guards against a regression where the new
// comparator accidentally promotes "" to the reserved tier.
func TestSelectEvictions_EmptyEvictionPriorityFallsThrough(t *testing.T) {
	now := time.Now()
	old := now.Add(-time.Hour)
	instances := []InstanceInfo{
		// No EvictionPriority set — pre-#475 shape.
		{Instance: "legacy-oldest", Plan: api.PlanPro, State: state.StateRunning, RAMMB: 512, LastRequest: old, Started: old},
		// New column explicitly 'reserved'.
		{Instance: "reserved-new", Plan: api.PlanPro, State: state.StateRunning, RAMMB: 512, LastRequest: old, Started: old, EvictionPriority: string(api.EvictionPriorityReserved)},
	}
	got := SelectEvictions(EvictionThresholdMB+1, now, instances)
	if len(got) != 1 || got[0] != "legacy-oldest" {
		t.Errorf("empty EvictionPriority must fall through to best_effort path; got %v", got)
	}
}

// TestResolvePriority (issue #475) pins the helper that maps an
// instance id to its per-app Tier label for the per-tier eviction
// counter. The boolean return is false when the id is not in the
// snapshot (a benign race: the instance was already parked by an
// earlier branch in the same tick). The empty-string fallback
// coerces to 'best_effort' to match the historical default.
func TestResolvePriority(t *testing.T) {
	now := time.Now()
	old := now.Add(-time.Hour)
	snapshot := []InstanceInfo{
		{Instance: "reserved-app", AppID: "a", Plan: api.PlanPro, State: state.StateRunning, RAMMB: 512, LastRequest: old, Started: old, EvictionPriority: string(api.EvictionPriorityReserved)},
		{Instance: "legacy-app", AppID: "b", Plan: api.PlanPro, State: state.StateRunning, RAMMB: 512, LastRequest: old, Started: old},
	}
	if got, ok := resolvePriority(snapshot, "reserved-app"); !ok || got != "reserved" {
		t.Errorf("reserved lookup = (%q, %v), want (\"reserved\", true)", got, ok)
	}
	if got, ok := resolvePriority(snapshot, "legacy-app"); !ok || got != "best_effort" {
		t.Errorf("empty-string fallback = (%q, %v), want (\"best_effort\", true)", got, ok)
	}
	if got, ok := resolvePriority(snapshot, "missing"); ok || got != "" {
		t.Errorf("missing id = (%q, %v), want (\"\", false)", got, ok)
	}
}

// TestReapIdleNilMetrics_DoesNotPoisonSharedSet (P1D / code-review
// finding 2 regression pin) — pins the load-bearing invariant that
// ReapIdle, when called with nil metrics, must NOT record the
// cooldown-skipped app into the shared cooldownHeldByApp set.
// Otherwise, when ReapAggressive is called next with non-nil metrics
// in the same tick, its `alreadySeen` check would falsely suppress
// the emission and the observation is silently dropped.
//
// Layout: one app, three stale Pro instances in cooldown. Call
// ReapIdle with nil metrics (record should NOT happen); then call
// ReapAggressive with non-nil metrics on the same shared set. The
// aggressive emission must fire and the final metric value must be
// exactly 1.
func TestReapIdleNilMetrics_DoesNotPoisonSharedSet(t *testing.T) {
	now := time.Now()
	last := now.Add(-1 * time.Second)
	instances := []InstanceInfo{
		{Instance: "a", AppID: "app1", Plan: api.PlanPro, State: state.StateRunning,
			LastRequest: now.Add(-time.Hour), LastScaleInAt: &last, ScaleInCooldownS: 60},
		{Instance: "b", AppID: "app1", Plan: api.PlanPro, State: state.StateRunning,
			LastRequest: now.Add(-45 * time.Minute), LastScaleInAt: &last, ScaleInCooldownS: 60},
	}
	// Step 1: ReapIdle with nil metrics. No emission; set must
	// remain empty (the load-bearing assertion).
	shared := map[string]struct{}{}
	if got := ReapIdle(now, instances, nil, shared); len(got) != 0 {
		t.Fatalf("ReapIdle = %v, want [] (cooldown held)", got)
	}
	if len(shared) != 0 {
		t.Errorf("ReapIdle(nil metrics) poisoned shared set: %v (want empty)", shared)
	}
	// Step 2: ReapAggressive with non-nil metrics on the SAME shared
	// set. Must NOT see the app as "already seen" because idle didn't
	// emit — must emit the observation itself.
	ops := wire.NewOpsMetrics("schedd")
	desiredByApp := map[string]int{"app1": 0}
	if got := ReapAggressive(now, instances, desiredByApp, ops, shared); len(got) != 0 {
		t.Errorf("ReapAggressive = %v, want [] (cooldown held)", got)
	}
	body := wireRenderMetrics(t, ops)
	want := `schedd_scale_down_decisions_total{app="app1",outcome="cooldown_held"} 1`
	if !bytes.Contains(body, []byte(want)) {
		t.Errorf("missing line %q in:\n%s", want, body)
	}
	// And the shared set must contain the app — the aggressive
	// emission now records it (so a third observer downstream would
	// see it).
	if _, ok := shared["app1"]; !ok {
		t.Errorf("ReapAggressive did not record into shared set: %v", shared)
	}
}

// TestReapIdleSkipsMirrorInstances is the issue #72 / ADR-125 mirror
// carve-out guard for the reaper. A mirror instance (Mode="mirror")
// self-parks on completion (runMirror's defer), so it never reaches
// the idle-reap path with a stale LastRequest — but if a wake
// stalls or the customer disconnects mid-mirror, the instance
// could otherwise sit idle in state.StateRunning with no customer
// serving it. The reaper must skip those rows: they're not the
// customer's instance, the customer already has source's response,
// and reaping them via the idle path would race the deferred
// parkMirrorInstance.
//
// Mirrors TestReapIdleSkipsInstanceWithOpenConns /
// TestReapIdleSkipsInstanceWithTailCount shape: a normal-but-stale
// control row confirms the reaper still works for non-mirror
// instances.
func TestReapIdleSkipsMirrorInstances(t *testing.T) {
	now := time.Now()
	instances := []InstanceInfo{
		// Mirror instance, stale LastRequest: must NOT be reaped
		// because Mode=="mirror". The mirror goroutine handles its
		// own lifecycle.
		{Instance: "mirror-stale", Plan: api.PlanPro, State: state.StateRunning,
			Mode: string(state.InstanceModeMirror), LastRequest: now.Add(-time.Hour)},
		// No regression: a stale normal instance still gets reaped.
		{Instance: "idle", Plan: api.PlanPro, State: state.StateRunning,
			LastRequest: now.Add(-time.Hour)},
	}
	got := ReapIdle(now, instances, nil, nil)
	if !equalSet(got, []string{"idle"}) {
		t.Errorf("ReapIdle = %v, want [idle] only (mirror instance must be skipped)", got)
	}
}
