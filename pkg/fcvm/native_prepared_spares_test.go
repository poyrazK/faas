//go:build linux || darwin

// adr: 149 — amendment 2026-11-07: prepared networks under native recovery.
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
)

func (v *recoveryVMMFixture) nativePreparedSparesEnabled() bool {
	return v.jailer.nativePreparedSparesEnabled()
}
func (v *recoveryVMMFixture) prepareNativeSpare(ctx context.Context, l Lease) (nativeLaunchRecord, error) {
	return v.jailer.prepareNativeSpare(ctx, l)
}
func (v *recoveryVMMFixture) claimNativeSpare(ctx context.Context, r nativeLaunchRecord, target string) error {
	return v.jailer.claimNativeSpare(ctx, r, target)
}
func (v *recoveryVMMFixture) moveNativeSpare(ctx context.Context, target string, move func() error) error {
	return v.jailer.moveNativeSpare(ctx, target, move)
}
func (v *recoveryVMMFixture) finishNativeClaim(ctx context.Context, link nativePreparedLink) error {
	return v.jailer.finishNativeClaim(ctx, link)
}
func (v *recoveryVMMFixture) archiveNativeSpare(ctx context.Context, l Lease) error {
	return v.jailer.archiveNativeSpare(ctx, l)
}

type nativeSpareMoves struct {
	mu    sync.Mutex
	moves [][2]string
}

func (s *nativeSpareMoves) move(oldName, newName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.moves = append(s.moves, [2]string{oldName, newName})
	return nil
}

func (s *nativeSpareMoves) all() [][2]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][2]string(nil), s.moves...)
}

// nativeSparePool is a native-recovery Manager with native_prepared_networks
// on and a hand-built pool, as EnablePreparedNetworks wires it on Linux.
func nativeSparePool(t *testing.T) (*Manager, *JailerVMM, *preparedNetworkPool, *nativeSpareMoves) {
	t.Helper()
	m, v, _ := nativeManagerFixture(t)
	v.WithNativeLaunchOwners().WithNativePreparedNetworks()
	v.nativeRecovery.journal.ownerID = func() (nativeProducerIdentity, error) {
		return nativeProducerIdentity{Protocol: nativeLaunchOwnerProtocol, PID: 4242, UID: 0, StartTime: 99}, nil
	}
	if err := m.RecoverNativeProcesses(t.Context()); err != nil {
		t.Fatal(err)
	}
	moves := &nativeSpareMoves{}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	p := &preparedNetworkPool{m: m, capacity: 1, ctx: ctx, cancel: cancel,
		notify: make(chan struct{}, 1), move: moves.move, removed: func(netns.Config) bool { return true }}
	m.preparedNetworks = p
	return m, v, p, moves
}

