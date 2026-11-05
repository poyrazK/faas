// adr: 20261102124306 — step 3: the RAM an app under memory pressure needs.
package api

import "fmt"

// MemoryRightSizing is the RAM an app under memory pressure needs: enough
// that its lowest point would have left ProfileMemoryPressureRearmAvailablePercent
// of the guest's memory available, the level at which the guest stops
// counting an episode.
type MemoryRightSizing struct {
	// CurrentMB is the app's ram_mb; SuggestedMB the RAM it needs, rounded up
	// to MemoryRightSizeStepMB and always above CurrentMB.
	CurrentMB, SuggestedMB int
	// Plan and PlanMB are the account's plan and the most RAM it allows.
	Plan   Plan
	PlanMB int
	// FitsPlan reports SuggestedMB ≤ PlanMB. When it does not, UpgradePlan is
	// the smallest plan that allows SuggestedMB, or "" when none does.
	FitsPlan    bool
	UpgradePlan Plan
	UpgradeMB   int
}

// RightSizeMemory works out the RAM an app needs from the low point of its
// memory-pressure rollup: the guest's total memory and the least of it that
// was available. ok is false for an unknown plan or a rollup that says
// nothing about use.
func RightSizeMemory(plan Plan, appRAMMB, guestTotalMB, lowestAvailableMB int) (MemoryRightSizing, bool) {
	limits, ok := LimitsFor(plan)
	if !ok || guestTotalMB <= 0 || lowestAvailableMB < 0 || lowestAvailableMB >= guestTotalMB {
		return MemoryRightSizing{}, false
	}
	current := appRAMMB
	if current <= 0 {
		current = StandaloneAppDefaultRAMMB(limits)
	}
	// The guest kernel keeps current - guestTotalMB for itself whatever the
	// app's size; the app's use must fit in the rest with headroom to spare.
	used := guestTotalMB - lowestAvailableMB
	free := 100 - ProfileMemoryPressureRearmAvailablePercent
	need := max(current-guestTotalMB, 0) + (used*100+free-1)/free
	step := MemoryRightSizeStepMB
	need = (need + step - 1) / step * step
	if need <= current {
		need = (current/step + 1) * step
	}
	out := MemoryRightSizing{CurrentMB: current, SuggestedMB: need, Plan: plan, PlanMB: limits.RAMMB, FitsPlan: need <= limits.RAMMB}
	if out.FitsPlan {
		return out, true
	}
	for _, p := range Plans {
		if l, ok := LimitsFor(p); ok && l.RAMMB >= need && l.RAMMB > limits.RAMMB {
			out.UpgradePlan, out.UpgradeMB = p, l.RAMMB
			break
		}
	}
	return out, true
}

// Advice says how much RAM would have left
// ProfileMemoryPressureRearmAvailablePercent free at the low point, and
// whether the plan allows it. App health and debug status both show it.
func (s MemoryRightSizing) Advice() string {
	need := fmt.Sprintf("about %d MB would have kept %d%% free", s.SuggestedMB, ProfileMemoryPressureRearmAvailablePercent)
	switch {
	case s.FitsPlan:
		return fmt.Sprintf("Raise ram_mb from %d MB: %s, and the %s plan allows up to %d MB.", s.CurrentMB, need, s.Plan, s.PlanMB)
	case s.UpgradePlan != "":
		return fmt.Sprintf("At %d MB, %s, more than the %s plan's %d MB; the %s plan allows %d MB.", s.CurrentMB, need, s.Plan, s.PlanMB, s.UpgradePlan, s.UpgradeMB)
	default:
		return fmt.Sprintf("At %d MB, %s, more than any plan allows; trim in-memory state such as caches and buffered request bodies.", s.CurrentMB, need)
	}
}
