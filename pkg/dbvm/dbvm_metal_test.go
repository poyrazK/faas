//go:build metal && linux

// adr: 1027 — a database microVM bootstraps PostgreSQL on its persistent
// volume, parks with a clean shutdown, and cold-boots back with its data.
package dbvm

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/wire"
)

// defaultBaseRef is the official PostgreSQL 17 image, pinned to its
// linux/amd64 manifest because base staging rejects manifest lists.
const defaultBaseRef = "docker.io/library/postgres@sha256:66aafa11cf15800a3c94763f7e11d1e7b2e37e5e84bc4ce027cb6e1e4bfaf4df"

type metalEnv struct {
	workdir     string
	kernel      string
	base        string
	firecracker string
}

func loadMetalEnv(t *testing.T) metalEnv {
	t.Helper()
	workdir := os.Getenv("FAAS_DBVM_WORKDIR")
	guestInit := os.Getenv("FAAS_GUEST_INIT")
	if workdir == "" || guestInit == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_GUEST_INIT to run the database VM metal test")
	}
	env := metalEnv{
		workdir:     workdir,
		kernel:      envOr("FAAS_DBVM_KERNEL", "/srv/fc/kernel/1.7.0"),
		base:        filepath.Join(workdir, "base-postgres-17.ext4"),
		firecracker: envOr("FAAS_FIRECRACKER", "firecracker"),
	}
	if _, err := os.Stat(env.base); errors.Is(err, os.ErrNotExist) {
		buildMetalBase(t, envOr("FAAS_DBVM_BASE_REF", defaultBaseRef), guestInit, env.base)
	}
	return env
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// buildMetalBase stages the PostgreSQL image as a drive0 ext4 through the
// same rootfs builder imaged uses, with this tree's guest-init as /sbin/init.
func buildMetalBase(t *testing.T, ref, guestInit, out string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	client := oci.NewRegistryClient()
	manifest, err := client.PullManifest(ctx, ref)
	if err != nil {
		t.Fatalf("pull manifest %s: %v", ref, err)
	}
	parsed, err := oci.ParseReference(ref)
	if err != nil {
		t.Fatal(err)
	}
	readers := make([]io.Reader, 0, len(manifest.Layers))
	for _, layer := range manifest.Layers {
		body, err := client.PullBlob(ctx, parsed.Registry+"/"+parsed.Repository, layer.Digest)
		if err != nil {
			t.Fatalf("pull layer %s: %v", layer.Digest, err)
		}
		defer func() { _ = body.Close() }()
		readers = append(readers, body)
	}
	started := time.Now()
	res, err := rootfs.NewBuilder(wire.ExecRunner{}).BuildBase(ctx, rootfs.BaseBuildInput{Layers: readers, OutImage: out, GuestInitPath: guestInit})
	if err != nil {
		t.Fatalf("build base: %v", err)
	}
	t.Logf("built base %s: %d content bytes in %v", out, res.SizeBytes, time.Since(started).Round(time.Millisecond))
}

type runningVM struct {
	cmd     *exec.Cmd
	uds     string
	exited  chan error
	console *os.File
	// alive ends when Firecracker exits, so waits fail fast on a dead VM.
	alive context.Context
}

