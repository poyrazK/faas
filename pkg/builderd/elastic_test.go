package builderd

// adr: 20261118154632 — elastic builder slots: per-node reserved and
// borrowed slots, and borrowed builds that give their memory back.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/builderslots"
	"github.com/onebox-faas/faas/pkg/state"
)

// borrowedFakeVM is fakeVM plus vmmd's borrowed builder capability.
type borrowedFakeVM struct {
	fakeVM
	supported bool
	forgotten int
	mu        sync.Mutex
	cancelled []string
}

func (v *borrowedFakeVM) SupportsBorrowedBuilders(context.Context) bool { return v.supported }
func (v *borrowedFakeVM) ForgetBorrowedCapability()                     { v.forgotten++ }
func (v *borrowedFakeVM) Cancel(_ context.Context, buildID string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.cancelled = append(v.cancelled, buildID)
	return nil
}

var (
	quietNode    = builderslots.Memory{TenantMB: 2_000, TenantMaxMB: 10_000, AvailableMB: 9_000}
	pressureNode = builderslots.Memory{TenantMB: 6_000, BorrowedMB: 3_000, TenantMaxMB: 10_000, AvailableMB: 3_000}
)

// memoryProbe is a settable node memory reading.
type memoryProbe struct {
	mu  sync.Mutex
	m   builderslots.Memory
	err error
}

func (p *memoryProbe) set(m builderslots.Memory) { p.mu.Lock(); p.m = m; p.mu.Unlock() }
func (p *memoryProbe) read() (builderslots.Memory, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.m, p.err
}

