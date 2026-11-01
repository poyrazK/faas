//go:build metal && linux

// adr: 1027 — schedd's database scheduler provisions, wakes, parks (idle)
// and deletes a real jailed database VM through vmmd's gRPC handlers.
package dbsched

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbvm"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
)

type topLeaser struct {
	mu   sync.Mutex
	held map[string]fcvm.Lease
}

// Adopt is not exercised here: no VM outlives this test's runtime.
func (_ *topLeaser) Adopt(string, int) (fcvm.Lease, error) {
	return fcvm.Lease{}, errors.New("adopt: not used in this test")
}

func (l *topLeaser) Acquire(instance string) (fcvm.Lease, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held == nil {
		l.held = map[string]fcvm.Lease{}
	}
	slot := fcvm.MaxSlots - 40 - len(l.held)
	lease := fcvm.Lease{Instance: instance, Slot: slot, UID: fcvm.JailUIDBase + slot, GID: fcvm.JailUIDBase + slot}
	l.held[instance] = lease
	return lease, nil
}

func (l *topLeaser) Release(instance string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.held, instance)
	return nil
}

func TestMetalSchedulerLifecycle(t *testing.T) {
	workdir, base := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_BASE")
	if workdir == "" || base == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_BASE to run the scheduler metal test")
	}
	s, store, rt := metalScheduler(t, workdir, base)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	id := uuid.NewString()
	t.Cleanup(func() { _ = s.Delete(context.Background(), id) })

	started := time.Now()
	vm, err := s.Provision(ctx, ProvisionRequest{DatabaseID: id, Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30})
	if err != nil || vm.State != StateParked || !vm.Bootstrapped {
		t.Fatalf("provision = %+v, %v", vm, err)
	}
	provision := time.Since(started)

	password := uuid.NewString() + uuid.NewString()
	for _, sql := range []string{"CREATE ROLE app LOGIN PASSWORD '" + password + "'", "CREATE DATABASE app OWNER app"} {
		if resp, err := s.Admin(ctx, id, sql); err != nil || resp.Error != "" {
			t.Fatalf("admin: %+v %v", resp, err)
		}
	}
	query(ctx, t, rt, id, password, "CREATE TABLE kept(v text); INSERT INTO kept VALUES ('survives idle park')")

	// The idle sweep parks the database once it has been quiet long enough.
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := s.ParkIdle(ctx); err != nil {
			t.Fatal(err)
		}
		if got, _ := store.Get(ctx, id); got.State == StateParked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("idle database was never parked")
		}
		time.Sleep(500 * time.Millisecond)
	}
	if rt.Running(id) {
		t.Fatal("parked database VM is still running")
	}

	woke := time.Now()
	if vm, err := s.Wake(ctx, id); err != nil || vm.State != StateRunning {
		t.Fatalf("wake = %+v, %v", vm, err)
	}
	wake := time.Since(woke)
	if got := query(ctx, t, rt, id, password, "SELECT v FROM kept"); got != "survives idle park" {
		t.Fatalf("data after wake = %q", got)
	}
	if err := s.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("record survived delete: %v", err)
	}
	if _, err := os.Stat(rt.VolumePath(id)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("volume survived delete: %v", err)
	}
	t.Logf("provision (volume+boot+initdb+park) %v; scheduler wake to running %v", provision.Round(time.Millisecond), wake.Round(time.Millisecond))
}

func query(ctx context.Context, t *testing.T, rt *dbvm.Runtime, id, password, sql string) string {
	t.Helper()
	cfg, err := pgx.ParseConfig("host=dbvm user=app dbname=app sslmode=disable password=" + password)
	if err != nil {
		t.Fatal(err)
	}
	cfg.LookupFunc = func(_ context.Context, host string) ([]string, error) { return []string{host}, nil }
	cfg.DialFunc = func(ctx context.Context, _, _ string) (net.Conn, error) { return rt.Dial(ctx, id) }
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var out string
	if strings.HasPrefix(sql, "SELECT") {
		if err := conn.QueryRow(ctx, sql).Scan(&out); err != nil {
			t.Fatalf("query: %v", err)
		}
		return out
	}
	if _, err := conn.Exec(ctx, sql); err != nil {
		t.Fatalf("exec: %v", err)
	}
	return out
}

