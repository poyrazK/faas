package builderd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/builderslots"
	"github.com/onebox-faas/faas/pkg/state"
)

// elastic.go — builderd's half of elastic builder slots (ADR-20261118154632).
//
// With builder_elastic_slots on, a node runs builder_slots' Reserved builds
// (faas-cp-build.slice, funded out of tenant sizing, never preempted) plus
// Max-Reserved borrowed ones (faas-borrowed.slice). A borrowed build starts
// only while tenants, borrowed builders and the new build stay within
// BuilderBorrowAdmitPct of the tenant fence, and gives its memory back: above
// BuilderBorrowGiveBackPct builderd cancels the newest borrowed build and
// returns it to the queue at its place. vmmd destroys one itself above
// BuilderBorrowForcePct. With the flag off, acquireSlot's fixed two-slot
// allocator runs exactly as before.

// slotLabelBorrowed labels a borrowed builder slot (SlotDecision.Label).
const slotLabelBorrowed = "borrowed"

// ErrPreempted is returned when a borrowed slot gave its build back to the
// queue because tenants needed the memory. Like ErrNoSlot, the row is back in
// the queue at its original position.
var ErrPreempted = errors.New("builderd: build gave its borrowed slot back to tenants")

// ElasticSlots holds a node's applied elastic slot settings.
type ElasticSlots struct {
	node    string
	cpus    int
	read    func() (builderslots.Memory, error)
	now     func() time.Time
	log     *slog.Logger
	enabled atomic.Bool
	cfg     atomic.Pointer[builderslots.Config]
	// provisioned is the node's reserved slots (FAAS_BUILDER_RESERVED_SLOTS);
	// zero means one.
	provisioned atomic.Int32

	mu           sync.Mutex
	lastGiveBack time.Time
	lastApplied  builderslots.Slots
}

// NewElasticSlots resolves node's entry in builder_slots, clamps it to cpus,
// and reads the node's memory through read. It starts with the flag off and
// the compiled builder_slots.
func NewElasticSlots(node string, cpus int, read func() (builderslots.Memory, error), log *slog.Logger) *ElasticSlots {
	if log == nil {
		log = slog.Default()
	}
	e := &ElasticSlots{node: node, cpus: cpus, read: read, now: time.Now, log: log}
	cfg := builderslots.DefaultConfig()
	e.cfg.Store(&cfg)
	return e
}

// SetEnabled applies builder_elastic_slots.
func (e *ElasticSlots) SetEnabled(on bool) { e.enabled.Store(on) }

// SetProvisioned sets the reserved slots this node is provisioned with
// (ADR-20261118154632 amendment 1). builder_slots cannot change that count;
// Applied always reports it as Reserved.
func (e *ElasticSlots) SetProvisioned(reserved int) {
	e.provisioned.Store(int32(api.ClampBuilderReservedSlots(reserved))) //nolint:gosec // bounded by api.BuilderReservedSlotsMax
}

func (e *ElasticSlots) provisionedReserved() int {
	if n := int(e.provisioned.Load()); n > 0 {
		return n
	}
	return api.BuilderSlotsDefaultReserved
}

// resolve is cfg's entry for this node with the node's provisioned reserved
// slots, clamped to its CPUs. It reports whether provisioning or the CPU
// clamp changed what builder_slots asked for.
func (e *ElasticSlots) resolve(cfg *builderslots.Config) (requested, applied builderslots.Slots, provisioned, clamped bool) {
	requested = cfg.For(e.node)
	withProvision, provisioned := builderslots.Provisioned(requested, e.provisionedReserved())
	applied, clamped = builderslots.Clamp(withProvision, e.cpus)
	return requested, applied, provisioned, clamped
}

