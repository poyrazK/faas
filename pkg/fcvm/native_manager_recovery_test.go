//go:build linux || darwin

// adr: 435 — native ownership and uncertain retirement must remain fenced.
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
)

// Boots use the ordinary orchestration fake. Native retirement, admission and
// resource acknowledgement run the production lifecycle against fake /proc.
type recoveryVMMFixture struct {
	*fakeVMM
	jailer *JailerVMM
}

type nativeOwnershipRunner struct {
	journal  *nativeLaunchJournal
	instance string
	checked  bool
	lease    Lease
}

func (r *nativeOwnershipRunner) Run(_ context.Context, argv []string) error {
	if !r.checked {
		r.checked = true
		lease, err := r.journal.read(r.instance)
		if err != nil {
			return err
		}
		r.lease = lease.Lease
		return errors.New("network setup deliberately failed after ownership proof")
	}
	return nil
}

func TestNativeManagerPreparesFullLeaseBeforeAppAndJobNetworkEffects(t *testing.T) {
	for _, kind := range []string{"app", "job"} {
		t.Run(kind, func(t *testing.T) {
			m, v, _ := nativeManagerFixture(t)
			id := "new-" + kind
			runner := &nativeOwnershipRunner{journal: v.nativeRecovery.journal, instance: id}
			m.run = runner
			var err error
			if kind == "app" {
				_, err = m.Wake(t.Context(), WakeRequest{Instance: id, Plan: api.PlanHobby, MemSizeMiB: 256})
			} else {
				_, err = m.BootJob(t.Context(), JobBootRequest{Instance: id, Plan: api.PlanHobby, MemSizeMiB: 256})
			}
			if err == nil || !runner.checked || runner.lease.Plan != api.PlanHobby || runner.lease.MemoryMaxMiB != 256 {
				t.Fatalf("effect saw lease=%+v checked=%v err=%v", runner.lease, runner.checked, err)
			}
			if m.alloc.InUse() != 0 {
				t.Fatal("confirmed failed setup retained reusable lease")
			}
			record, err := v.nativeRecovery.journal.read(id)
			if err != nil || !record.Revoked || !record.ExitConfirmed || !record.ResourcesRemoved {
				t.Fatalf("failed setup record=%+v err=%v", record, err)
			}
		})
	}
}

func TestNativeManagerJournalWriteFailureCannotReleaseUnattestedOwnership(t *testing.T) {
	m, v, _ := nativeManagerFixture(t)
	cause := errors.New("journal write failed")
	v.nativeRecovery.journal.writeRecord = func(string, nativeLaunchRecord) error { return cause }
	if _, err := m.Wake(t.Context(), WakeRequest{Instance: "new-instance", Plan: api.PlanHobby, MemSizeMiB: 256}); !errors.Is(err, cause) {
		t.Fatalf("wake=%v", err)
	}
	if m.alloc.InUse() != 1 || m.live["new-instance"] == nil || len(m.cidToID) != 0 {
		t.Fatal("unattested cleanup released ownership or readiness")
	}
	if len(m.run.(*fakeRunner).commands) != 0 {
		t.Fatal("failed journal preparation reached host effects")
	}
}

func (v *recoveryVMMFixture) nativeRecoveryRuntime() *nativeProcessRecoveryRuntime {
	return v.jailer.nativeRecoveryRuntime()
}
func (v *recoveryVMMFixture) nativeRecoveryLeases(ctx context.Context) ([]Lease, error) {
	return v.jailer.nativeRecoveryLeases(ctx)
}
func (v *recoveryVMMFixture) prepareNativeLease(ctx context.Context, l Lease) error {
	return v.jailer.prepareNativeLease(ctx, l)
}
func (v *recoveryVMMFixture) confirmNativeCleanup(ctx context.Context, l Lease, nc netns.Config) error {
	return v.jailer.confirmNativeCleanup(ctx, l, nc)
}
func (v *recoveryVMMFixture) Kill(ctx context.Context, l Lease) error { return v.jailer.Kill(ctx, l) }
func (v *recoveryVMMFixture) DestroyWithExport(ctx context.Context, l Lease, out string) (int, error) {
	return v.jailer.DestroyWithExport(ctx, l, out)
}

