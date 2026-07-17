package sched

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
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
	got := ReapIdle(now, instances)
	if !equalSet(got, []string{"idle", "free-idle"}) {
		t.Errorf("ReapIdle = %v, want [idle free-idle]", got)
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
	got := ReapIdle(now, instances)
	if !equalSet(got, []string{"idle"}) {
		t.Errorf("ReapIdle = %v, want [idle] only", got)
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
