package builderd

import "fmt"

// slot.go — builder slot gating (CLAUDE.md "Builder slots").
//
// The control plane fences builder RAM via cgroups (spec §13): a control-plane
// leak OOMs the control plane, never tenants. Within that envelope:
//
//   - 1 guaranteed builder slot — always available, lives in faas-cp.slice.
//   - 1 opportunistic builder slot — only when tenant residency < 60% of the
//     admission ceiling (the gate below). The 60% threshold is the financial
//     model's safe headroom for tenant spikes (spec §1, §4.5).
//
// Schedd's Ledger is the authoritative source for tenant residency. builderd
// does NOT call schedd over its gRPC socket here — instead cmd/builderd polls
// schedd's metric /metrics on the interval and caches the result. A nil
// ResidencyProbe in this package's ProcessOne path means "1 slot only, no
// opportunistic ask" — the safe default when schedd is unreachable.

// SlotDecision is the outcome of a slot allocation request.
type SlotDecision struct {
	Allowed bool
	Label   string // "guaranteed" | "opportunistic"
	Reason  string // populated when !Allowed
}

// slotThresholdFraction is the tenant residency fraction below which the
// opportunistic 2nd builder slot is granted (spec §13).
const slotThresholdFraction = 0.60

// DecideSlot computes whether a builder spawn is allowed, given the current
// tenant residency. residentMB and ceilingMB are in megabytes; a nil
// residencyProbe is treated as "always allow 1 guaranteed slot only".
func DecideSlot(resid ResidencyProbe, ceilingMB int) SlotDecision {
	if resid == nil {
		// No probe ⇒ we can't see tenant RAM. Be conservative: allow the
		// guaranteed slot only. The opportunistic one waits until a
		// ResidencyProbe is wired.
		return SlotDecision{Allowed: true, Label: "guaranteed", Reason: "no residency probe"}
	}
	cur := resid.ResidentMB()
	if ceilingMB <= 0 {
		return SlotDecision{Allowed: true, Label: "guaranteed", Reason: "no ceiling"}
	}
	frac := float64(cur) / float64(ceilingMB)
	if frac >= slotThresholdFraction {
		return SlotDecision{
			Allowed: true,
			Label:   "guaranteed",
			Reason:  fmt.Sprintf("tenant residency %.0f%% >= threshold %.0f%%; opportunistic slot denied", frac*100, slotThresholdFraction*100),
		}
	}
	return SlotDecision{
		Allowed: true,
		Label:   "opportunistic",
		Reason:  fmt.Sprintf("tenant residency %.0f%% < threshold %.0f%%; opportunistic slot granted", frac*100, slotThresholdFraction*100),
	}
}