func nativeManagerFixture(t *testing.T) (*Manager, *JailerVMM, string) {
	t.Helper()
	v := NewJailerVMM(t.TempDir(), time.Second).WithNativeProcessRecovery()
	r := v.nativeRecovery
	r.journal = nativeJournalFixture(filepath.Join(v.chrootBase, ".native-processes"))
	proc := t.TempDir()
	r.retirer.probe = nativeProcessProbe{root: proc, chrootBase: v.chrootBase}
	r.support = func() error { return nil }
	r.mounts = func(string) ([]string, error) { return nil, nil }
	// Portable tests do not claim Linux network/mount acceptance.
	r.resources = func(Lease, netns.Config) error { return nil }
	t.Cleanup(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.daemonLock != nil {
			_ = r.daemonLock.Close()
		}
	})
	m := NewManager(&fakeRunner{}, &recoveryVMMFixture{fakeVMM: &fakeVMM{}, jailer: v}, Paths{}, "1.7.0", nil, nil)
	return m, v, proc
}

func prepareRecoveredNative(t *testing.T, v *JailerVMM, slot int, instance string) Lease {
	t.Helper()
	l := leaseForSlot(instance, slot)
	l.Plan = api.PlanHobby
	if err := v.nativeRecovery.journal.prepare(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	return l
}

func TestNativeManagerRecoveryQuarantinesBeforeBootWithoutReadiness(t *testing.T) {
	m, v, proc := nativeManagerFixture(t)
	l := prepareRecoveredNative(t, v, 0, "old-instance")
	writeNativeRecoveryProcess(t, proc, 42, l.Instance, l.UID, 101)
	_, flight, err := m.beginInstanceBoot(t.Context(), "fresh-instance")
	if err != nil {
		t.Fatal(err)
	}
	defer m.finishInstanceBoot("fresh-instance", flight)
	if m.alloc.InUse() != 1 || len(m.live) != 0 || len(m.cidToID) != 0 {
		t.Fatal("recovery conferred live/readiness authority")
	}
	if _, err := m.alloc.Acquire(l.Instance); err == nil {
		t.Fatal("recovered ID admitted again")
	}
	fresh, err := m.alloc.Acquire("fresh-instance")
	if err != nil || fresh.Slot == l.Slot {
		t.Fatalf("fresh=%+v err=%v", fresh, err)
	}
}

func TestNativeManagerUnknownStopWaitsAndRetainsUncertainResources(t *testing.T) {
	m, v, proc := nativeManagerFixture(t)
	l := prepareRecoveredNative(t, v, 0, "old-instance")
	ticket, err := v.nativeRecovery.journal.beginLaunch(t.Context(), l)
	if err != nil {
		t.Fatal(err)
	}
	if err := ticket.authorize(42, 101); err != nil {
		t.Fatal(err)
	}
	if err := ticket.close(); err != nil {
		t.Fatal(err)
	}
	// This producer remains in the same daemon. A genuinely restarted
	// daemon has no such token and must retain unknown helper ownership.
	local, err := v.nativeRecovery.journal.read(l.Instance)
	if err != nil {
		t.Fatal(err)
	}
	v.nativeRecovery.remember(local)
	dir := writeNativeRecoveryProcess(t, proc, 42, l.Instance, l.UID, 101)
	entered, release := make(chan struct{}), make(chan struct{})
	h := &recoveryHandle{onWait: func(ctx context.Context) error {
		close(entered)
		select {
		case <-release:
			return os.RemoveAll(dir)
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	v.nativeRecovery.retirer.open = func(int) (nativeProcessHandle, error) { return h, nil }
	resourceErr := errors.New("namespace survived")
	v.nativeRecovery.resources = func(Lease, netns.Config) error { return resourceErr }
	done := make(chan error, 1)
	go func() { _, err := m.DestroyWithExport(t.Context(), l.Instance, ""); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("did not join pinned exit")
	}
	select {
	case err := <-done:
		t.Fatalf("stop acknowledged before exit: %v", err)
	default:
	}
	if m.alloc.InUse() != 1 {
		t.Fatal("slot released before exit")
	}
	close(release)
	if err := <-done; !errors.Is(err, resourceErr) {
		t.Fatalf("cleanup=%v", err)
	}
	record, err := v.nativeRecovery.journal.read(l.Instance)
	if err != nil || !record.ExitConfirmed || record.ResourcesRemoved || m.alloc.InUse() != 1 {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	v.nativeRecovery.resources = func(Lease, netns.Config) error { return nil }
	if code, err := m.DestroyWithExport(t.Context(), l.Instance, ""); err != nil || code != -1 || m.alloc.InUse() != 0 {
		t.Fatalf("retry code=%d held=%d err=%v", code, m.alloc.InUse(), err)
	}
	if code, err := m.DestroyWithExport(t.Context(), l.Instance, ""); err != nil || code != -1 {
		t.Fatalf("idempotent code=%d err=%v", code, err)
	}
	if !h.waited || !h.closed || len(h.signals) != 1 || h.signals[0] != syscall.SIGKILL {
		t.Fatalf("handle=%+v", h)
	}
}

func TestNativeManagerUnknownStopCannotAcknowledgeMissingOrDuplicateOwnership(t *testing.T) {
	for _, kind := range []string{"unknown", "duplicate", "legacy"} {
		t.Run(kind, func(t *testing.T) {
			m, v, proc := nativeManagerFixture(t)
			id := "old-instance"
			if kind != "unknown" {
				prepareRecoveredNative(t, v, 0, id)
			}
			if kind == "duplicate" {
				writeNativeRecoveryProcess(t, proc, 42, id, JailUIDBase+1, 101)
			}
			if kind == "legacy" {
				if err := os.Remove(v.nativeRecovery.journal.path(id)); err != nil {
					t.Fatal(err)
				}
				writeNativeRecoveryProcess(t, proc, 42, id, JailUIDBase, 101)
			}
			if _, err := m.DestroyWithExport(t.Context(), id, ""); err == nil {
				t.Fatal("unproven stop acknowledged")
			}
			if _, _, err := m.SignalAndKill(t.Context(), id, syscall.SIGTERM, time.Millisecond); err == nil {
				t.Fatal("unproven signal-stop acknowledged")
			}
			if kind != "unknown" && m.alloc.InUse() == 0 {
				t.Fatal("uncertain ownership was released")
			}
		})
	}
}

func TestNativeRecoveryDaemonDeathConfirmsVMExitButRetainsUnknownHelpers(t *testing.T) {
	m, v, _ := nativeManagerFixture(t)
	l := prepareRecoveredNative(t, v, 0, "restarted-instance")
	root := filepath.Join(v.chrootBase, v.fcName, l.Instance, "root")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DestroyWithExport(t.Context(), l.Instance, ""); err == nil || !strings.Contains(err.Error(), "helper ownership") {
		t.Fatalf("recovered stop=%v", err)
	}
	record, err := v.nativeRecovery.journal.read(l.Instance)
	if err != nil || !record.Revoked || !record.ExitConfirmed || record.ResourcesRemoved || m.alloc.InUse() != 1 {
		t.Fatalf("record=%+v held=%d err=%v", record, m.alloc.InUse(), err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal("unattested helper resources removed")
	}
}

func TestNativeManagerRecoveryFailureBlocksAllAdmissionAndRetries(t *testing.T) {
	m, v, _ := nativeManagerFixture(t)
	want := errors.New("pidfd unavailable")
	v.nativeRecovery.support = func() error { return want }
	if _, _, err := m.beginInstanceBoot(t.Context(), "new"); !errors.Is(err, want) {
		t.Fatalf("boot=%v", err)
	}
	if err := m.EnablePreparedNetworks(t.Context(), 1); !errors.Is(err, want) {
		t.Fatalf("cache=%v", err)
	}
	if m.alloc.InUse() != 0 || len(m.bootFlights) != 0 || m.preparedNetworks != nil {
		t.Fatal("failed recovery admitted effects")
	}
	v.nativeRecovery.support = func() error { return nil }
	if err := m.RecoverNativeProcesses(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestNativeRecoveryOnlyOneDaemonCanOwnTheAllocatorRoot(t *testing.T) {
	m, v, _ := nativeManagerFixture(t)
	if err := m.RecoverNativeProcesses(t.Context()); err != nil {
		t.Fatal(err)
	}
	other := &nativeProcessRecoveryRuntime{journal: nativeJournalFixture(v.nativeRecovery.journal.root)}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if err := other.acquireDaemonOwnership(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second allocator=%v", err)
	}
	if other.daemonLock != nil {
		t.Fatal("concurrent daemon acquired ownership")
	}
	// Simulate the kernel releasing the old descriptor after process death.
	if err := v.nativeRecovery.daemonLock.Close(); err != nil {
		t.Fatal(err)
	}
	v.nativeRecovery.daemonLock = nil
	if err := other.acquireDaemonOwnership(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer other.daemonLock.Close()
}

func TestNativeRecoveryDisabledModeCannotBypassExistingJournal(t *testing.T) {
	_, v, _ := nativeManagerFixture(t)
	prepareRecoveredNative(t, v, 0, "old-instance")
	legacy := NewJailerVMM(v.chrootBase, time.Second)
	m := NewManager(&fakeRunner{}, legacy, Paths{}, "1.7.0", nil, nil)
	if err := m.RecoverNativeProcesses(t.Context()); err == nil {
		t.Fatal("disabled mode bypassed journal ownership")
	}
	if _, _, err := m.beginInstanceBoot(t.Context(), "new-instance"); err == nil {
		t.Fatal("legacy boot bypassed journal ownership")
	}
	if err := m.EnablePreparedNetworks(t.Context(), 1); err == nil {
		t.Fatal("legacy cache bypassed journal ownership")
	}
	if m.alloc.InUse() != 0 || len(m.run.(*fakeRunner).commands) != 0 {
		t.Fatal("rejected mode reached host effects")
	}
}

func TestNativeRecoveryLegacyChrootAndReappearedResourcesBlockAdmission(t *testing.T) {
	for _, kind := range []string{"legacy_chroot", "reappeared_resource"} {
		t.Run(kind, func(t *testing.T) {
			m, v, _ := nativeManagerFixture(t)
			if kind == "legacy_chroot" {
				if err := os.MkdirAll(filepath.Join(v.chrootBase, "firecracker-v1.6.0", "legacy", "root"), 0o750); err != nil {
					t.Fatal(err)
				}
			} else {
				l := prepareRecoveredNative(t, v, 0, "old-instance")
				record, err := v.nativeRecovery.journal.retire(t.Context(), l.Instance, v.nativeRecovery.retirer)
				if err != nil {
					t.Fatal(err)
				}
				if err := v.nativeRecovery.journal.confirmResourcesRemoved(t.Context(), record); err != nil {
					t.Fatal(err)
				}
				v.nativeRecovery.resources = func(Lease, netns.Config) error { return errors.New("veth reappeared") }
			}
			if err := m.RecoverNativeProcesses(t.Context()); err == nil {
				t.Fatal("uncertain physical state granted admission")
			}
			if m.nativeRecoveryReady {
				t.Fatal("failed recovery marked ready")
			}
		})
	}
}

func TestNativeRecoveryOnlyRegisteredProducerCanUseRestoreFallback(t *testing.T) {
	_, v, _ := nativeManagerFixture(t)
	l := leaseForSlot("local-instance", 0)
	l.Plan = api.PlanHobby
	if err := v.prepareNativeLease(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	old, err := v.nativeRecovery.journal.read(l.Instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Kill(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	if err := v.ensureNativeLaunch(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	fresh, err := v.nativeRecovery.journal.read(l.Instance)
	if err != nil || fresh.Generation == old.Generation {
		t.Fatal("fallback did not replace retired incarnation")
	}
	v.nativeRecovery.owned = map[string]string{}
	if err := v.ensureNativeLaunch(t.Context(), l); err == nil {
		t.Fatal("restarted daemon borrowed producer authority")
	}
}

func TestNativeRecoveryRecoveredBuilderPreservesUnprovenArtifacts(t *testing.T) {
	m, v, _ := nativeManagerFixture(t)
	l := prepareRecoveredNative(t, v, 0, "old-builder")
	root := filepath.Join(v.chrootBase, v.fcName, l.Instance, "root")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "artifact")
	if err := os.WriteFile(marker, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DestroyWithExport(t.Context(), l.Instance, t.TempDir()); err == nil || !strings.Contains(err.Error(), "provenance") {
		t.Fatalf("export=%v", err)
	}
	if _, err := os.Stat(marker); err != nil || m.alloc.InUse() != 1 {
		t.Fatal("unproven export released artifact ownership")
	}
}
