//go:build linux || darwin

package fcvm

// adr: 771
import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func nativePreparedBootFixture(t *testing.T) (*JailerVMM, Lease, Machine) {
	t.Helper()
	v, lease, f, _ := nativeMachineAPIFixture(t)
	owner, err := v.nativeRecovery.journal.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	owner.Authorized, owner.PID, owner.StartTime = false, 0, 0
	if err := v.nativeRecovery.journal.write(owner); err != nil {
		t.Fatal(err)
	}
	return v, lease, f.machine
}

func TestNativeBootConfigRefusesBeforeStaging(t *testing.T) {
	for _, entry := range []string{"direct", "app", "no-ready", "job"} {
		for _, dimension := range []string{"ram", "vcpu"} {
			t.Run(entry+"/"+dimension, func(t *testing.T) {
				v, lease, machine := nativePreparedBootFixture(t)
				original, err := v.nativeRecovery.journal.read(lease.Instance)
				if err != nil {
					t.Fatal(err)
				}
				if dimension == "ram" {
					machine.MemSizeMib++
				} else {
					machine.VcpuCount++
				}
				trap := &nativeSnapshotPublicationTrap{}
				v.WithStorage(trap)
				switch entry {
				case "direct":
					err = v.Boot(t.Context(), lease, VMConfig{MachineConfig: machine}, "")
				case "app", "no-ready":
					err = v.BootColdBoot(t.Context(), lease, ColdBootSpec{KernelKey: "kernel", BaseKey: "base", LayerKey: "layer", Tap: "tap0", MemSizeMiB: machine.MemSizeMib, VcpuCount: machine.VcpuCount, SkipReady: entry == "no-ready"})
				case "job":
					err = v.BootColdBootForJob(t.Context(), lease, JobColdBootSpec{KernelKey: "kernel", BaseKey: "base", ImageRef: "image", Tap: "tap0", MemSizeMiB: machine.MemSizeMib, VcpuCount: machine.VcpuCount, AccountID: "account", RunID: "run", Command: []string{"true"}, LeaseToken: "token", TaskTimeoutSec: 1})
				}
				if err == nil || !strings.Contains(err.Error(), "native boot:") || trap.storageCalls.Load() != 0 {
					t.Fatalf("invalid config reached staging: err=%v calls=%d", err, trap.storageCalls.Load())
				}
				after, err := v.nativeRecovery.journal.read(lease.Instance)
				if err != nil || !sameNativeObservedOwner(original, after) {
					t.Fatal("preflight changed original provenance", err)
				}
				if _, err := os.Stat(v.chrootRoot(lease.Instance)); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("preflight created a chroot", err)
				}
			})
		}
	}
}

func TestNativeBootPreparedProvenance(t *testing.T) {
	for _, scenario := range []string{"matches", "historical", "legacy", "legacy-wrong-ram", "lease", "generation", "revoked", "authorized", "boot", "daemon", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			v, lease, machine := nativePreparedBootFixture(t)
			r := v.nativeRecovery
			owner, err := r.journal.read(lease.Instance)
			if err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			switch scenario {
			case "historical":
				owner.Version, owner.RegionalResources = 2, nil
			case "legacy", "legacy-wrong-ram":
				owner.Version, owner.RegionalResources, owner.RegionalRuntime = 1, nil, nil
				machine.VcpuCount++ // Unknown legacy vCPU is not invented provenance.
				if scenario == "legacy-wrong-ram" {
					machine.MemSizeMib++
				}
			case "lease":
				lease.CPUMillicores++
			case "generation":
				r.owned[lease.Instance] = uuid.NewString()
			case "revoked":
				owner.Revoked = true
			case "authorized":
				owner.Authorized, owner.PID, owner.StartTime = true, 42, 101
			case "boot":
				owner.KernelBootID = uuid.NewString()
			case "daemon":
				if err := r.daemonLock.Close(); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if err := r.journal.write(owner); err != nil {
				t.Fatal(err)
			}
			_, err = v.nativeBootStagingOwner(ctx, lease, machine)
			if (err == nil) != (scenario == "matches" || scenario == "legacy") {
				t.Fatalf("prepared config check=%v", err)
			}
		})
	}
}

