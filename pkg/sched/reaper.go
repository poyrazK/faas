package sched

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Idle reaper + eviction selection (spec §4.3). Both are pure functions over a
// snapshot of instance metadata so the policy is unit-tested without a clock or a
// database; schedd calls them on its 10 s tick and under RAM pressure.

// EvictionThresholdMB is the RAM level above which schedd starts evicting: 80% of
// the 85% admission target (spec §4.3). Below it, nothing is evicted.
const EvictionThresholdMB = api.RAMAdmissionCeilingMB * 80 / 100 // 38,080

// MinInstanceAge protects a freshly-woken instance from being reaped/evicted
// before it has had a chance to serve (spec §4.3: never evict younger than 30 s).
const MinInstanceAge = 30 * time.Second

// InstanceInfo is the snapshot schedd hands the selectors for one instance.
type InstanceInfo struct {
	Instance     string
	AppID        string
	Plan         api.Plan
	State        state.State
	RAMMB        int
	LastRequest  time.Time
	Started      time.Time
	IdleTimeoutS int // app-configured; 0 => plan default
}

func (i InstanceInfo) admissionMB() int { return i.RAMMB + api.PerVMOverheadMB }

// EffectiveIdleTimeoutS resolves an app's idle timeout: the plan default unless
// the app configured one within bounds (floor 10 s, ceiling plan default × 2,
// spec §4.3).
func EffectiveIdleTimeoutS(plan api.Plan, configured int) int {
	l := api.MustLimitsFor(plan)
	if configured <= 0 {
		return l.IdleTimeoutS
	}
	floor, ceiling := l.IdleTimeoutBounds()
	switch {
	case configured < floor:
		return floor
	case configured > ceiling:
		return ceiling
	default:
		return configured
	}
}

// ReapIdle returns the instances to park for idleness: RUNNING instances whose
// time since last request exceeds their effective idle timeout (spec §4.3).
func ReapIdle(now time.Time, instances []InstanceInfo) []string {
	var park []string
	for _, in := range instances {
		if in.State != state.StateRunning {
			continue
		}
		timeout := time.Duration(EffectiveIdleTimeoutS(in.Plan, in.IdleTimeoutS)) * time.Second
		if now.Sub(in.LastRequest) > timeout {
			park = append(park, in.Instance)
		}
	}
	return park
}

// SelectEvictions returns instances to park to bring residentMB down to the
// eviction threshold, in eviction order (spec §4.3): LRU by last request, never
// an instance younger than MinInstanceAge, Scale plan evicted last. It returns
// nothing when resident RAM is at or below the threshold.
func SelectEvictions(residentMB int, now time.Time, instances []InstanceInfo) []string {
	if residentMB <= EvictionThresholdMB {
		return nil
	}

	// Candidates: running instances old enough to evict.
	var cands []InstanceInfo
	for _, in := range instances {
		if in.State != state.StateRunning {
			continue
		}
		if now.Sub(in.Started) < MinInstanceAge {
			continue
		}
		cands = append(cands, in)
	}

	// Order: non-Scale before Scale (Scale evicted last), then oldest last
	// request first (LRU), then instance id for determinism.
	sort.Slice(cands, func(a, b int) bool {
		as, bs := cands[a].Plan == api.PlanScale, cands[b].Plan == api.PlanScale
		if as != bs {
			return !as // non-Scale first
		}
		if !cands[a].LastRequest.Equal(cands[b].LastRequest) {
			return cands[a].LastRequest.Before(cands[b].LastRequest)
		}
		return cands[a].Instance < cands[b].Instance
	})

	// Greedily evict until resident drops to the threshold.
	var evict []string
	remaining := residentMB
	for _, in := range cands {
		if remaining <= EvictionThresholdMB {
			break
		}
		evict = append(evict, in.Instance)
		remaining -= in.admissionMB()
	}
	return evict
}