// SetConfig applies builder_slots and logs what this node now runs.
func (e *ElasticSlots) SetConfig(cfg builderslots.Config) {
	e.cfg.Store(&cfg)
	requested, applied, provisioned, clamped := e.resolve(&cfg)
	e.mu.Lock()
	changed := applied != e.lastApplied
	e.lastApplied = applied
	e.mu.Unlock()
	if !changed {
		return
	}
	if provisioned {
		e.log.Warn("builderd: builder_slots' reserved differs from the slots this node is provisioned with; the node runs its provisioned count",
			"node", e.node, "requested_reserved", requested.Reserved, "provisioned_reserved", applied.Reserved)
	}
	if clamped {
		e.log.Warn("builderd: builder_slots asks for more builds than this node's CPUs run; clamped",
			"node", e.node, "requested_max", requested.Max, "applied_max", applied.Max, "online_cpus", e.cpus)
	}
	e.log.Info("builderd: builder slots applied", "node", e.node, "reserved", applied.Reserved, "max", applied.Max)
}

// Enabled reports builder_elastic_slots.
func (e *ElasticSlots) Enabled() bool { return e != nil && e.enabled.Load() }

// Applied is this node's slots: its builder_slots entry with the node's
// provisioned reserved slots, clamped to its CPUs.
func (e *ElasticSlots) Applied() builderslots.Slots {
	_, slots, _, _ := e.resolve(e.cfg.Load())
	return slots
}

// coolingDown reports whether a give-back happened within
// BuilderBorrowCooldown.
func (e *ElasticSlots) coolingDown() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return !e.lastGiveBack.IsZero() && e.now().Sub(e.lastGiveBack) < api.BuilderBorrowCooldown
}

func (e *ElasticSlots) noteGiveBack() {
	e.mu.Lock()
	e.lastGiveBack = e.now()
	e.mu.Unlock()
}

// borrowedVM is the VM driver surface borrowed slots need.
type borrowedVM interface {
	SupportsBorrowedBuilders(context.Context) bool
	ForgetBorrowedCapability()
}

// borrowedBuild is one build running on a borrowed slot.
type borrowedBuild struct {
	started    time.Time
	preempting bool
}

// WithElasticSlots wires elastic builder slots. They run the node's
// provisioned reserved slots (WithReservedSlots).
func (b *Builderd) WithElasticSlots(e *ElasticSlots) *Builderd {
	b.elastic = e
	if e != nil {
		e.SetProvisioned(b.reservedSlots())
	}
	return b
}

// slotLimit is the enforced slot limit builderd_build_slots_limit reports.
func (b *Builderd) slotLimit() int {
	if b.elastic.Enabled() {
		return b.elastic.Applied().Max
	}
	return b.fixedSlotLimit()
}

// acquireBuildSlot takes a builder slot for build: elastic slots while
// builder_elastic_slots is on, else the fixed allocator. A slot in
// faas-cp-build.slice also records whether build shares that slice with
// another builder (sliceSharedProbe).
func (b *Builderd) acquireBuildSlot(ctx context.Context, build state.Build) (SlotDecision, func(), bool) {
	var (
		slot    SlotDecision
		release func()
		ok      bool
	)
	if !b.elastic.Enabled() {
		slot, release, ok = b.acquireSlot()
	} else {
		slot, release, ok = b.acquireElasticSlot(ctx, build)
	}
	if !ok || slot.Label == slotLabelBorrowed {
		return slot, release, ok
	}
	b.joinBuildSlice(build.ID)
	return slot, func() {
		release()
		b.leaveBuildSlice(build.ID)
	}, true
}

