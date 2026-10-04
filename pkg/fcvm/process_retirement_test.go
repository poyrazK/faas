// adr: 532 — environment intent and runtime ownership contracts.
package fcvm

import (
	"context"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestNativeKillTimeoutRetainsRecordAndChroot(t *testing.T) {
	v := NewJailerVMM(t.TempDir(), 20*time.Millisecond)
	v.destroyWait = 20 * time.Millisecond
	id := "unconfirmed-native-exit"
	rec := &instanceRecord{done: make(chan struct{})}
	v.recs[id] = rec
	root := v.chrootRoot(id)
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, stop := range []func() error{
		func() error { return v.Kill(t.Context(), Lease{Instance: id}) },
		func() error { _, err := v.DestroyWithExport(t.Context(), Lease{Instance: id}, ""); return err },
	} {
		if err := stop(); err == nil || !strings.Contains(err.Error(), "not confirmed") {
			t.Fatalf("unobserved exit acknowledged: %v", err)
		}
		if v.recs[id] != rec {
			t.Fatal("unobserved process lost its recovery record")
		}
		if _, err := os.Stat(root); err != nil {
			t.Fatalf("unobserved process lost its chroot: %v", err)
		}
	}
	close(rec.done)
	if _, err := v.DestroyWithExport(t.Context(), Lease{Instance: id}, ""); err != nil {
		t.Fatal(err)
	}
	if v.recs[id] != nil {
		t.Fatal("confirmed exit retained its record")
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("confirmed exit retained its chroot: %v", err)
	}
}

func TestNativeSignalTimeoutRetainsUnobservedProcess(t *testing.T) {
	for _, grace := range []time.Duration{0, 10 * time.Millisecond} {
		t.Run(grace.String(), func(t *testing.T) {
			v := NewJailerVMM(t.TempDir(), 20*time.Millisecond)
			v.destroyWait = 20 * time.Millisecond
			id := "unobserved-signalled-exit"
			rec := &instanceRecord{done: make(chan struct{})}
			v.recs[id] = rec
			killed, _, err := v.SignalAndKill(t.Context(), Lease{Instance: id}, syscall.SIGTERM, grace)
			if !killed || err == nil || v.recs[id] != rec {
				t.Fatalf("unobserved signal exit: killed=%v err=%v retained=%v", killed, err, v.recs[id] == rec)
			}
			close(rec.done)
			if err := v.Kill(t.Context(), Lease{Instance: id}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestManagerUncertainProcessRetainsReservationAcrossLifecycle(t *testing.T) {
	for _, lifecycle := range []string{"destroy", "signal", "park", "process_exit", "failed_cold_boot", "failed_restore"} {
		t.Run(lifecycle, func(t *testing.T) {
			vmm := &fakeVMM{}
			m := newTestManager(&fakeRunner{}, vmm)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			id := "uncertain-" + lifecycle
			failedBoot := strings.HasPrefix(lifecycle, "failed_")
			if !failedBoot {
				if _, err := m.ColdBoot(ctx, req(id)); err != nil {
					t.Fatal(err)
				}
			}
			uncertain := errors.New("process retirement unconfirmed")
			vmm.killErr = uncertain
			var err error
			switch lifecycle {
			case "destroy":
				err = m.Destroy(ctx, id)
			case "signal":
				_, _, err = m.SignalAndKill(ctx, id, syscall.SIGTERM, 0)
			case "park":
				_, err = m.Park(ctx, id, SnapshotSpec{})
			case "process_exit":
				m.ProcessExited(id, 137)
				err = m.Destroy(ctx, id)
			case "failed_cold_boot":
				vmm.bootErr = errors.New("boot failed")
				_, err = m.ColdBoot(ctx, req(id))
			case "failed_restore":
				vmm.restoreErr = errors.New("restore failed")
				_, err = m.Wake(ctx, wakeReq(id, usableSnapshot()))
				if vmm.boots() != 0 {
					t.Fatal("cold-boot fallback overlapped an unconfirmed restore")
				}
			}
			wantLive := 1
			if failedBoot {
				wantLive = 0
			}
			if !errors.Is(err, uncertain) || m.LiveCount() != wantLive || m.LeasedCount() != 1 || len(m.pendingCleanup) != 1 {
				t.Fatalf("lost uncertainty: err=%v live=%d leased=%d", err, m.LiveCount(), m.LeasedCount())
			}
			owned := m.teardownIdentity(id)
			if owned == nil {
				t.Fatal("uncertain retirement lost its complete identity")
			}
			cid := GuestVsockCID(owned.Lease.Slot)
			if _, err := m.InstanceByCID(cid); err == nil {
				t.Fatal("uncertain stop retained the guest readiness/broker join")
			}
			if _, err := m.ColdBoot(ctx, req(id)); err == nil {
				t.Fatal("replacement reused an unconfirmed reservation")
			}
			vmm.killErr = nil
			if err := m.Destroy(ctx, id); err != nil || m.LiveCount() != 0 || m.LeasedCount() != 0 {
				t.Fatalf("process retirement recovery: err=%v live=%d leased=%d", err, m.LiveCount(), m.LeasedCount())
			}
		})
	}
}

func TestNativeKillMissingWatchdogRetainsProcessForRecovery(t *testing.T) {
	v, id, rec := runningBuildProcess(t)
	delete(v.recs, id)
	root := v.chrootRoot(id)
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, stop := range []func() error{
		func() error { return v.Kill(t.Context(), Lease{Instance: id}) },
		func() error { _, err := v.DestroyWithExport(t.Context(), Lease{Instance: id}, ""); return err },
	} {
		if err := stop(); err == nil || !strings.Contains(err.Error(), "missing watchdog") {
			t.Fatalf("missing watchdog reported absence: %v", err)
		}
		if v.proc[id] == nil {
			t.Fatal("missing watchdog lost its process registration")
		}
		if _, err := os.Stat(root); err != nil {
			t.Fatalf("missing watchdog removed its chroot: %v", err)
		}
	}
	select {
	case <-rec.done:
	case <-time.After(2 * time.Second):
		t.Fatal("test child did not exit")
	}
	v.recs[id] = rec
	if _, err := v.DestroyWithExport(t.Context(), Lease{Instance: id}, ""); err != nil || v.proc[id] != nil || v.recs[id] != nil {
		t.Fatalf("watchdog recovery: err=%v process=%v record=%v", err, v.proc[id] != nil, v.recs[id] != nil)
	}
}