func TestNativeBootConfigHandoffChecksBeforeWrite(t *testing.T) {
	for _, scenario := range []string{"matches", "ram", "vcpu", "zero", "negative", "overhead", "lease", "historical", "dead", "recovered", "revoked", "cancelled", "actual-shape"} {
		t.Run(scenario, func(t *testing.T) {
			v, lease, f, h := nativeMachineAPIFixture(t)
			f.state = "Running"
			machine := f.machine
			ctx := t.Context()
			switch scenario {
			case "ram":
				machine.MemSizeMib++
			case "vcpu":
				machine.VcpuCount++
			case "zero":
				machine.VcpuCount = 0
			case "negative":
				machine.MemSizeMib = -1
			case "overhead":
				machine.MemSizeMib = api.BillableRAMMB(machine.MemSizeMib)
			case "lease":
				lease.MemoryMaxMiB++
			case "historical", "revoked":
				owner, err := v.nativeRecovery.journal.read(lease.Instance)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "historical" {
					owner.Version, owner.RegionalResources = 2, nil
				} else {
					owner.Revoked = true
				}
				if err := v.nativeRecovery.journal.write(owner); err != nil {
					t.Fatal(err)
				}
			case "dead":
				h.setLiveError(errors.New("original task exited"))
			case "recovered":
				delete(v.nativeRecovery.owned, lease.Instance)
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "actual-shape":
				f.machine.VcpuCount++
			}
			path := filepath.Join(t.TempDir(), "config")
			if err := os.WriteFile(path, []byte("unchanged"), 0o600); err != nil {
				t.Fatal(err)
			}
			err := v.releaseNativeBootConfig(ctx, lease, machine, path, []byte("configuration"))
			bytes, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if (err == nil) != (scenario == "matches") {
				t.Fatalf("handoff=%v", err)
			}
			wrote := scenario == "matches" || scenario == "actual-shape"
			want := "unchanged"
			if wrote {
				want = "configuration"
			}
			if string(bytes) != want || !wrote && len(f.requests) != 0 || h.waited || len(h.signals) != 0 {
				t.Fatalf("invalid handoff had effects: bytes=%q requests=%v handle=%+v", bytes, f.requests, h)
			}
		})
	}
}

type nativeBootNotifyingHandle struct {
	*nativeMachineHandleFixture
	checked chan struct{}
}

func (h *nativeBootNotifyingHandle) CheckLive() error {
	select {
	case h.checked <- struct{}{}:
	default:
	}
	return h.nativeMachineHandleFixture.CheckLive()
}

func TestNativeBootConfigWaitingHoldsLockAndRechecksAuthority(t *testing.T) {
	for _, change := range []string{"daemon", "dead", "cancelled"} {
		t.Run(change, func(t *testing.T) {
			v, lease, f, h := nativeMachineAPIFixture(t)
			notify := &nativeBootNotifyingHandle{nativeMachineHandleFixture: h, checked: make(chan struct{}, 4)}
			v.nativeRecovery.retirer.open = func(int) (nativeProcessHandle, error) { return notify, nil }
			path := filepath.Join(t.TempDir(), "fifo")
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- v.releaseNativeBootConfig(ctx, lease, f.machine, path, []byte("config")) }()
			select {
			case <-notify.checked:
			case <-ctx.Done():
				t.Fatal("handoff did not pin the original task")
			}
			lockCtx, lockCancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
			lock, err := v.nativeRecovery.journal.lock(lockCtx, lease.Instance)
			lockCancel()
			if lock != nil {
				_ = lock.Close()
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("original lock not retained during reader wait: %v", err)
			}
			switch change {
			case "daemon":
				if err := v.nativeRecovery.daemonLock.Close(); err != nil {
					t.Fatal(err)
				}
			case "dead":
				h.setLiveError(errors.New("original task exited"))
			case "cancelled":
				cancel()
			}
			select {
			case err := <-done:
				if err == nil || change != "cancelled" && errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("authority loss was not promptly refused: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("handoff outlived its authority")
			}
			if len(f.requests) != 0 || !h.closed {
				t.Fatalf("waiting handoff had API effects or leaked pin: %v", f.requests)
			}
			lock, err = v.nativeRecovery.journal.lock(t.Context(), lease.Instance)
			if err != nil {
				t.Fatal("handoff leaked original lock", err)
			}
			_ = lock.Close()
		})
	}
}

func TestConfigFIFOChecksOwnershipAfterOpenBeforeWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	lost := errors.New("ownership lost after opening")
	err := writeConfigFIFOChecked(t.Context(), path, []byte("config"), time.Second, func() error {
		calls++
		if calls == 2 {
			return lost
		}
		return nil
	})
	bytes, readErr := os.ReadFile(path)
	if !errors.Is(err, lost) || readErr != nil || string(bytes) != "unchanged" || calls != 2 {
		t.Fatalf("opened FIFO bypassed ownership: err=%v read=%v bytes=%q calls=%d", err, readErr, bytes, calls)
	}
}

func TestNativeBootPreflightNeverChangesOriginalResources(t *testing.T) {
	v, lease, machine := nativePreparedBootFixture(t)
	original, err := v.nativeRecovery.journal.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := v.preflightNativeBootConfig(t.Context(), lease, machine); err != nil {
			t.Fatal(err)
		}
	}
	after, err := v.nativeRecovery.journal.read(lease.Instance)
	if err != nil || !reflect.DeepEqual(original, after) {
		t.Fatal("preflight minted new provenance", err)
	}
}
