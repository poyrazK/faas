package builderd

import (
	"sync/atomic"

	"github.com/onebox-faas/faas/pkg/api"
)

// reserved_slots.go — a node's provisioned reserved builder slots
// (ADR-20261118154632 amendment 1). node_join sizes faas-cp-build.slice,
// faas-cp.slice and the tenant fence for FAAS_BUILDER_RESERVED_SLOTS
// guaranteed builders, and builderd runs exactly that many guaranteed slots:
// the fixed allocator's first N slots, or elastic slots' Reserved.

// WithReservedSlots sets the reserved builder slots this node is provisioned
// with. Zero or a negative count is one; a count above
// api.BuilderReservedSlotsMax is clamped.
func (b *Builderd) WithReservedSlots(n int) *Builderd {
	b.provisionedReserved = api.ClampBuilderReservedSlots(n)
	if b.elastic != nil {
		b.elastic.SetProvisioned(b.provisionedReserved)
	}
	return b
}

// reservedSlots is the node's provisioned reserved builder slots.
func (b *Builderd) reservedSlots() int {
	if b.provisionedReserved > 0 {
		return b.provisionedReserved
	}
	return api.BuilderSlotsDefaultReserved
}

// joinBuildSlice records that buildID holds a slot in faas-cp-build.slice.
// When another builder already runs there (activeReserved counts every
// builder in the slice, the BuildKit seed run included), every build in the
// slice, the new one too, is marked shared: the slice's oom_kill counter can
// no longer say whose VM it killed.
func (b *Builderd) joinBuildSlice(buildID string) {
	b.slotMu.Lock()
	defer b.slotMu.Unlock()
	if b.sliceBuilds == nil {
		b.sliceBuilds = map[string]*atomic.Bool{}
	}
	shared := &atomic.Bool{}
	if b.activeReserved > 1 {
		shared.Store(true)
		for _, other := range b.sliceBuilds {
			other.Store(true)
		}
	}
	b.sliceBuilds[buildID] = shared
}

// leaveBuildSlice forgets buildID's faas-cp-build.slice slot.
func (b *Builderd) leaveBuildSlice(buildID string) {
	b.slotMu.Lock()
	delete(b.sliceBuilds, buildID)
	b.slotMu.Unlock()
}

// sliceSharedProbe is VMRequest.SliceShared for buildID: whether it has
// shared faas-cp-build.slice since its slot was granted. It is nil for a
// build without a slot in the slice (a borrowed one).
func (b *Builderd) sliceSharedProbe(buildID string) func() bool {
	b.slotMu.Lock()
	shared := b.sliceBuilds[buildID]
	b.slotMu.Unlock()
	if shared == nil {
		return nil
	}
	return shared.Load
}