// metalScheduler runs a real database runtime behind an in-process vmmd
// gRPC server and returns a scheduler that reaches it through GRPCVMM.
func metalScheduler(t *testing.T, workdir, base string) (*Scheduler, *MemStore, *dbvm.Runtime) {
	t.Helper()
	parent := "dbsched-metal-test"
	if err := os.MkdirAll(filepath.Join("/sys/fs/cgroup", parent), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Join("/sys/fs/cgroup", parent)) })
	rt, err := dbvm.NewRuntime(dbvm.RuntimeConfig{
		JailBase: filepath.Join(workdir, "sjail"), VolumeRoot: filepath.Join(workdir, "schedvolumes"),
		KernelPath: "/srv/fc/kernel/1.7.0", Bases: map[int]string{17: base}, ParentCgroup: parent,
		JailerBin: "/usr/local/bin/jailer", FirecrackerBin: "/usr/local/bin/firecracker", Leases: &topLeaser{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Recover(); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(workdir, "dbsched-vmmd.sock")
	_ = os.Remove(sock)
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	gsrv := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(gsrv, vmmdgrpc.New(nil, nil, "test", nil).WithDatabases(rt))
	go func() { _ = gsrv.Serve(lis) }()
	t.Cleanup(gsrv.Stop)

	store := NewMemStore()
	seedNodes(t, store, Node{ID: "db-test", TargetURL: "unix://" + sock, AdmissionCeilingMB: 2048, VolumeCapacityBytes: 20 << 30, Lifecycle: NodeActive})
	vmm := NewGRPCVMMWithDial(nil, func(ctx context.Context, target string, _ *tls.Config) (*grpc.ClientConn, error) {
		return grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	})
	t.Cleanup(func() { _ = vmm.Close() })
	s := New(store, vmm, Config{IdleTimeout: 2 * time.Second, ReadyTimeout: time.Minute}, nil)
	return s, store, rt
}

// adr: 1027 — an always-on database is woken by the keep-warm sweep, never
// parked for idleness, and rebooted when its VM disappears behind the
// scheduler's back (a database node reboot). Back on scale-to-zero it parks.
func TestMetalAlwaysOnKeepWarmAndLostVM(t *testing.T) {
	workdir, base := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_BASE")
	if workdir == "" || base == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_BASE to run the scheduler metal test")
	}
	s, store, rt := metalScheduler(t, workdir, base)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	id := uuid.NewString()
	t.Cleanup(func() { _ = s.Delete(context.Background(), id) })
	if _, err := s.Provision(ctx, ProvisionRequest{DatabaseID: id, Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30, AlwaysOn: true}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.KeepWarm(ctx); err != nil || n != 1 || !rt.Running(id) {
		t.Fatalf("keep-warm after provision = %d, %v, running %v", n, err, rt.Running(id))
	}
	time.Sleep(3 * time.Second) // past the 2 s idle timeout
	if n, err := s.ParkIdle(ctx); err != nil || n != 0 {
		t.Fatalf("ParkIdle = %d, %v; an always-on database must stay up", n, err)
	}

	// The node "reboots": the VM stops without the scheduler knowing.
	if err := rt.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Get(ctx, id); got.State != StateRunning {
		t.Fatalf("record = %s before the sweep", got.State)
	}
	lost := time.Now()
	if n, err := s.KeepWarm(ctx); err != nil || n != 1 {
		t.Fatalf("keep-warm after VM loss = %d, %v", n, err)
	}
	recovered := time.Since(lost)
	if !rt.Running(id) {
		t.Fatal("lost VM was not rebooted")
	}
	if resp, err := s.Admin(ctx, id, "SELECT 1"); err != nil || resp.Error != "" {
		t.Fatalf("admin after recovery = %+v, %v", resp, err)
	}

	if _, err := s.SetAlwaysOn(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Second)
	if n, err := s.ParkIdle(ctx); err != nil || n != 1 || rt.Running(id) {
		t.Fatalf("back on scale-to-zero: ParkIdle = %d, %v, running %v", n, err, rt.Running(id))
	}
	t.Logf("lost VM detected and rebooted in %v", recovered.Round(time.Millisecond))
}

// adr: 1027 — resize a running database to a larger class and volume: it
// restarts on the new shape (shared_buffers follows guest RAM), its volume
// and ext4 filesystem grow, and its data survives. A class-only resize of a
// parked database applies at its next wake.
func TestMetalResize(t *testing.T) {
	workdir, base := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_BASE")
	if workdir == "" || base == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_BASE to run the scheduler metal test")
	}
	s, _, rt := metalScheduler(t, workdir, base)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	id := uuid.NewString()
	t.Cleanup(func() { _ = s.Delete(context.Background(), id) })
	if _, err := s.Provision(ctx, ProvisionRequest{DatabaseID: id, Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30}); err != nil {
		t.Fatal(err)
	}
	password := uuid.NewString() + uuid.NewString()
	for _, sql := range []string{"CREATE ROLE app LOGIN PASSWORD '" + password + "'", "CREATE DATABASE app OWNER app"} {
		if resp, err := s.Admin(ctx, id, sql); err != nil || resp.Error != "" {
			t.Fatalf("admin: %+v %v", resp, err)
		}
	}
	query(ctx, t, rt, id, password, "CREATE TABLE kept(v text); INSERT INTO kept VALUES ('survives the resize')")
	small := query(ctx, t, rt, id, password, "SELECT current_setting('shared_buffers')")

	started := time.Now()
	vm, err := s.Resize(ctx, id, Shape{VcpuCount: 2, MemMiB: 1024, VolumeBytes: 2 << 30})
	took := time.Since(started)
	if err != nil || vm.State != StateRunning || vm.ResizePending {
		t.Fatalf("resize = %+v, %v", vm, err)
	}
	large := query(ctx, t, rt, id, password, "SELECT current_setting('shared_buffers')")
	if large == small {
		t.Fatalf("shared_buffers %s before and after: the VM did not restart on the new shape", small)
	}
	if got := query(ctx, t, rt, id, password, "SELECT v FROM kept"); got != "survives the resize" {
		t.Fatalf("data after resize = %q", got)
	}
	if err := s.Park(ctx, id); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(rt.VolumePath(id)); err != nil || info.Size() != 2<<30 {
		t.Fatalf("volume file = %v, %v", info, err)
	}
	super, err := exec.CommandContext(ctx, "dumpe2fs", "-h", rt.VolumePath(id)).Output()
	if err != nil {
		t.Fatal(err)
	}
	if fs, err := dbvm.FilesystemBytes(super); err != nil || fs != 2<<30 {
		t.Fatalf("filesystem = %d bytes, %v; want the grown 2 GiB", fs, err)
	}

	// Back to the small class while parked: no boot until the next wake.
	vm, err = s.Resize(ctx, id, Shape{VcpuCount: 1, MemMiB: 384, VolumeBytes: 2 << 30})
	if err != nil || vm.State != StateParked || vm.ResizePending || rt.Running(id) {
		t.Fatalf("parked resize = %+v, %v (running %v)", vm, err, rt.Running(id))
	}
	if _, err := s.Wake(ctx, id); err != nil {
		t.Fatal(err)
	}
	if got := query(ctx, t, rt, id, password, "SELECT current_setting('shared_buffers')"); got != small {
		t.Fatalf("shared_buffers after resizing back = %s, want %s", got, small)
	}
	t.Logf("running resize (stop, grow 1->2 GiB, boot) %v; shared_buffers %s -> %s", took.Round(time.Millisecond), small, large)
}
