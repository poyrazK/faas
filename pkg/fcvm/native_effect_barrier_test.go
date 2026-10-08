//go:build linux || darwin

// adr: 767
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
	"golang.org/x/sys/unix"
)

func awaitNativeEffect(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !condition() {
		select {
		case <-deadline.C:
			t.Fatal("native lifecycle condition did not become true")
		case <-tick.C:
		}
	}
}

func awaitNativeEffectResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("native lifecycle operation did not finish")
		return nil
	}
}

func TestNativeEffectBarrierNestedLifetimeAndWriterPreference(t *testing.T) {
	var barrier nativeEffectBarrier
	lock := func(context.Context, bool) (func() error, error) { return func() error { return nil }, nil }
	parent, release, err := barrier.begin(t.Context(), "root", false, lock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	observed := make(chan error, 1)
	go func() {
		_, finish, err := barrier.begin(t.Context(), "root", true, lock)
		if err == nil {
			finish()
		}
		observed <- err
	}()
	awaitNativeEffect(t, func() bool {
		barrier.mu.Lock()
		defer barrier.mu.Unlock()
		return barrier.waitingInventories == 1
	})
	blocked, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, _, err := barrier.begin(blocked, "root", false, lock); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("new producer bypassed waiting inventory: %v", err)
	}
	_, childRelease, err := barrier.begin(parent, "root", false, lock)
	if err != nil {
		t.Fatalf("active producer could not finish its nested cleanup: %v", err)
	}
	t.Cleanup(childRelease)
	release()
	select {
	case err := <-observed:
		t.Fatalf("inventory bypassed active nested producer: %v", err)
	default:
	}
	if _, _, err := barrier.begin(parent, "root", false, lock); !errors.Is(err, errNativeEffectExpired) {
		t.Fatalf("escaped parent context reused ownership: %v", err)
	}
	childRelease()
	if err := awaitNativeEffectResult(t, observed); err != nil {
		t.Fatal(err)
	}
}

func TestNativeEffectBarrierRejectsDirectionChangeAndPoisonsFailedRelease(t *testing.T) {
	for _, inventory := range []bool{false, true} {
		t.Run(map[bool]string{false: "producer", true: "inventory"}[inventory], func(t *testing.T) {
			var barrier nativeEffectBarrier
			cause := errors.New("ownership descriptor close failed")
			lock := func(context.Context, bool) (func() error, error) { return func() error { return cause }, nil }
			ctx, release, err := barrier.begin(t.Context(), "root", inventory, lock)
			if err != nil {
				t.Fatal(err)
			}
			want := errNativeInventoryNested
			if inventory {
				want = errNativeInventoryMutation
			}
			if _, _, err := barrier.begin(ctx, "root", !inventory, lock); !errors.Is(err, want) {
				t.Fatalf("direction change accepted: %v", err)
			}
			release()
			release()
			if _, _, err := barrier.begin(t.Context(), "root", false, lock); !errors.Is(err, cause) {
				t.Fatalf("failed release did not fence new effects: %v", err)
			}
		})
	}
}