func (b *Builderd) acquireElasticSlot(ctx context.Context, build state.Build) (SlotDecision, func(), bool) {
	applied := b.elastic.Applied()
	b.slotMu.Lock()
	if b.activeSlots >= applied.Max {
		b.slotMu.Unlock()
		b.pipeline.observeAdmission("limit")
		return SlotDecision{Label: "none", Reason: fmt.Sprintf("all %d builder slots on this node are busy", applied.Max)}, nil, false
	}
	if b.activeReserved < applied.Reserved {
		b.activeSlots++
		b.activeReserved++
		b.slotMu.Unlock()
		b.pipeline.observeAdmission("granted")
		return SlotDecision{Allowed: true, Label: "guaranteed", Reason: "reserved slot available"}, b.releaseOnce(func() {
			b.activeReserved--
		}), true
	}
	b.slotMu.Unlock()

	// Every reserved slot is busy: a borrowed one, if tenants leave room.
	if outcome, reason := b.borrowedRefusal(ctx, build); reason != "" {
		b.pipeline.observeAdmission(outcome)
		return SlotDecision{Label: "none", Reason: reason}, nil, false
	}
	b.slotMu.Lock()
	if b.activeSlots >= applied.Max || b.activeBorrowed >= applied.Borrowed() {
		b.slotMu.Unlock()
		b.pipeline.observeAdmission("limit")
		return SlotDecision{Label: "none", Reason: "builder slots filled while checking memory"}, nil, false
	}
	b.activeSlots++
	b.activeBorrowed++
	if b.borrowedBuilds == nil {
		b.borrowedBuilds = map[string]*borrowedBuild{}
	}
	b.borrowedBuilds[build.ID] = &borrowedBuild{started: b.elastic.now()}
	b.slotMu.Unlock()
	b.pipeline.observeAdmission("granted")
	return SlotDecision{Allowed: true, Label: slotLabelBorrowed, Reason: "borrowed slot: tenants leave room"}, b.releaseOnce(func() {
		b.activeBorrowed--
		delete(b.borrowedBuilds, build.ID)
	}), true
}

// releaseOnce returns an idempotent slot release that also runs undo under
// slotMu.
func (b *Builderd) releaseOnce(undo func()) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			b.slotMu.Lock()
			if b.activeSlots > 0 {
				b.activeSlots--
			}
			undo()
			b.slotMu.Unlock()
		})
	}
}

// borrowedRefusal returns why build may not take a borrowed slot now, with
// the admission metric outcome, or an empty reason.
func (b *Builderd) borrowedRefusal(ctx context.Context, build state.Build) (outcome, reason string) {
	if b.elastic.Applied().Borrowed() == 0 {
		return "limit", "this node has no borrowed builder slots"
	}
	vm, ok := b.vm.(borrowedVM)
	if !ok || !vm.SupportsBorrowedBuilders(ctx) {
		return "policy", "this node's vmmd runs no borrowed builders"
	}
	if b.elastic.coolingDown() {
		return "memory", "a borrowed build gave its memory back to tenants within the last minute"
	}
	if store, ok := b.store.(state.BuildPreemptionStore); ok {
		n, err := store.BuildPreemptions(ctx, build.ID)
		if err != nil {
			return "policy", "cannot read how often this build was given back: " + err.Error()
		}
		if n >= api.BuildMaxPreemptions {
			return "policy", fmt.Sprintf("this build was given back %d times; it waits for a reserved slot", n)
		}
	}
	m, err := b.elastic.read()
	if err != nil {
		return "memory", "cannot read this node's memory: " + err.Error()
	}
	if !builderslots.CanAdmitBorrowed(m) {
		return "memory", fmt.Sprintf("tenants need this node's memory (tenant %d of %d MiB, borrowed %d MiB, %d MiB available)",
			m.TenantMB, m.TenantMaxMB, m.BorrowedMB, m.AvailableMB)
	}
	return "", ""
}

// ElasticWorkerActive reports whether elastic poll worker i (1-based; the
// durable worker is 0) should try to claim a build: elastic slots are on, the
// node runs more than i slots, and one of them could take a build now.
func (b *Builderd) ElasticWorkerActive(ctx context.Context, i int) bool {
	if !b.elastic.Enabled() {
		return false
	}
	applied := b.elastic.Applied()
	if i >= applied.Max {
		return false
	}
	b.slotMu.Lock()
	full := b.activeSlots >= applied.Max
	reservedFree := b.activeReserved < applied.Reserved
	borrowedFree := b.activeBorrowed < applied.Borrowed()
	b.slotMu.Unlock()
	if full {
		return false
	}
	if reservedFree {
		return true
	}
	if !borrowedFree || b.elastic.coolingDown() {
		return false
	}
	if vm, ok := b.vm.(borrowedVM); !ok || !vm.SupportsBorrowedBuilders(ctx) {
		return false
	}
	m, err := b.elastic.read()
	return err == nil && builderslots.CanAdmitBorrowed(m)
}