// queuedBuild seeds a queued build and returns it, so the preemption count
// builderd reads exists.
func queuedBuild(t *testing.T, store *state.MemStore) state.Build {
	t.Helper()
	seededDeploymentsMu.Lock()
	defer seededDeploymentsMu.Unlock()
	depID, ok := seededDeployments[store]
	if !ok {
		var buildID string
		buildID, depID, _ = seedDeployment(t, store, filepath.Join(t.TempDir(), "src.tar.gz"))
		seededDeployments[store] = depID
		return state.Build{ID: buildID}
	}
	build, err := store.CreateBuild(context.Background(), depID, state.DeploymentKindTarball, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	return build
}

var (
	seededDeploymentsMu sync.Mutex
	seededDeployments   = map[*state.MemStore]string{}
)

func newElasticBuilderd(t *testing.T, store state.Store, vm VM, slots builderslots.Slots) (*Builderd, *ElasticSlots, *memoryProbe) {
	t.Helper()
	probe := &memoryProbe{m: quietNode}
	elastic := NewElasticSlots("node-a", 16, probe.read, slog.New(slog.NewTextHandler(io.Discard, nil)))
	elastic.SetConfig(builderslots.Config{Default: slots})
	elastic.SetEnabled(true)
	b := New(store, &fakeNotifier{}, vm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithElasticSlots(elastic)
	return b, elastic, probe
}

func TestElasticSlotsReservedThenBorrowed(t *testing.T) {
	store := state.NewMemStore()
	vm := &borrowedFakeVM{supported: true}
	b, _, probe := newElasticBuilderd(t, store, vm, builderslots.Slots{Reserved: 1, Max: 3})
	ctx := context.Background()

	first, releaseFirst, ok := b.acquireElasticSlot(ctx, queuedBuild(t, store))
	if !ok || first.Label != "guaranteed" {
		t.Fatalf("first slot = %+v, %t; want the reserved slot", first, ok)
	}
	second, releaseSecond, ok := b.acquireElasticSlot(ctx, queuedBuild(t, store))
	if !ok || second.Label != slotLabelBorrowed {
		t.Fatalf("second slot = %+v, %t; want a borrowed slot", second, ok)
	}
	// Tenants need the memory: no third.
	probe.set(builderslots.Memory{TenantMB: 5_000, BorrowedMB: 3_000, TenantMaxMB: 10_000, AvailableMB: 5_000})
	if third, _, ok := b.acquireElasticSlot(ctx, queuedBuild(t, store)); ok {
		t.Fatalf("third slot granted under tenant pressure: %+v", third)
	}
	probe.set(quietNode)
	third, releaseThird, ok := b.acquireElasticSlot(ctx, queuedBuild(t, store))
	if !ok || third.Label != slotLabelBorrowed {
		t.Fatalf("third slot = %+v, %t; want a borrowed slot", third, ok)
	}
	if fourth, _, ok := b.acquireElasticSlot(ctx, queuedBuild(t, store)); ok {
		t.Fatalf("fourth slot granted above max 3: %+v", fourth)
	}
	if b.activeSlots != 3 || b.activeReserved != 1 || b.activeBorrowed != 2 || len(b.borrowedBuilds) != 2 {
		t.Fatalf("slots %d reserved %d borrowed %d tracked %d", b.activeSlots, b.activeReserved, b.activeBorrowed, len(b.borrowedBuilds))
	}
	releaseFirst()
	releaseSecond()
	releaseSecond() // idempotent
	releaseThird()
	if b.activeSlots != 0 || b.activeReserved != 0 || b.activeBorrowed != 0 || len(b.borrowedBuilds) != 0 {
		t.Fatalf("after release: slots %d reserved %d borrowed %d tracked %d", b.activeSlots, b.activeReserved, b.activeBorrowed, len(b.borrowedBuilds))
	}
}

func TestElasticSlotsBorrowedRefusals(t *testing.T) {
	tests := []struct {
		name     string
		prepare  func(t *testing.T, store *state.MemStore, vm *borrowedFakeVM, e *ElasticSlots, p *memoryProbe) state.Build
		wantPart string
	}{
		{name: "vmmd runs no borrowed builders", prepare: func(t *testing.T, store *state.MemStore, vm *borrowedFakeVM, _ *ElasticSlots, _ *memoryProbe) state.Build {
			vm.supported = false
			return queuedBuild(t, store)
		}, wantPart: "vmmd runs no borrowed builders"},
		{name: "cooling down", prepare: func(t *testing.T, store *state.MemStore, _ *borrowedFakeVM, e *ElasticSlots, _ *memoryProbe) state.Build {
			e.noteGiveBack()
			return queuedBuild(t, store)
		}, wantPart: "within the last minute"},
		{name: "unreadable memory", prepare: func(t *testing.T, store *state.MemStore, _ *borrowedFakeVM, _ *ElasticSlots, p *memoryProbe) state.Build {
			p.err = errors.New("no cgroupfs")
			return queuedBuild(t, store)
		}, wantPart: "cannot read this node's memory"},
		{name: "given back twice", prepare: func(t *testing.T, store *state.MemStore, _ *borrowedFakeVM, _ *ElasticSlots, _ *memoryProbe) state.Build {
			ctx := context.Background()
			buildID := queuedBuild(t, store).ID
			for i := 0; i < api.BuildMaxPreemptions; i++ {
				claim, err := store.ClaimQueuedBuild(ctx, buildID)
				if err != nil {
					t.Fatal(err)
				}
				if err := store.RequeuePreemptedBuild(ctx, claim); err != nil {
					t.Fatal(err)
				}
			}
			return state.Build{ID: buildID}
		}, wantPart: "waits for a reserved slot"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			vm := &borrowedFakeVM{supported: true}
			b, e, probe := newElasticBuilderd(t, store, vm, builderslots.Slots{Reserved: 1, Max: 2})
			build := tt.prepare(t, store, vm, e, probe)
			_, release, ok := b.acquireElasticSlot(ctx, queuedBuild(t, store))
			if !ok {
				t.Fatal("reserved slot not granted")
			}
			defer release()
			got, _, ok := b.acquireElasticSlot(ctx, build)
			if ok || !containsText(got.Reason, tt.wantPart) {
				t.Fatalf("borrowed slot = %+v, %t; want a refusal mentioning %q", got, ok, tt.wantPart)
			}
		})
	}
}

func containsText(s, part string) bool {
	return len(part) == 0 || (len(s) >= len(part) && (func() bool {
		for i := 0; i+len(part) <= len(s); i++ {
			if s[i:i+len(part)] == part {
				return true
			}
		}
		return false
	})())
}

// The fixed allocator and the seed run count their slots as reserved, so
// turning elastic slots on while they run never puts a second builder in
// faas-cp-build.slice.
func TestElasticSlotsCountFixedAllocatorAndSeedAsReserved(t *testing.T) {
	store := state.NewMemStore()
	vm := &borrowedFakeVM{supported: true}
	b, e, _ := newElasticBuilderd(t, store, vm, builderslots.Slots{Reserved: 1, Max: 2})
	ctx := context.Background()

	e.SetEnabled(false)
	_, releaseFixed, ok := b.acquireBuildSlot(ctx, queuedBuild(t, store))
	if !ok || b.activeReserved != 1 {
		t.Fatalf("fixed allocator slot: ok %t reserved %d", ok, b.activeReserved)
	}
	e.SetEnabled(true)
	got, releaseNext, ok := b.acquireBuildSlot(ctx, queuedBuild(t, store))
	if !ok || got.Label != slotLabelBorrowed {
		t.Fatalf("slot after the flag turned on = %+v, %t; want borrowed, not a second reserved", got, ok)
	}
	releaseNext()
	releaseFixed()

	releaseSeed, ok := b.acquireIdleSlot()
	if !ok || b.activeReserved != 1 {
		t.Fatalf("seed run: ok %t reserved %d", ok, b.activeReserved)
	}
	if got, release, ok := b.acquireBuildSlot(ctx, queuedBuild(t, store)); !ok || got.Label != slotLabelBorrowed {
		t.Fatalf("slot during the seed run = %+v, %t; want borrowed", got, ok)
	} else {
		release()
	}
	releaseSeed()
	if b.activeSlots != 0 || b.activeReserved != 0 || b.activeBorrowed != 0 {
		t.Fatalf("after release: slots %d reserved %d borrowed %d", b.activeSlots, b.activeReserved, b.activeBorrowed)
	}
}

func TestElasticSlotLimitAndClamp(t *testing.T) {
	store := state.NewMemStore()
	b, e, _ := newElasticBuilderd(t, store, &borrowedFakeVM{}, builderslots.Slots{Reserved: 1, Max: 8})
	if got := b.slotLimit(); got != 8 {
		t.Fatalf("slot limit = %d, want 8", got)
	}
	e.SetEnabled(false)
	if got := b.slotLimit(); got != maxBuilderSlots {
		t.Fatalf("slot limit with the flag off = %d, want %d", got, maxBuilderSlots)
	}
	small := NewElasticSlots("node-b", 4, (&memoryProbe{}).read, nil)
	small.SetConfig(builderslots.Config{Default: builderslots.Slots{Reserved: 1, Max: 8}})
	if got := small.Applied(); got.Max != 2 {
		t.Fatalf("4-CPU node applies max %d, want 2", got.Max)
	}
	perNode := NewElasticSlots("big", 32, (&memoryProbe{}).read, nil)
	perNode.SetConfig(builderslots.Config{Default: builderslots.Slots{Reserved: 1, Max: 2}, Nodes: map[string]builderslots.Slots{"big": {Reserved: 1, Max: 8}}})
	if got := perNode.Applied(); got.Max != 8 {
		t.Fatalf("named node applies max %d, want 8", got.Max)
	}
}

func TestGiveBackCancelsNewestBorrowedBuild(t *testing.T) {
	store := state.NewMemStore()
	vm := &borrowedFakeVM{supported: true}
	b, e, probe := newElasticBuilderd(t, store, vm, builderslots.Slots{Reserved: 1, Max: 4})
	ctx := context.Background()
	clock := time.Unix(1_800_000_000, 0)
	e.now = func() time.Time { return clock }

	oldBuild, newBuild := queuedBuild(t, store), queuedBuild(t, store)
	_, releaseReserved, ok := b.acquireElasticSlot(ctx, queuedBuild(t, store))
	if !ok {
		t.Fatal("reserved slot not granted")
	}
	defer releaseReserved()
	_, releaseOld, ok := b.acquireElasticSlot(ctx, oldBuild)
	if !ok {
		t.Fatal("first borrowed slot not granted")
	}
	defer releaseOld()
	clock = clock.Add(time.Minute)
	_, releaseNew, ok := b.acquireElasticSlot(ctx, newBuild)
	if !ok {
		t.Fatal("second borrowed slot not granted")
	}

	b.giveBackOnce(ctx)
	if len(vm.cancelled) != 0 {
		t.Fatalf("cancelled %v on a quiet node", vm.cancelled)
	}
	probe.set(pressureNode)
	b.giveBackOnce(ctx)
	if len(vm.cancelled) != 1 || vm.cancelled[0] != newBuild.ID {
		t.Fatalf("cancelled %v, want the newest borrowed build", vm.cancelled)
	}
	// One give-back at a time: wait for its memory before the next.
	b.giveBackOnce(ctx)
	if len(vm.cancelled) != 1 {
		t.Fatalf("cancelled %v while a give-back was still in flight", vm.cancelled)
	}
	if actor, ok := b.preemptionActor(newBuild.ID, BuildOutcome{}); !ok || actor != "builderd" {
		t.Fatalf("preemption actor = %q, %t; want builderd", actor, ok)
	}
	if !e.coolingDown() {
		t.Fatal("a give-back must start the cooldown")
	}
	releaseNew()
	b.giveBackOnce(ctx)
	if len(vm.cancelled) != 2 || vm.cancelled[1] != oldBuild.ID {
		t.Fatalf("cancelled %v, want old next", vm.cancelled)
	}
	// vmmd's forced give-back reports through the outcome.
	if actor, ok := b.preemptionActor("elsewhere", BuildOutcome{PreemptedForTenants: true}); !ok || actor != "vmmd" {
		t.Fatalf("vmmd preemption actor = %q, %t", actor, ok)
	}
	if _, ok := b.preemptionActor("elsewhere", BuildOutcome{}); ok {
		t.Fatal("a build nobody gave back reports a preemption")
	}
}

func TestElasticWorkerActive(t *testing.T) {
	store := state.NewMemStore()
	vm := &borrowedFakeVM{supported: true}
	b, e, probe := newElasticBuilderd(t, store, vm, builderslots.Slots{Reserved: 1, Max: 3})
	ctx := context.Background()
	if !b.ElasticWorkerActive(ctx, 1) || !b.ElasticWorkerActive(ctx, 2) || b.ElasticWorkerActive(ctx, 3) {
		t.Fatal("workers 1 and 2 claim for a 3-slot node; worker 3 never does")
	}
	_, release, _ := b.acquireElasticSlot(ctx, queuedBuild(t, store))
	defer release()
	probe.set(pressureNode)
	if b.ElasticWorkerActive(ctx, 1) {
		t.Fatal("an extra worker claims while tenants need the memory")
	}
	probe.set(quietNode)
	if !b.ElasticWorkerActive(ctx, 1) {
		t.Fatal("an extra worker stays idle with a borrowed slot free")
	}
	e.SetEnabled(false)
	if b.ElasticWorkerActive(ctx, 1) {
		t.Fatal("an extra worker claims with elastic slots off")
	}
	if _, err := b.ProcessNextElastic(ctx, 1); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("idle extra worker ProcessNext err = %v, want ErrNotFound without a claim", err)
	}
}

