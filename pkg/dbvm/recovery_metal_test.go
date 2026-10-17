//go:build metal && linux

// adr: 1027 — a database VM outlives vmmd: the next vmmd adopts it with
// the same postmaster, data and slot, stops it cleanly and boots it again.
package dbvm

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

// abandon drops every VM without stopping it, as vmmd's exit does: its
// relays and console reader go, Firecracker stays.
func (r *Runtime) abandon() {
	r.filling.Lock()
	r.sparesStopped = true
	r.filling.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, vm := range r.vms {
		if vm.archive != nil {
			_ = vm.archive.Close()
		}
		if vm.replication != nil {
			_ = vm.replication.Close()
		}
		if vm.importLn != nil {
			_ = vm.importLn.Close()
		}
		if vm.console != nil {
			_ = vm.console.Close()
		}
		delete(r.vms, id)
	}
}

// signalMasks returns a process's ignored and caught signal masks.
func signalMasks(t *testing.T, pid int) (ignored, caught string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if rest, ok := strings.CutPrefix(line, "SigIgn:"); ok {
			ignored = strings.TrimSpace(rest)
		}
		if rest, ok := strings.CutPrefix(line, "SigCgt:"); ok {
			caught = strings.TrimSpace(rest)
		}
	}
	return ignored, caught
}