func bootDatabaseVM(ctx context.Context, t *testing.T, env metalEnv, volume, name string) (*runningVM, time.Time) {
	t.Helper()
	uds := filepath.Join(env.workdir, name+".vsock")
	_ = os.Remove(uds)
	cfg, err := BuildConfig(Spec{
		KernelPath: env.kernel, BasePath: env.base, VolumePath: volume, Major: 17,
		VcpuCount: 1, MemSizeMiB: 384, GuestCID: 0x7d000, VsockUDS: uds,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(env.workdir, name+".json")
	if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	console, err := os.Create(filepath.Join(env.workdir, name+".console.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(env.firecracker, "--no-api", "--config-file", cfgPath)
	cmd.Stdout, cmd.Stderr = console, console
	started := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start firecracker: %v", err)
	}
	alive, stopAlive := context.WithCancel(ctx)
	vm := &runningVM{cmd: cmd, uds: uds, exited: make(chan error, 1), console: console, alive: alive}
	go func() {
		err := cmd.Wait()
		stopAlive()
		vm.exited <- err
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = console.Close()
		_ = os.Remove(uds)
	})
	return vm, started
}

func (vm *runningVM) park(t *testing.T) time.Duration {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	started := time.Now()
	if _, err := Control(ctx, vm.uds, dbguest.Request{Op: dbguest.OpStop}); err != nil {
		t.Fatalf("stop: %v", err)
	}
	select {
	case <-vm.exited:
	case <-time.After(90 * time.Second):
		t.Fatal("database VM did not power off after stop")
	}
	return time.Since(started)
}

func connectVM(ctx context.Context, t *testing.T, uds, password string) *pgx.Conn {
	t.Helper()
	cfg, err := pgx.ParseConfig(fmt.Sprintf("host=dbvm user=postgres dbname=postgres sslmode=disable password=%s", password))
	if err != nil {
		t.Fatal(err)
	}
	routeToVM(cfg, uds)
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return conn
}

// routeToVM sends a pgx connection to the guest's vsock data port. The
// placeholder host is never resolved through DNS.
func routeToVM(cfg *pgx.ConnConfig, uds string) {
	cfg.LookupFunc = func(_ context.Context, host string) ([]string, error) { return []string{host}, nil }
	cfg.DialFunc = func(ctx context.Context, _, _ string) (net.Conn, error) { return Dial(ctx, uds, dbguest.DataPort) }
}

func TestMetalDatabaseVMBootstrapParkAndColdBoot(t *testing.T) {
	env := loadMetalEnv(t)
	volume := filepath.Join(env.workdir, "volume-"+t.Name()+".img")
	_ = os.Remove(volume)
	t.Cleanup(func() { _ = os.Remove(volume) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := CreateVolume(ctx, volume, 2<<30); err != nil {
		t.Fatal(err)
	}
	secret := make([]byte, 24)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	password := hex.EncodeToString(secret)

	vm, started := bootDatabaseVM(ctx, t, env, volume, "first")
	if _, err := WaitState(vm.alive, vm.uds, dbguest.StateNeedsBootstrap); err != nil {
		t.Fatalf("first boot: %v", err)
	}
	firstControl := time.Since(started)
	bootstrapStarted := time.Now()
	if resp, err := Control(ctx, vm.uds, dbguest.Request{Op: dbguest.OpBootstrap, SuperuserPassword: password}); err != nil || resp.Error != "" {
		t.Fatalf("bootstrap: %+v %v", resp, err)
	}
	if _, err := WaitState(vm.alive, vm.uds, dbguest.StateReady); err != nil {
		t.Fatal(err)
	}
	bootstrap := time.Since(bootstrapStarted)

	conn := connectVM(ctx, t, vm.uds, password)
	if _, err := conn.Exec(ctx, "create table parked(id int primary key, note text not null)"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "insert into parked values (1, 'survives park')"); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(ctx)
	if resp, err := Control(ctx, vm.uds, dbguest.Request{Op: dbguest.OpBootstrap, SuperuserPassword: password}); err != nil || resp.Error != dbguest.ErrCodeAlreadyInitialized {
		t.Fatalf("second bootstrap = %+v, %v; want %s", resp, err, dbguest.ErrCodeAlreadyInitialized)
	}
	park := vm.park(t)

	vm2, started2 := bootDatabaseVM(ctx, t, env, volume, "second")
	if _, err := WaitState(vm2.alive, vm2.uds, dbguest.StateReady); err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	coldBoot := time.Since(started2)
	conn2 := connectVM(ctx, t, vm2.uds, password)
	var note string
	if err := conn2.QueryRow(ctx, "select note from parked where id = 1").Scan(&note); err != nil || note != "survives park" {
		t.Fatalf("data after cold boot = %q, %v", note, err)
	}
	firstQuery := time.Since(started2)
	_ = conn2.Close(ctx)
	vm2.park(t)

	t.Logf("first boot to control %v; bootstrap (initdb+start) %v; park %v; cold boot to ready %v; cold boot to first query %v",
		firstControl.Round(time.Millisecond), bootstrap.Round(time.Millisecond), park.Round(time.Millisecond),
		coldBoot.Round(time.Millisecond), firstQuery.Round(time.Millisecond))
}

func TestMetalDatabaseVMRejectsWrongPassword(t *testing.T) {
	env := loadMetalEnv(t)
	volume := filepath.Join(env.workdir, "volume-"+t.Name()+".img")
	_ = os.Remove(volume)
	t.Cleanup(func() { _ = os.Remove(volume) })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := CreateVolume(ctx, volume, 1<<30); err != nil {
		t.Fatal(err)
	}
	vm, _ := bootDatabaseVM(ctx, t, env, volume, "auth")
	if _, err := WaitState(vm.alive, vm.uds, dbguest.StateNeedsBootstrap); err != nil {
		t.Fatal(err)
	}
	password := hex.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	if _, err := Control(ctx, vm.uds, dbguest.Request{Op: dbguest.OpBootstrap, SuperuserPassword: password}); err != nil {
		t.Fatal(err)
	}
	if _, err := WaitState(vm.alive, vm.uds, dbguest.StateReady); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig("host=dbvm user=postgres dbname=postgres sslmode=disable password=wrong-password")
	if err != nil {
		t.Fatal(err)
	}
	routeToVM(cfg, vm.uds)
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err == nil {
		_ = conn.Close(ctx)
		t.Fatal("wrong password accepted")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "28P01" {
		t.Fatalf("wrong password error = %v, want SQLSTATE 28P01 from the server", err)
	}
	vm.park(t)
}

// TestMetalRuntimeJailedLifecycle drives the vmmd-facing Runtime: jailed
// boot with its own chroot base and cgroup parent, bootstrap, park, cold
// boot, and complete cleanup of jail, cgroup and lease.
func TestMetalRuntimeJailedLifecycle(t *testing.T) {
	env := loadMetalEnv(t)
	parent := "dbvm-metal-test"
	parentDir := filepath.Join("/sys/fs/cgroup", parent)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatalf("cgroup parent: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(parentDir) })
	leases := &fakeLeaser{}
	rt, err := NewRuntime(RuntimeConfig{
		JailBase: filepath.Join(env.workdir, "dbjail"), VolumeRoot: filepath.Join(env.workdir, "volumes"),
		KernelPath: env.kernel, Bases: map[int]string{17: env.base}, ParentCgroup: parent,
		JailerBin: envOr("FAAS_JAILER", "jailer"), FirecrackerBin: env.firecracker, Leases: leases,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Recover(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	id := "8b1d4e2a-6c3f-4a5b-9d7e-1f2a3b4c5d6e"
	_ = rt.DeleteVolume(ctx, id)
	if err := rt.CreateVolume(ctx, id, 1<<30); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background(), id); _ = rt.DeleteVolume(context.Background(), id) })
	boot := BootRequest{ID: id, Major: 17, VcpuCount: 1, MemSizeMiB: 384}

	started := time.Now()
	if err := rt.Start(ctx, boot); err != nil {
		t.Fatalf("start: %v", err)
	}
	firstBoot := time.Since(started)
	assertJailedProcess(t, rt, id, parent)
	password := hex.EncodeToString([]byte("runtime-metal-test-superuser-pw!"))
	if resp, err := rt.Control(ctx, id, dbguest.Request{Op: dbguest.OpBootstrap, SuperuserPassword: password}); err != nil || resp.Error != "" {
		t.Fatalf("bootstrap: %+v %v", resp, err)
	}
	if _, err := rt.WaitState(ctx, id, dbguest.StateReady); err != nil {
		t.Fatal(err)
	}
	conn := connectRuntime(ctx, t, rt, id, password)
	if _, err := conn.Exec(ctx, "create table jailed(v text); insert into jailed values ('kept')"); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(ctx)

	parkStarted := time.Now()
	if err := rt.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
	park := time.Since(parkStarted)
	if _, err := os.Stat(rt.jailDir("db-" + id)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("jail dir survived stop: %v", err)
	}
	if _, err := os.Stat(filepath.Join(parentDir, "db-"+id)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cgroup survived stop: %v", err)
	}
	if len(leases.held) != 0 {
		t.Fatalf("lease survived stop: %v", leases.held)
	}

	wakeStarted := time.Now()
	if err := rt.Start(ctx, boot); err != nil {
		t.Fatalf("wake: %v", err)
	}
	if _, err := rt.WaitState(ctx, id, dbguest.StateReady); err != nil {
		t.Fatal(err)
	}
	wake := time.Since(wakeStarted)
	conn = connectRuntime(ctx, t, rt, id, password)
	var v string
	if err := conn.QueryRow(ctx, "select v from jailed").Scan(&v); err != nil || v != "kept" {
		t.Fatalf("data after wake = %q, %v", v, err)
	}
	_ = conn.Close(ctx)
	if err := rt.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
	t.Logf("jailed first boot to control %v; park %v; jailed wake to ready %v",
		firstBoot.Round(time.Millisecond), park.Round(time.Millisecond), wake.Round(time.Millisecond))
}

func connectRuntime(ctx context.Context, t *testing.T, rt *Runtime, id, password string) *pgx.Conn {
	t.Helper()
	cfg, err := pgx.ParseConfig("host=dbvm user=postgres dbname=postgres sslmode=disable password=" + password)
	if err != nil {
		t.Fatal(err)
	}
	cfg.LookupFunc = func(_ context.Context, host string) ([]string, error) { return []string{host}, nil }
	cfg.DialFunc = func(ctx context.Context, _, _ string) (net.Conn, error) { return rt.Dial(ctx, id) }
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return conn
}

// assertJailedProcess checks the VM runs as its jail uid inside its chroot
// with the billable memory ceiling on its own cgroup.
func assertJailedProcess(t *testing.T, rt *Runtime, id, parent string) {
	t.Helper()
	rt.mu.Lock()
	vm := rt.vms[id]
	rt.mu.Unlock()
	pid := vm.cmd.Process.Pid
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("Uid:\t%d\t", vm.lease.UID); !strings.Contains(string(status), want) {
		t.Fatalf("firecracker not running as jail uid %d:\n%s", vm.lease.UID, status)
	}
	if cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err != nil || !cmdlineHasJailID(cmdline, vm.jailID) {
		t.Fatalf("firecracker cmdline lacks --id %s: %q, %v", vm.jailID, cmdline, err)
	}
	if pids := jailProcesses(vm.jailID); len(pids) != 1 || pids[0] != pid {
		t.Fatalf("jail process scan = %v, want [%d]", pids, pid)
	}
	memMax, err := os.ReadFile(filepath.Join("/sys/fs/cgroup", parent, vm.jailID, "memory.max"))
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("%d", int64(api.BillableRAMMB(384))<<20); strings.TrimSpace(string(memMax)) != want {
		t.Fatalf("memory.max = %s, want %s", memMax, want)
	}
}