// A borrowed build vmmd gave back returns to the queue at its place, counted,
// and the deployment stays in flight.
func TestProcessOneRequeuesPreemptedBorrowedBuild(t *testing.T) {
	for _, tt := range []struct {
		name  string
		vmOut BuildOutcome
		err   error
	}{
		{name: "vmmd forced", vmOut: BuildOutcome{ExitCode: -9, PreemptedForTenants: true}},
		{name: "vmmd forced, wait error", vmOut: BuildOutcome{PreemptedForTenants: true}, err: errors.New("destroy: instance gone")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			src := filepath.Join(t.TempDir(), "src.tar.gz")
			makeTarballWithName(t, src, []string{"package.json"})
			buildID, depID, _ := seedDeployment(t, store, src)
			vm := &borrowedFakeVM{supported: true}
			vm.out, vm.waitErr = tt.vmOut, tt.err
			b, _, _ := newElasticBuilderd(t, store, vm, builderslots.Slots{Reserved: 1, Max: 2})
			release, ok := b.acquireIdleSlot() // the reserved slot is busy
			if !ok {
				t.Fatal("reserved slot not taken")
			}
			defer release()

			if _, err := b.ProcessOne(ctx, buildID); !errors.Is(err, ErrPreempted) {
				t.Fatalf("ProcessOne err = %v, want ErrPreempted", err)
			}
			if !vm.lastRequest.Borrowed {
				t.Fatal("the build did not ask vmmd for a borrowed builder")
			}
			build, _ := store.BuildByID(ctx, buildID)
			dep, _ := store.DeploymentByID(ctx, depID)
			n, _ := store.BuildPreemptions(ctx, buildID)
			if build.Status != state.BuildQueued || dep.Status == state.DeployFailed || n != 1 {
				t.Fatalf("build %s deployment %s preemptions %d; want queued, in flight, 1", build.Status, dep.Status, n)
			}
			if b.activeBorrowed != 0 || len(b.borrowedBuilds) != 0 {
				t.Fatalf("borrowed slot not released: %d, %v", b.activeBorrowed, b.borrowedBuilds)
			}
		})
	}
}

// vmmd stopped running borrowed builders after builderd last asked: the
// build waits in the queue instead of failing.
func TestProcessOneRequeuesWhenVmmdRefusesBorrowed(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, src, []string{"package.json"})
	buildID, depID, _ := seedDeployment(t, store, src)
	vm := &borrowedFakeVM{supported: true}
	vm.spawnErr = fmt.Errorf("builderd: create cold boot: %w", status.Error(codes.FailedPrecondition, "this vmmd runs no borrowed builders"))
	b, _, _ := newElasticBuilderd(t, store, vm, builderslots.Slots{Reserved: 1, Max: 2})
	release, _ := b.acquireIdleSlot()
	defer release()

	if _, err := b.ProcessOne(ctx, buildID); !errors.Is(err, ErrNoSlot) {
		t.Fatalf("ProcessOne err = %v, want ErrNoSlot", err)
	}
	build, _ := store.BuildByID(ctx, buildID)
	dep, _ := store.DeploymentByID(ctx, depID)
	if build.Status != state.BuildQueued || dep.Status == state.DeployFailed || vm.forgotten != 1 {
		t.Fatalf("build %s deployment %s forgotten %d; want queued, in flight, capability dropped", build.Status, dep.Status, vm.forgotten)
	}
}