// ProcessNextElastic is elastic poll worker i's ProcessNext: it claims only
// while ElasticWorkerActive, so idle extra workers cost no claim queries.
func (b *Builderd) ProcessNextElastic(ctx context.Context, i int) (BuildResult, error) {
	if !b.ElasticWorkerActive(ctx, i) {
		return BuildResult{}, state.ErrNotFound
	}
	return b.ProcessNext(ctx)
}

// RunBorrowedGiveBack cancels the newest borrowed build whenever tenants
// need its memory (builderslots.ShouldGiveBack), one at a time, until ctx
// ends. It runs whatever builder_elastic_slots says, so turning the flag off
// never leaves a borrowed build without its give-back.
func (b *Builderd) RunBorrowedGiveBack(ctx context.Context) {
	t := time.NewTicker(api.BuilderBorrowCheckInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		b.giveBackOnce(ctx)
	}
}

func (b *Builderd) giveBackOnce(ctx context.Context) {
	if b.elastic == nil {
		return
	}
	b.slotMu.Lock()
	victim := ""
	var newest time.Time
	for id, running := range b.borrowedBuilds {
		if running.preempting {
			// One give-back at a time: wait until its memory is back.
			b.slotMu.Unlock()
			return
		}
		if victim == "" || running.started.After(newest) || (running.started.Equal(newest) && id > victim) {
			victim, newest = id, running.started
		}
	}
	b.slotMu.Unlock()
	if victim == "" {
		return
	}
	m, err := b.elastic.read()
	if err == nil && !builderslots.ShouldGiveBack(m) {
		return
	}
	b.slotMu.Lock()
	running, ok := b.borrowedBuilds[victim]
	if !ok {
		b.slotMu.Unlock()
		return
	}
	running.preempting = true
	b.slotMu.Unlock()
	b.elastic.noteGiveBack()
	b.log.Warn("builderd: giving a borrowed build's memory back to tenants",
		"build", victim, "tenant_mb", m.TenantMB, "tenant_max_mb", m.TenantMaxMB,
		"borrowed_mb", m.BorrowedMB, "available_mb", m.AvailableMB, "read_err", err)
	cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), activeVMCancelTimeout)
	defer cancel()
	if cancelErr := b.vm.Cancel(cancelCtx, victim); cancelErr != nil {
		b.log.Error("builderd: stop borrowed build for tenants", "build", victim, "err", cancelErr)
		b.slotMu.Lock()
		if running, ok := b.borrowedBuilds[victim]; ok {
			running.preempting = false
		}
		b.slotMu.Unlock()
	}
}

// preemptionActor reports who gave build back, if anyone: builderd's
// give-back or vmmd's forced one.
func (b *Builderd) preemptionActor(buildID string, out BuildOutcome) (string, bool) {
	b.slotMu.Lock()
	running, ok := b.borrowedBuilds[buildID]
	byBuilderd := ok && running.preempting
	b.slotMu.Unlock()
	switch {
	case byBuilderd:
		return "builderd", true
	case out.PreemptedForTenants:
		return "vmmd", true
	default:
		return "", false
	}
}

// requeuePreempted returns a build its borrowed slot gave back to the queue,
// at its place, and counts the give-back.
func (b *Builderd) requeuePreempted(ctx context.Context, build state.Build, actor string) error {
	recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), activeVMCancelTimeout)
	defer cancel()
	var err error
	if store, ok := b.store.(state.BuildPreemptionStore); ok {
		err = store.RequeuePreemptedBuild(recoveryCtx, build)
	} else {
		err = b.store.RequeueBuild(recoveryCtx, build.ID)
	}
	b.pipeline.observeRequeue(recoveryCtx, requeuePreempted, err)
	b.pipeline.observePreemption(actor)
	if err != nil {
		b.log.Warn("builderd: requeue a build its borrowed slot gave back", "build", build.ID, "actor", actor, "err", err)
		return err
	}
	b.emitBuildLog(recoveryCtx, build.ID, "this build ran on a borrowed builder slot and gave its memory back to running apps; it is back in the build queue at its place and will restart\n")
	b.log.Info("builderd: requeued a build its borrowed slot gave back", "build", build.ID, "actor", actor)
	return nil
}