func TestMetalRuntimeAdoptsVMsAfterARestart(t *testing.T) {
	env := loadMetalEnv(t)
	parent := "dbvm-adopt-metal"
	if err := os.MkdirAll(filepath.Join("/sys/fs/cgroup", parent), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Join("/sys/fs/cgroup", parent)) })
	cfgFor := func(leases Leaser) RuntimeConfig {
		return RuntimeConfig{
			JailBase: filepath.Join(env.workdir, "ajail"), VolumeRoot: filepath.Join(env.workdir, "avolumes"),
			KernelPath: env.kernel, Bases: map[int]string{17: env.base}, ParentCgroup: parent,
			JailerBin: envOr("FAAS_JAILER", "jailer"), FirecrackerBin: env.firecracker, Leases: leases,
		}
	}
	first := &fakeLeaser{}
	rt, err := NewRuntime(cfgFor(first))
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Recover(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	id := "3e1f5a7b-9c2d-4e6f-8a1b-2c3d4e5f6a7b"
	_ = rt.DeleteVolume(ctx, id)
	if err := rt.CreateVolume(ctx, id, 1<<30); err != nil {
		t.Fatal(err)
	}
	boot := BootRequest{ID: id, Major: 17, VcpuCount: 1, MemSizeMiB: 384}
	if err := rt.Start(ctx, boot); err != nil {
		t.Fatal(err)
	}
	// Whichever runtime holds the VM stops it; one abandoned halfway is
	// cleaned up by hand.
	owner := rt
	rt.mu.Lock()
	jail := rt.vms[id]
	rt.mu.Unlock()
	t.Cleanup(func() {
		if err := owner.Stop(context.Background(), id); err != nil {
			killJailProcesses(parent, jail.jailID)
			unmountJail(mountsUnder(jail.dir))
			_ = os.RemoveAll(jail.dir)
			removeJailCgroup(parent, jail.jailID)
		}
		_ = owner.DeleteVolume(context.Background(), id)
	})
	if resp, err := rt.Control(ctx, id, dbguest.Request{Op: dbguest.OpBootstrap}); err != nil || resp.Error != "" {
		t.Fatalf("bootstrap: %+v %v", resp, err)
	}
	if _, err := rt.WaitState(ctx, id, dbguest.StateReady); err != nil {
		t.Fatal(err)
	}
	password := hex.EncodeToString([]byte("adopt-metal-test-password-0001"))
	createAppRole(t, func(req dbguest.Request) (dbguest.Response, error) { return rt.Control(ctx, id, req) }, password)
	conn := connectRuntime(ctx, t, rt, id, password)
	if _, err := conn.Exec(ctx, `CREATE TABLE kept (v int); INSERT INTO kept SELECT generate_series(1, 1000)`); err != nil {
		t.Fatal(err)
	}
	var started time.Time
	if err := conn.QueryRow(ctx, `SELECT pg_postmaster_start_time()`).Scan(&started); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(ctx)
	rt.mu.Lock()
	vm := rt.vms[id]
	rt.mu.Unlock()
	pid, jailID, slot := vm.proc.Pid, vm.jailID, vm.lease.Slot
	// vmmd exits; the next one adopts the VM.
	rt.abandon()
	// With no one reading its console, a console write raises SIGPIPE in
	// Firecracker. It must survive that: raise one.
	ignored, caught := signalMasks(t, pid)
	t.Logf("firecracker SigIgn %s SigCgt %s", ignored, caught)
	if err := unix.Kill(pid, unix.SIGPIPE); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	if err := unix.Kill(pid, 0); err != nil {
		t.Fatalf("Firecracker died of SIGPIPE: %v", err)
	}
	second := &fakeLeaser{}
	rt2, err := NewRuntime(cfgFor(second))
	if err != nil {
		t.Fatal(err)
	}
	if err := rt2.Recover(); err != nil {
		t.Fatal(err)
	}
	owner = rt2
	rt2.mu.Lock()
	adopted := rt2.vms[id]
	rt2.mu.Unlock()
	if adopted == nil || adopted.proc.Pid != pid || adopted.jailID != jailID || adopted.lease.Slot != slot {
		t.Fatalf("adopted %+v; want pid %d, jail %s, slot %d", adopted, pid, jailID, slot)
	}
	if lease, ok := second.held[jailID]; !ok || lease.Slot != slot {
		t.Fatalf("the new vmmd's leases = %v", second.held)
	}
	resp, err := rt2.Control(ctx, id, dbguest.Request{Op: dbguest.OpStatus})
	if err != nil || resp.State != dbguest.StateReady {
		t.Fatalf("status through the new vmmd = %+v, %v", resp, err)
	}
	conn = connectRuntime(ctx, t, rt2, id, password)
	var count int64
	var still time.Time
	if err := conn.QueryRow(ctx, `SELECT count(*), pg_postmaster_start_time() FROM kept`).Scan(&count, &still); err != nil || count != 1000 || !still.Equal(started) {
		t.Fatalf("after adoption: %d rows, postmaster %v (was %v), %v", count, still, started, err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO kept VALUES (1001)`); err != nil {
		t.Fatalf("write after adoption: %v", err)
	}
	_ = conn.Close(ctx)

	// The new vmmd stops it cleanly and leaves nothing behind.
	if err := rt2.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(adopted.dir); !os.IsNotExist(err) {
		t.Fatalf("jail left behind: %v", err)
	}
	if _, err := os.Stat(filepath.Join("/sys/fs/cgroup", parent, jailID)); !os.IsNotExist(err) {
		t.Fatalf("cgroup left behind: %v", err)
	}
	if _, held := second.held[jailID]; held {
		t.Fatal("the adopted lease was not released")
	}
	// A clean stop: the next boot needs no crash recovery, and the data is there.
	if err := rt2.Start(ctx, boot); err != nil {
		t.Fatal(err)
	}
	rt2.mu.Lock()
	jail = rt2.vms[id]
	rt2.mu.Unlock()
	if _, err := rt2.WaitState(ctx, id, dbguest.StateReady); err != nil {
		t.Fatal(err)
	}
	conn = connectRuntime(ctx, t, rt2, id, password)
	defer func() { _ = conn.Close(ctx) }()
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM kept`).Scan(&count); err != nil || count != 1001 {
		t.Fatalf("after a restart on the new vmmd: %d rows, %v", count, err)
	}
	console, err := os.ReadFile(filepath.Join(jail.dir, "console.log"))
	if err != nil || len(console) == 0 {
		t.Fatalf("console log through the FIFO: %d bytes, %v", len(console), err)
	}
}

// A standby's node restarts vmmd: the new vmmd adopts the standby and its
// relay, and the standby streams from its primary again.
func TestMetalAdoptedStandbyStreamsAgain(t *testing.T) {
	env := loadMetalEnv(t)
	primary := metalNodeRuntime(t, env, "e", &fakeLeaser{})
	standby := metalNodeRuntime(t, env, "f", &offsetLeaser{offset: 5})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	id := "5d7e9f1a-3b4c-4d5e-8f6a-7b8c9d0e1f2a"
	standbyOwner := standby
	for _, rt := range []*Runtime{primary, standby} {
		_ = rt.DeleteVolume(ctx, id)
		if err := rt.CreateVolume(ctx, id, 1<<30); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = primary.Stop(context.Background(), id)
		_ = primary.DeleteVolume(context.Background(), id)
		_ = standbyOwner.Stop(context.Background(), id)
		_ = standbyOwner.DeleteVolume(context.Background(), id)
	})
	boot := BootRequest{ID: id, Major: 17, VcpuCount: 1, MemSizeMiB: 384}
	if err := primary.Start(ctx, boot); err != nil {
		t.Fatal(err)
	}
	if resp, err := primary.Control(ctx, id, dbguest.Request{Op: dbguest.OpBootstrap}); err != nil || resp.Error != "" {
		t.Fatalf("bootstrap: %+v %v", resp, err)
	}
	if _, err := primary.WaitState(ctx, id, dbguest.StateReady); err != nil {
		t.Fatal(err)
	}
	password := hex.EncodeToString([]byte("adopt-standby-metal-password-01"))
	createAppRole(t, func(req dbguest.Request) (dbguest.Response, error) { return primary.Control(ctx, id, req) }, password)
	conn := connectRuntime(ctx, t, primary, id, password)
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, "create table rows(v text); insert into rows select 'seeded-' || g from generate_series(1, 1000) g"); err != nil {
		t.Fatal(err)
	}
	// The replication role and slot the standby seeds through.
	replicationStatus(ctx, t, primary, id, dbguest.ReplicationAsync)
	dial := func(ctx context.Context) (io.ReadWriteCloser, error) { return primary.DialReplication(ctx, id) }
	standbyBoot := boot
	standbyBoot.Primary, standbyBoot.PrimaryNode, standbyBoot.PrimaryTarget = dial, "db-e", "10.42.0.5:9443"
	if err := standby.Start(ctx, standbyBoot); err != nil {
		t.Fatal(err)
	}
	if _, err := standby.WaitState(ctx, id, dbguest.StateStandby); err != nil {
		t.Fatal(err)
	}
	awaitSynchronous(ctx, t, primary, id)

	// The standby node's vmmd restarts; the new one adopts the standby.
	standby.abandon()
	restarted := metalNodeRuntime(t, env, "f", &offsetLeaser{offset: 5})
	standbyOwner = restarted
	restarted.SetReplicationDialer(func(gotID, node, target string) (ReplicationDialer, error) {
		if gotID != id || node != "db-e" || target != "10.42.0.5:9443" {
			return nil, fmt.Errorf("dialer for %s %s %s", gotID, node, target)
		}
		return dial, nil
	})
	if resp, err := restarted.Control(ctx, id, dbguest.Request{Op: dbguest.OpStatus}); err != nil || resp.State != dbguest.StateStandby {
		t.Fatalf("adopted standby = %+v, %v", resp, err)
	}
	// vmmd's exit cut the stream its relay carried: cut it here too. The
	// walreceiver reconnects through the adopted relay, and a synchronous
	// commit completes only once it has.
	if resp, err := primary.Control(ctx, id, dbguest.Request{Op: dbguest.OpAdminSQL,
		SQL: "SELECT pg_catalog.pg_terminate_backend(pid) FROM pg_catalog.pg_stat_replication"}); err != nil || resp.Error != "" {
		t.Fatalf("terminate the walsender: %+v %v", resp, err)
	}
	commitCtx, commitCancel := context.WithTimeout(ctx, 90*time.Second)
	defer commitCancel()
	started := time.Now()
	if _, err := conn.Exec(commitCtx, "insert into rows values ('after-restart')"); err != nil {
		t.Fatalf("synchronous commit after the standby's vmmd restarted: %v", err)
	}
	t.Logf("standby streaming again through the adopted relay; held commit took %v", time.Since(started).Round(time.Millisecond))
	if status := replicationStatus(ctx, t, primary, id, ""); !status.Streaming || !status.Synchronous {
		t.Fatalf("after the restart: %+v", status)
	}
}