// Wait for the cross-object writer to hold its turnstile while an existing
// shared producer still runs. This observes actual flock ownership, not timing.
func awaitNativeInventoryTurnstile(t *testing.T, root string) {
	t.Helper()
	file, err := openNativeJournalFile(filepath.Join(root, ".native-effects-turnstile.lock"), os.O_RDWR)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	awaitNativeEffect(t, func() bool {
		err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if errors.Is(err, unix.EWOULDBLOCK) {
			return true
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := unix.Flock(int(file.Fd()), unix.LOCK_UN); err != nil {
			t.Fatal(err)
		}
		return false
	})
}

func TestNativeEffectBarrierSharesRootAcrossVMMs(t *testing.T) {
	root := t.TempDir()
	first, reader, other := NewJailerVMM(root, time.Second), NewJailerVMM(root, time.Second), NewJailerVMM(root, time.Second)
	parent, release, err := first.beginNativeEffects(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	observed := make(chan error, 1)
	go func() {
		observed <- reader.withNativeInventory(t.Context(), func(context.Context) error { return nil })
	}()
	awaitNativeInventoryTurnstile(t, root)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, _, err := other.beginNativeEffects(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("another VMM bypassed root inventory: %v", err)
	}
	_, childRelease, err := other.beginNativeEffects(parent)
	if err != nil {
		t.Fatalf("nested same-root cleanup blocked: %v", err)
	}
	t.Cleanup(childRelease)
	release()
	select {
	case err := <-observed:
		t.Fatalf("root lock released before child: %v", err)
	default:
	}
	childRelease()
	if err := awaitNativeEffectResult(t, observed); err != nil {
		t.Fatal(err)
	}
	if _, _, err := other.beginNativeEffects(parent); !errors.Is(err, errNativeEffectExpired) {
		t.Fatalf("stale root permit: %v", err)
	}
}

func TestNativeEffectBarrierModeTransitionDrainsLegacy(t *testing.T) {
	root := t.TempDir()
	legacy := NewJailerVMM(root, time.Second)
	native := NewJailerVMM(root, time.Second).WithNativeProcessRecovery()
	parent, release, err := legacy.beginNativeEffects(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	transition := make(chan error, 1)
	go func() {
		_, finish, err := native.beginNativeEffects(t.Context())
		if err == nil {
			finish()
		}
		transition <- err
	}()
	awaitNativeInventoryTurnstile(t, root)
	_, nestedRelease, err := legacy.beginNativeEffects(parent)
	if err != nil {
		t.Fatal(err)
	}
	nestedRelease()
	release()
	if err := awaitNativeEffectResult(t, transition); err != nil {
		t.Fatal(err)
	}
	daemonLock := native.nativeRecovery.daemonLock
	t.Cleanup(func() { _ = daemonLock.Close() })
	if _, _, err := legacy.beginNativeEffects(t.Context()); err == nil {
		t.Fatal("legacy effects bypassed durable native ownership")
	}
	if err := native.nativeRecovery.daemonLock.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := native.beginNativeEffects(t.Context()); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed ownership accepted: %v", err)
	}
	native.nativeRecovery.daemonLock = nil
	if _, _, err := native.beginNativeEffects(t.Context()); err == nil {
		t.Fatal("lost original ownership descriptor was reacquired")
	}
}

type barrierWakeRunner struct {
	entered, resume chan struct{}
	once            sync.Once
	cause           error
}

func (r *barrierWakeRunner) Run(_ context.Context, argv []string) error {
	if !strings.Contains(strings.Join(argv, " "), "ip netns add ") {
		return nil
	}
	first := false
	r.once.Do(func() { first = true; close(r.entered); <-r.resume })
	if first {
		return r.cause
	}
	return nil
}

func TestNativeEffectBarrierWakeRemainsPinnedThroughCleanup(t *testing.T) {
	manager, vmm, _ := nativeManagerFixture(t)
	if err := manager.RecoverNativeProcesses(t.Context()); err != nil {
		t.Fatal(err)
	}
	runner := &barrierWakeRunner{entered: make(chan struct{}), resume: make(chan struct{}), cause: errors.New("network setup failed")}
	manager.run = runner
	var unblock sync.Once
	t.Cleanup(func() { unblock.Do(func() { close(runner.resume) }) })
	wakeResult := make(chan error, 1)
	go func() {
		_, err := manager.Wake(t.Context(), WakeRequest{Instance: "barrier-wake", Plan: api.PlanHobby, MemSizeMiB: 256})
		wakeResult <- err
	}()
	select {
	case <-runner.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("wake never reached its host helper")
	}
	inventory := make(chan error, 1)
	go func() {
		inventory <- vmm.withNativeInventory(t.Context(), func(context.Context) error {
			record, err := vmm.nativeRecovery.journal.read("barrier-wake")
			if err != nil {
				return err
			}
			if !record.Revoked || !record.ExitConfirmed || !record.ResourcesRemoved || manager.alloc.InUse() != 0 {
				return errors.New("inventory observed incomplete wake rollback")
			}
			return nil
		})
	}()
	awaitNativeEffect(t, func() bool {
		vmm.nativeEffects.mu.Lock()
		defer vmm.nativeEffects.mu.Unlock()
		return vmm.nativeEffects.waitingInventories > 0
	})
	unblock.Do(func() { close(runner.resume) })
	if err := awaitNativeEffectResult(t, wakeResult); !errors.Is(err, runner.cause) {
		t.Fatalf("wake error: %v", err)
	}
	if err := awaitNativeEffectResult(t, inventory); err != nil {
		t.Fatal(err)
	}
}

func TestNativeEffectBarrierInventoryBlocksNativeSurfaces(t *testing.T) {
	manager, vmm, _ := nativeManagerFixture(t)
	if err := manager.RecoverNativeProcesses(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, release, err := vmm.beginNativeInventory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	lease := Lease{Instance: "inventory-fenced"}
	operations := map[string]func(context.Context) error{
		"app wake":       func(ctx context.Context) error { _, err := manager.Wake(ctx, WakeRequest{}); return err },
		"job wake":       func(ctx context.Context) error { _, err := manager.BootJob(ctx, JobBootRequest{}); return err },
		"parent mount":   func(ctx context.Context) error { _, err := manager.MountParentExt4(ctx, ""); return err },
		"parent unmount": func(ctx context.Context) error { return manager.UmountParentExt4(ctx, "") },
		"overlay mount":  func(ctx context.Context) error { return manager.MountOverlayParent(ctx, "", "", "", "") },
		"host sweep": func(ctx context.Context) error {
			return manager.RunNativeHostMaintenance(ctx, func(context.Context) error { t.Error("maintenance ran during inventory"); return nil })
		},
		"network policy":    func(ctx context.Context) error { return manager.UpdateEgressPorts(ctx, "", nil) },
		"cpu policy":        func(ctx context.Context) error { return manager.UpdateAppCPULimit(ctx, "", 1, 250) },
		"boot":              func(ctx context.Context) error { return vmm.Boot(ctx, lease, VMConfig{}, "") },
		"restore":           func(ctx context.Context) error { return vmm.Restore(ctx, lease, RestoreSpec{}) },
		"resume":            func(ctx context.Context) error { return vmm.ResumeServingVM(ctx, lease, ServingResumeSpec{}) },
		"retirement":        func(ctx context.Context) error { return manager.Destroy(ctx, "") },
		"builder interrupt": func(ctx context.Context) error { _, err := vmm.InterruptBuild(ctx, ""); return err },
		"lease preparation": func(ctx context.Context) error { return vmm.prepareNativeLease(ctx, lease) },
		"host helper":       func(ctx context.Context) error { _, err := vmm.runHostHelper(ctx, "", nil); return err },
		"cleanup proof":     func(ctx context.Context) error { return vmm.confirmNativeCleanup(ctx, lease, netns.Config{}) },
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			if err := operation(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("producer bypassed inventory: %v", err)
			}
		})
	}
}

func TestNativeEffectBarrierCPUCallbackUsesFreshOwnershipAndRechecksTail(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "restore", true: "replacement"}[replace], func(t *testing.T) {
			root := withFakeCgroupRoot(t)
			lease := Lease{Instance: "cpu-barrier", Plan: api.PlanPro, CPUMillicores: 250}
			scope := filepath.Join(root, ParentCgroupFor(lease.Plan), PerInstanceScope(lease.Instance))
			if err := os.MkdirAll(scope, 0o755); err != nil {
				t.Fatal(err)
			}
			quota := filepath.Join(scope, "cpu.max")
			if err := os.WriteFile(quota, []byte("500000 500000\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			vmm := NewJailerVMM(t.TempDir(), time.Second)
			ctx, finish, err := vmm.beginNativeEffects(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			vmm.scheduleStartupCPUBoostTail(ctx, lease, nil, startupCPUProfile{StartupMillicores: 1000, ConfiguredMillicores: 250}, time.Now(), time.Hour)
			tail := vmm.cpuBoostTails[lease.Instance]
			tail.timer.Stop()
			finish()
			_, release, err := vmm.beginNativeInventory(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(release)
			if replace {
				vmm.mu.Lock()
				vmm.cpuBoostTails[lease.Instance] = &startupCPUBoostTail{lease: lease}
				vmm.mu.Unlock()
			}
			finished := make(chan error, 1)
			go func() { vmm.finishStartupCPUBoostTail(tail.ctx, tail); finished <- nil }()
			select {
			case <-finished:
				t.Fatal("callback reused its released wake permit")
			case <-time.After(20 * time.Millisecond):
			}
			body, err := os.ReadFile(quota)
			if err != nil || string(body) != "500000 500000\n" {
				t.Fatalf("inventory was mutated: %q %v", body, err)
			}
			release()
			if err := awaitNativeEffectResult(t, finished); err != nil {
				t.Fatal(err)
			}
			body, err = os.ReadFile(quota)
			want := "125000 500000\n"
			if replace {
				want = "500000 500000\n"
			}
			if err != nil || string(body) != want {
				t.Fatalf("callback quota: %q want %q err=%v", body, want, err)
			}
		})
	}
}

func TestNativeEffectBarrierRecoveryPinsDiscoveryThroughQuarantine(t *testing.T) {
	manager, vmm, _ := nativeManagerFixture(t)
	prepareRecoveredNative(t, vmm, 0, "old-barrier-owner")
	entered, resume := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	t.Cleanup(func() { unblock.Do(func() { close(resume) }) })
	vmm.nativeRecovery.inventory = func([]Lease) error { close(entered); <-resume; return nil }
	recovery := make(chan error, 1)
	go func() { recovery <- manager.RecoverNativeProcesses(t.Context()) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("recovery did not reach native inventory")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := vmm.prepareNativeLease(ctx, leaseForSlot("new-barrier-owner", 1)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("producer entered partial recovery: %v", err)
	}
	if manager.alloc.InUse() != 0 {
		t.Fatal("fixture did not exercise scan-to-reservation gap")
	}
	observed := make(chan error, 1)
	go func() {
		observed <- vmm.withNativeInventory(t.Context(), func(context.Context) error {
			if manager.alloc.InUse() != 1 {
				return errors.New("discovered lease escaped before allocator quarantine")
			}
			return nil
		})
	}()
	unblock.Do(func() { close(resume) })
	if err := awaitNativeEffectResult(t, recovery); err != nil {
		t.Fatal(err)
	}
	if err := awaitNativeEffectResult(t, observed); err != nil {
		t.Fatal(err)
	}
	if !manager.nativeRecoveryReady || len(manager.nativeRecovered["old-barrier-owner"]) == 0 || manager.LiveCount() != 0 {
		t.Fatal("quarantine did not preserve original ownership without serving authority")
	}
}

func TestNativeEffectBarrierRequiresProtectedRootAndPrivateLocks(t *testing.T) {
	for _, kind := range []string{"writable root", "symlink root", "writable lock", "symlink lock", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			ctx := t.Context()
			switch kind {
			case "writable root":
				if err := os.Chmod(root, 0o777); err != nil {
					t.Fatal(err)
				}
			case "symlink root":
				link := filepath.Join(t.TempDir(), "root")
				if err := os.Symlink(root, link); err != nil {
					t.Fatal(err)
				}
				root = link
			case "writable lock":
				path := filepath.Join(root, ".native-effects-turnstile.lock")
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0o666); err != nil {
					t.Fatal(err)
				}
			case "symlink lock":
				if err := os.Symlink(filepath.Join(t.TempDir(), "outside"), filepath.Join(root, ".native-effects-turnstile.lock")); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				root = filepath.Join(root, "not-created")
			}
			vmm := NewJailerVMM(root, time.Second)
			if _, release, err := vmm.beginNativeEffects(ctx); err == nil {
				release()
				t.Fatal("unsafe coordination accepted")
			}
			if kind == "cancelled" {
				if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("cancelled producer created root: %v", err)
				}
			}
		})
	}
}

func TestNativeEffectBarrierFreshDaemonCannotBypassExistingOwner(t *testing.T) {
	root := t.TempDir()
	first := NewJailerVMM(root, time.Second).WithNativeProcessRecovery()
	_, release, err := first.beginNativeEffects(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	release()
	t.Cleanup(func() { _ = first.nativeRecovery.daemonLock.Close() })
	other := NewJailerVMM(root, time.Second).WithNativeProcessRecovery()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, _, err := other.beginNativeEffects(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second daemon bypassed owner: %v", err)
	}
	if other.nativeRecovery.daemonLock != nil || other.nativeRecovery.everOwned || len(other.nativeRecovery.owned) != 0 {
		t.Fatal("failed initialization acquired original producer ownership")
	}
	_, release, err = first.beginNativeEffects(t.Context())
	if err != nil {
		t.Fatalf("cancelled inventory poisoned existing owner: %v", err)
	}
	release()
}
