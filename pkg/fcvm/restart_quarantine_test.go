// adr: 398
package fcvm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func restartFixture(t *testing.T) restartInventoryOptions {
	t.Helper()
	root := t.TempDir()
	opts := restartInventoryOptions{filepath.Join(root, "proc"), filepath.Join(root, "net"), filepath.Join(root, "netns"), filepath.Join(root, "jails")}
	for _, dir := range []string{opts.procRoot, opts.netRoot, opts.netnsRoot, opts.jailRoot} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	bootPath := filepath.Join(opts.procRoot, "sys/kernel/random/boot_id")
	if err := os.MkdirAll(filepath.Dir(bootPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bootPath, []byte(idLive), 0o600); err != nil {
		t.Fatal(err)
	}
	return opts
}

func restartProcessFixture(t *testing.T, opts restartInventoryOptions, pid, slot int, args ...string) {
	t.Helper()
	path := filepath.Join(opts.procRoot, fmt.Sprint(pid))
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "cmdline"), []byte(strings.Join(args, "\x00")+"\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	uid := JailUIDBase + slot
	if err := os.WriteFile(filepath.Join(path, "status"), fmt.Appendf(nil, "Uid:\t%d\t%d\t%d\t%d\n", uid, uid, uid, uid), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRestartQuarantineProtectsSurvivingIdentities(t *testing.T) {
	opts := restartFixture(t)
	// Real Firecracker argv omits jailer's --uid. Both processes of the
	// same logical instance must reserve their slots, rather than overwrite.
	restartProcessFixture(t, opts, 101, 0, "firecracker-v1.7.0", "--id", idLive)
	restartProcessFixture(t, opts, 102, 2, "firecracker", "--id", idLive, "--uid", "29999")
	restartProcessFixture(t, opts, 103, 9, "jailer", "--id", idOther, "--exec-file", "/bin/firecracker", "--uid", "20001")
	restartProcessFixture(t, opts, 104, 3, "unrelated", "--id", idDead)
	for root, names := range map[string][]string{
		opts.netRoot:   {"vh0", "vp4", "gpn-h00005", "gpn-p00006", "vh01", "vh10000", "br-tenants"},
		opts.netnsRoot: {"fc-" + idDead}, opts.jailRoot: {idLive},
	} {
		for _, name := range names {
			if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	vmm := &fakeVMM{}
	run := &fakeRunner{}
	m := newTestManager(run, vmm)
	rep, err := m.recoverRestartQuarantine(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if rep != (RestartQuarantineReport{Slots: 6, Instances: 3, Processes: 3}) {
		t.Fatalf("inventory = %+v", rep)
	}
	// Slots 0,1,2,4,5,6 are observed. Ordinary and prepared allocation
	// must both skip them; Release cannot manufacture a free survivor slot.
	lease, err := m.alloc.Acquire("fresh")
	if err != nil || lease.Slot != 3 {
		t.Fatalf("fresh lease = %+v, %v", lease, err)
	}
	prepared, err := m.alloc.reserveNetwork("prepared")
	if err != nil || prepared.Slot != 7 {
		t.Fatalf("prepared lease = %+v, %v", prepared, err)
	}
	if err := m.alloc.Release(idLive); err == nil {
		t.Fatal("quarantined guest became releasable")
	}
	for _, id := range []string{idLive, idDead, idOther} {
		if m.HasInstanceOwnership(id) || m.LiveCount() != 0 {
			t.Fatal("observation became recovered lifecycle ownership")
		}
		if _, err := m.Wake(t.Context(), WakeRequest{Instance: id}); !errors.Is(err, ErrRestartQuarantine) {
			t.Fatalf("Wake(%s): %v", id, err)
		}
		if _, err := m.BootJob(t.Context(), JobBootRequest{Instance: id}); !errors.Is(err, ErrRestartQuarantine) {
			t.Fatalf("BootJob(%s): %v", id, err)
		}
		if err := m.Destroy(t.Context(), id); !errors.Is(err, ErrRestartQuarantine) {
			t.Fatalf("Destroy(%s): %v", id, err)
		}
		if _, _, err := m.SignalAndKill(t.Context(), id, syscall.SIGTERM, time.Second); !errors.Is(err, ErrRestartQuarantine) {
			t.Fatalf("SignalAndKill(%s): %v", id, err)
		}
		if _, err := m.DestroyWithExport(t.Context(), id, "/tmp/export"); !errors.Is(err, ErrRestartQuarantine) {
			t.Fatalf("DestroyWithExport(%s): %v", id, err)
		}
		if _, err := m.SnapshotKeepAlive(t.Context(), id, SnapshotSpec{}); !errors.Is(err, ErrRestartQuarantine) {
			t.Fatalf("SnapshotKeepAlive(%s): %v", id, err)
		}
	}
	if len(run.commands) != 0 || len(vmm.killed)+vmm.bootCount != 0 {
		t.Fatal("inventory or rejected operations touched guest resources")
	}
}

func TestRestartQuarantineFailsClosedBeforeMutation(t *testing.T) {
	for _, name := range []string{"unknown_uid", "unknown_id", "unreadable_argv", "missing_proc", "missing_sysfs", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			opts := restartFixture(t)
			restartProcessFixture(t, opts, 101, 0, "firecracker", "--id", idLive)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch name {
			case "unknown_uid":
				if err := os.WriteFile(filepath.Join(opts.procRoot, "101/status"), []byte("Uid:\t0\t0\t0\t0\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "unknown_id":
				if err := os.WriteFile(filepath.Join(opts.procRoot, "101/cmdline"), []byte("firecracker\x00--no-api\x00"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "unreadable_argv":
				path := filepath.Join(opts.procRoot, "101/cmdline")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "missing_proc":
				opts.procRoot += "-absent"
			case "missing_sysfs":
				opts.netRoot += "-absent"
			case "cancelled":
				cancel()
			}
			m := newTestManager(&fakeRunner{}, &fakeVMM{})
			if _, err := m.recoverRestartQuarantine(ctx, opts); err == nil {
				t.Fatal("incomplete inventory admitted")
			}
			if !m.alloc.pristine() || m.restartInventoryDone || len(m.restartQuarantine) != 0 {
				t.Fatal("failed inventory mutated allocation state")
			}
		})
	}
}

func TestRestartQuarantineOnlyAtStartup(t *testing.T) {
	for _, name := range []string{"lease", "prepared_reservation", "boot", "stop", "already_scanned"} {
		t.Run(name, func(t *testing.T) {
			m := newTestManager(&fakeRunner{}, &fakeVMM{})
			switch name {
			case "lease":
				if _, err := m.alloc.Acquire("live"); err != nil {
					t.Fatal(err)
				}
			case "prepared_reservation":
				if _, err := m.alloc.reserveNetwork("prepared"); err != nil {
					t.Fatal(err)
				}
			case "boot":
				_, flight, err := m.beginInstanceBoot(t.Context(), "boot")
				if err != nil {
					t.Fatal(err)
				}
				defer m.finishInstanceFlight("boot", flight)
			case "stop":
				stop, err := m.beginInstanceStop(t.Context(), "stop")
				if err != nil {
					t.Fatal(err)
				}
				defer m.finishInstanceStop("stop", stop)
			case "already_scanned":
				if _, err := m.recoverRestartQuarantine(t.Context(), restartFixture(t)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := m.recoverRestartQuarantine(t.Context(), restartFixture(t)); err == nil {
				t.Fatal("late inventory accepted")
			}
		})
	}
}

func TestRestartQuarantineAllowsUnusedNode(t *testing.T) {
	opts := restartFixture(t)
	opts.netnsRoot += "-absent"
	opts.jailRoot += "-absent"
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	rep, err := m.recoverRestartQuarantine(t.Context(), opts)
	if err != nil || rep != (RestartQuarantineReport{}) || !m.alloc.pristine() {
		t.Fatalf("empty node: %+v, %v", rep, err)
	}
}

func TestRestartQuarantineRejectsAmbiguousIdentities(t *testing.T) {
	for _, status := range []string{"", "Uid: 19999 19999 19999 19999", "Uid: 30000 30000 30000 30000", "Uid: 20000 20001 20000 20000", "Uid: 20000 20000", "Uid: 18446744073709571616 18446744073709571616 18446744073709571616 18446744073709571616"} {
		if slot := restartProcessSlot(status); slot != -1 {
			t.Fatalf("ambiguous status accepted: %q -> %d", status, slot)
		}
	}
	for _, uid := range []string{"", "-1", "19999", "30000", "18446744073709571616"} {
		if slot := restartJailerSlot([]string{"jailer", "--uid", uid}); slot != -1 {
			t.Fatalf("ambiguous jailer UID accepted: %q -> %d", uid, slot)
		}
	}
	for _, args := range [][]string{
		{"unrelated", "--id", idLive},
		{"jailer", "--id", idLive, "--exec-file", "/bin/unrelated"},
		{"jailer", "--id", "../invalid", "--exec-file", "/bin/firecracker"},
	} {
		if id, _ := restartProcessID(args); id != "" {
			t.Fatalf("unrelated process accepted: %v", args)
		}
	}
}

func TestRestartQuarantineCoversInternalGuestIDs(t *testing.T) {
	opts := restartFixture(t)
	compact := "0123456789abcdef0123456789abcdef"
	builder := "build-" + idLive
	restartProcessFixture(t, opts, 101, 0, "firecracker", "--id", compact)
	restartProcessFixture(t, opts, 102, 1, "firecracker", "--id", builder)
	for _, name := range []string{"fc-" + builder, "fc-prepared-" + idOther, idOther} {
		if err := os.WriteFile(filepath.Join(opts.netnsRoot, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// No veths or jails: compact/networkless IDs must still reserve their UID.
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	rep, err := m.recoverRestartQuarantine(t.Context(), opts)
	if err != nil || rep != (RestartQuarantineReport{Slots: 2, Instances: 2, Processes: 2}) {
		t.Fatalf("internal inventory: %+v, %v", rep, err)
	}
	lease, err := m.alloc.Acquire("fresh")
	if err != nil || lease.Slot != 2 {
		t.Fatalf("internal survivor slot reused: %+v, %v", lease, err)
	}
	for _, id := range []string{compact, builder} {
		if err := m.Destroy(t.Context(), id); !errors.Is(err, ErrRestartQuarantine) {
			t.Fatalf("unowned internal stop: %v", err)
		}
	}
}