func nativeSpareRecords(t *testing.T, v *JailerVMM) (spares, archived []string) {
	t.Helper()
	root := v.nativeRecovery.journal.root
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if id, ok := strings.CutSuffix(e.Name(), ".json"); ok && nativePreparedSpareName(id) {
			spares = append(spares, id)
		}
	}
	retired, err := os.ReadDir(filepath.Join(root, "retired"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	for _, e := range retired {
		archived = append(archived, e.Name())
	}
	return spares, archived
}

// Under native recovery a spare is built under its own record, and a wake
// that hits it moves the namespace only after the instance's linked record
// is durable, then archives the spare.
func TestNativePreparedNetworkWakeClaimsJournaledSpare(t *testing.T) {
	m, v, p, moves := nativeSparePool(t)
	fillTestPreparedPool(t, m, p, 250)
	spares, _ := nativeSpareRecords(t, v)
	if len(p.ready) != 1 || len(spares) != 1 || p.ready[0].native.Lease.Instance != spares[0] {
		t.Fatalf("filled pool: ready=%d spare records=%v", len(p.ready), spares)
	}
	spare := p.ready[0].native
	if !p.ready[0].config.PublicForwarding {
		t.Fatal("a native spare was built without public forwarding, so no wake could match it")
	}
	run := m.run.(*fakeRunner)
	setupBefore := run.setupCount

	const instance = "native-spare-hit"
	inst, err := m.Wake(t.Context(), WakeRequest{Instance: instance, BaseKey: "/base.ext4", LayerKey: "/layer.ext4",
		VcpuCount: 2, MemSizeMiB: 128, Plan: "scale", EgressMbit: 250, Snapshot: usableSnapshot()})
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if inst.Method != WakeRestore || run.setupCount != setupBefore {
		t.Fatalf("wake rebuilt the claimed network: method=%v setup %d->%d", inst.Method, setupBefore, run.setupCount)
	}
	if got := moves.all(); len(got) != 1 || got[0] != [2]string{spare.Lease.Netns, "fc-" + instance} {
		t.Fatalf("namespace moves = %v", got)
	}
	spares, archived := nativeSpareRecords(t, v)
	if len(spares) != 0 || len(archived) != 1 || archived[0] != spare.Generation+".json" {
		t.Fatalf("after claim: spare records=%v archived=%v", spares, archived)
	}
	owner, err := v.nativeRecovery.journal.read(instance)
	if err != nil || owner.Version != 3 || owner.Prepared != (nativePreparedLink{}) || owner.Lease.Slot != spare.Lease.Slot {
		t.Fatalf("instance record = %+v, %v", owner, err)
	}
}

// Startup retires every spare a previous daemon left, unclaimed or claimed
// by an instance that never published its record, and frees their slots.
func TestNativePreparedNetworkStartupRetiresRecoveredSpares(t *testing.T) {
	ctx := t.Context()
	m, v, _ := nativeManagerFixture(t)
	v.WithNativeLaunchOwners().WithNativePreparedNetworks()
	j := v.nativeRecovery.journal
	j.ownerID = func() (nativeProducerIdentity, error) {
		return nativeProducerIdentity{Protocol: nativeLaunchOwnerProtocol, PID: 4242, UID: 0, StartTime: 99}, nil
	}
	ready := nativeSpareLease(21)
	if _, err := j.prepareSpare(ctx, ready); err != nil {
		t.Fatal(err)
	}
	abandoned := nativeSpareLease(22)
	spare, err := j.prepareSpare(ctx, abandoned)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.claimSpare(ctx, spare, "never-published"); err != nil {
		t.Fatal(err)
	}
	// The daemon that published both spares has exited.
	v.nativeRecovery.producerExited = func(context.Context, nativeProducerIdentity) error { return nil }
	if err := m.RecoverNativeProcesses(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.EnablePreparedNetworks(ctx, 0); err != nil {
		t.Fatal(err)
	}
	spares, archived := nativeSpareRecords(t, v)
	if len(spares) != 0 || len(archived) != 2 {
		t.Fatalf("after startup: spare records=%v archived=%v", spares, archived)
	}
	m.mu.Lock()
	held := len(m.nativeRecovered)
	m.mu.Unlock()
	if held != 0 {
		t.Fatalf("recovered spares still hold %d slots", held)
	}
	run := m.run.(*fakeRunner)
	for _, l := range []Lease{ready, abandoned} {
		if !run.ran("ip netns del " + l.Netns) {
			t.Fatalf("startup never removed %s", l.Netns)
		}
	}
}

// A claim whose instance record cannot be published leaves the spare owning
// its objects: the spare is retired and archived, and nothing moved.
func TestNativePreparedNetworkAbandonedClaimRetiresSpare(t *testing.T) {
	m, v, p, moves := nativeSparePool(t)
	const instance = "native-spare-blocked"
	// Another daemon's live record for the instance cannot be replaced.
	blocking := nativeTargetLease(instance, 40)
	if err := v.nativeRecovery.journal.prepare(t.Context(), blocking); err != nil {
		t.Fatal(err)
	}
	fillTestPreparedPool(t, m, p, 250)
	if _, err := m.Wake(t.Context(), WakeRequest{Instance: instance, BaseKey: "/base.ext4", LayerKey: "/layer.ext4",
		VcpuCount: 2, MemSizeMiB: 128, Plan: "scale", EgressMbit: 250, Snapshot: usableSnapshot()}); err == nil {
		t.Fatal("wake over an unreplaceable record succeeded")
	}
	if got := moves.all(); len(got) != 0 {
		t.Fatalf("an abandoned claim moved a namespace: %v", got)
	}
	spares, archived := nativeSpareRecords(t, v)
	if len(spares) != 0 || len(archived) != 1 {
		t.Fatalf("abandoned spare: records=%v archived=%v", spares, archived)
	}
}

// A claim stopped before its move: the instance's cleanup removes the
// spare's namespace name too, then finishes the claim.
func TestNativePreparedNetworkLinkedCleanupRemovesSpareName(t *testing.T) {
	ctx := t.Context()
	m, v, p, _ := nativeSparePool(t)
	policy := fillTestPreparedPool(t, m, p, 250)
	const instance = "native-spare-interrupted"
	entry := p.claim(instance, policy)
	if entry == nil || entry.nativeClaim == nil {
		t.Fatal("native claim did not commit")
	}
	lease := entry.lease
	lease.Plan = api.PlanScale
	if err := m.prepareNativeLease(withNativePreparedClaim(ctx, *entry.nativeClaim), lease); err != nil {
		t.Fatal(err)
	}
	owner, err := v.nativeRecovery.journal.read(instance)
	if err != nil || owner.Prepared.Source != entry.native.Lease.Instance {
		t.Fatalf("linked target = %+v, %v", owner, err)
	}
	if err := m.cleanup(ctx, lease, nativeLeaseNetwork(lease), nil); err != nil {
		t.Fatal(err)
	}
	run := m.run.(*fakeRunner)
	if !run.ran("ip netns del " + entry.native.Lease.Netns) {
		t.Fatal("cleanup left the claimed spare's namespace name")
	}
	spares, archived := nativeSpareRecords(t, v)
	if len(spares) != 0 || len(archived) != 1 {
		t.Fatalf("after linked cleanup: spare records=%v archived=%v", spares, archived)
	}
	finished, err := v.nativeRecovery.journal.read(instance)
	if err != nil || !finished.ResourcesRemoved || finished.Prepared != (nativePreparedLink{}) {
		t.Fatalf("cleaned target = %+v, %v", finished, err)
	}
}

// A claimed spare keeps no effect authority, so a stale helper cannot act
// for it once its claim is durable.
func TestNativeClaimedSpareHasNoEffectAuthority(t *testing.T) {
	spare := nativeOwnerRecordFixture()
	spare.Lease = nativeSpareLease(3)
	spare.Version, spare.Prepared = nativePreparedSpareVersion, nativePreparedLink{Source: spare.Lease.Instance}
	if err := validateNativeHostHelperAuthority(spare, nativeHostHelperEffect, []string{"ip", "link", "show"}, false); err != nil {
		t.Fatalf("unclaimed spare lost effect authority: %v", err)
	}
	spare.Prepared.Target = "claimer"
	if err := validateNativeHostHelperAuthority(spare, nativeHostHelperEffect, []string{"ip", "link", "show"}, false); err == nil {
		t.Fatal("a claimed spare kept effect authority")
	}
}
