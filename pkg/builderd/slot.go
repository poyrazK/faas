package builderd

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// slot.go — builder slot gating (CLAUDE.md "Builder slots").
//
// The control plane fences builder RAM via cgroups (spec §13): a control-plane
// leak OOMs the control plane, never tenants. Within that envelope:
//
//   - N guaranteed builder slots — always available, in faas-cp-build.slice.
//     N is the node's provisioned reserved slots (FAAS_BUILDER_RESERVED_SLOTS,
//     default 1; ADR-20261118154632 amendment 1), and node_join sizes the
//     build slice and the tenant fence for exactly that many.
//   - 1 opportunistic slot — only when tenant residency is below 60% AND the
//     builder slice has secondSlotNeedMB free. A slice sized for N builders
//     never does while N builds run, so the scopes cannot overrun the fence;
//     ops open the slot by raising faas_cp_build_slice_memory_max.
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

// slotThresholdFraction is the tenant-residency fraction below which the
// opportunistic slot may open.
const slotThresholdFraction = 0.60

// maxBuilderSlots is a one-slot node's limit: one guaranteed build plus one
// opportunistic build. The opportunistic one runs only when tenant residency
// is below slotThresholdFraction AND the builder slice has room for a whole
// extra builder (secondSlotNeedMB), which a slice sized for its guaranteed
// builders never has while they run: raising faas_cp_build_slice_memory_max
// (and faas_cp_slice_memory_max) turns it on (retail-store feedback A3,
// 2026-11-17). A node provisioned with N reserved slots allows N+1
// (fixedSlotLimit).
const maxBuilderSlots = 2

// secondSlotNeedMB is a second builder's worst case inside the slice: a full
// snapshot charges its guest RAM twice plus the VMM overhead, and the slice
// keeps BuilderSecondSlotHeadroomMB beyond that.
const secondSlotNeedMB = 2*api.BuildVMRAMMB + api.BuildVMOverheadMB + api.BuilderSecondSlotHeadroomMB

// fixedSlotLimit is the fixed allocator's limit: the node's reserved slots
// plus one opportunistic slot.
func (b *Builderd) fixedSlotLimit() int {
	return b.reservedSlots() + maxBuilderSlots - 1
}

// acquireSlot reserves one of the process-wide builder slots. The first
// reservedSlots() active builds hold guaranteed slots. The returned release
// function is idempotent so every caller can defer it safely.
func (b *Builderd) acquireSlot() (SlotDecision, func(), bool) {
	b.slotMu.Lock()
	defer b.slotMu.Unlock()

	reserved := b.reservedSlots()
	if b.activeSlots >= b.fixedSlotLimit() {
		b.pipeline.observeAdmission("limit")
		return SlotDecision{
			Allowed: false,
			Label:   "none",
			Reason:  "builder slot limit reached",
		}, nil, false
	}
	decider := b.slotDecide
	if decider == nil {
		decider = DecideSlot
	}
	decision := decider(b.resid, api.RAMAdmissionCeilingMB)
	if !decision.Allowed {
		b.pipeline.observeAdmission("policy")
		return decision, nil, false
	}
	if b.activeSlots >= reserved && decision.Label != "opportunistic" {
		b.pipeline.observeAdmission("policy")
		return SlotDecision{
			Allowed: false,
			Label:   "none",
			Reason:  "opportunistic slot denied by tenant residency gate",
		}, nil, false
	}
	if b.activeSlots >= reserved {
		if freeMB, ok := b.builderSliceFreeMB(); !ok || freeMB < secondSlotNeedMB {
			b.pipeline.observeAdmission("memory")
			return SlotDecision{
				Allowed: false,
				Label:   "none",
				Reason:  fmt.Sprintf("opportunistic slot needs %d MiB free in the builder slice", secondSlotNeedMB),
			}, nil, false
		}
	}
	if b.activeSlots < reserved {
		decision.Label = "guaranteed"
		decision.Reason = "guaranteed slot available"
	}
	b.activeSlots++
	// All of this allocator's slots run in faas-cp-build.slice; counting
	// them as reserved keeps elastic slots right if builder_elastic_slots
	// turns on while they run (ADR-20261118154632).
	b.activeReserved++
	b.pipeline.observeAdmission("granted")
	return decision, b.releaseOnce(func() {
		if b.activeReserved > 0 {
			b.activeReserved--
		}
	}), true
}

// acquireIdleSlot takes the builder slot only when no build holds it, for
// platform work that must never take a slot from a build: the BuildKit seed
// run (ADR-20261030122204). It records no admission metric, since no build
// asked for it.
func (b *Builderd) acquireIdleSlot() (func(), bool) {
	b.slotMu.Lock()
	defer b.slotMu.Unlock()
	if b.activeSlots != 0 {
		return nil, false
	}
	b.activeSlots++
	// The seed run holds the reserved slot (faas-cp-build.slice).
	b.activeReserved++
	return b.releaseOnce(func() {
		if b.activeReserved > 0 {
			b.activeReserved--
		}
	}), true
}

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
