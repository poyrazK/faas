//go:build metal && linux

// adr: 1027 — vmmd's database RPCs create a volume, cold-boot a jailed
// database VM, relay bootstrap, park it, wake it and delete the volume.
package vmmdgrpc

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbvm"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

// topRangeLeaser hands out jail uids from the top of the range so the test
// never collides with a vmmd running on the same host.
type topRangeLeaser struct {
	mu   sync.Mutex
	held map[string]fcvm.Lease
}

func (l *topRangeLeaser) Acquire(instance string) (fcvm.Lease, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held == nil {
		l.held = map[string]fcvm.Lease{}
	}
	if _, ok := l.held[instance]; ok {
		return fcvm.Lease{}, errors.New("already leased")
	}
	slot := fcvm.MaxSlots - 20 - len(l.held)
	lease := fcvm.Lease{Instance: instance, Slot: slot, UID: fcvm.JailUIDBase + slot, GID: fcvm.JailUIDBase + slot}
	l.held[instance] = lease
	return lease, nil
}

func (l *topRangeLeaser) Release(instance string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.held, instance)
	return nil
}

func TestMetalDatabaseRPCLifecycle(t *testing.T) {
	workdir, base := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_BASE")
	if workdir == "" || base == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_BASE to run the database RPC metal test")
	}
	kernel := os.Getenv("FAAS_DBVM_KERNEL")
	if kernel == "" {
		kernel = "/srv/fc/kernel/1.7.0"
	}
	parent := "dbvm-rpc-metal-test"
	if err := os.MkdirAll(filepath.Join("/sys/fs/cgroup", parent), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Join("/sys/fs/cgroup", parent)) })
	rt, err := dbvm.NewRuntime(dbvm.RuntimeConfig{
		JailBase: filepath.Join(workdir, "rpcjail"), VolumeRoot: filepath.Join(workdir, "rpcvolumes"),
		KernelPath: kernel, Bases: map[int]string{17: base}, ParentCgroup: parent,
		JailerBin: "/usr/local/bin/jailer", FirecrackerBin: "/usr/local/bin/firecracker", Leases: &topRangeLeaser{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Recover(); err != nil {
		t.Fatal(err)
	}
	client := serveDatabaseRPCs(t, New(vmmStubBase{}, nil, "test", nil).WithDatabases(rt), workdir)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	id := "3c9e5b7a-1d2f-4e6a-8b0c-9d1e2f3a4b5c"
	_, _ = client.StopDatabase(ctx, &vmmdpb.StopDatabaseRequest{DatabaseId: id})
	_, _ = client.DeleteDatabaseVolume(ctx, &vmmdpb.DeleteDatabaseVolumeRequest{DatabaseId: id})
	t.Cleanup(func() {
		_, _ = client.StopDatabase(context.Background(), &vmmdpb.StopDatabaseRequest{DatabaseId: id})
		_, _ = client.DeleteDatabaseVolume(context.Background(), &vmmdpb.DeleteDatabaseVolumeRequest{DatabaseId: id})
	})
	if _, err := client.CreateDatabaseVolume(ctx, &vmmdpb.CreateDatabaseVolumeRequest{DatabaseId: id, SizeBytes: 1 << 30}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateDatabaseVolume(ctx, &vmmdpb.CreateDatabaseVolumeRequest{DatabaseId: id, SizeBytes: 1 << 30}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("second create = %v, want AlreadyExists", err)
	}
	start := &vmmdpb.StartDatabaseRequest{DatabaseId: id, PostgresMajor: 17, VcpuCount: 1, MemSizeMib: 384, VolumeOpsPerSec: 5000}
	started := time.Now()
	resp, err := client.StartDatabase(ctx, start)
	if err != nil || resp.GetState() != string(dbguest.StateNeedsBootstrap) {
		t.Fatalf("first start = %v, %v", resp, err)
	}
	firstBoot := time.Since(started)
	if _, err := client.StartDatabase(ctx, start); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("double start = %v, want FailedPrecondition", err)
	}
	if _, err := client.DeleteDatabaseVolume(ctx, &vmmdpb.DeleteDatabaseVolumeRequest{DatabaseId: id}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("delete while running = %v, want FailedPrecondition", err)
	}
	if c, err := client.DatabaseControl(ctx, &vmmdpb.DatabaseControlRequest{DatabaseId: id, Op: "bootstrap"}); err != nil || c.GetErrorCode() != "" {
		t.Fatalf("bootstrap = %v, %v", c, err)
	}
	waitRPCReady(ctx, t, client, id)
	if c, err := client.DatabaseControl(ctx, &vmmdpb.DatabaseControlRequest{DatabaseId: id, Op: "admin_sql", Sql: "CREATE ROLE app LOGIN PASSWORD '" + strings.Repeat("rpc-metal-", 4) + "'"}); err != nil || c.GetErrorCode() != "" {
		t.Fatalf("admin_sql = %v, %v", c, err)
	}
	if _, err := client.StopDatabase(ctx, &vmmdpb.StopDatabaseRequest{DatabaseId: id}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.StopDatabase(ctx, &vmmdpb.StopDatabaseRequest{DatabaseId: id}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("stop when parked = %v, want FailedPrecondition", err)
	}

	woke := time.Now()
	if _, err := client.StartDatabase(ctx, start); err != nil {
		t.Fatalf("wake: %v", err)
	}
	waitRPCReady(ctx, t, client, id)
	wake := time.Since(woke)
	if c, err := client.DatabaseControl(ctx, &vmmdpb.DatabaseControlRequest{DatabaseId: id, Op: "bootstrap"}); err != nil || c.GetErrorCode() != dbguest.ErrCodeAlreadyInitialized {
		t.Fatalf("bootstrap after wake = %v, %v; want %s", c, err, dbguest.ErrCodeAlreadyInitialized)
	}
	if _, err := client.StopDatabase(ctx, &vmmdpb.StopDatabaseRequest{DatabaseId: id}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.DeleteDatabaseVolume(ctx, &vmmdpb.DeleteDatabaseVolumeRequest{DatabaseId: id}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(rt.VolumePath(id)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("volume survived delete: %v", err)
	}
	t.Logf("rpc first start %v; rpc wake to ready %v", firstBoot.Round(time.Millisecond), wake.Round(time.Millisecond))
}

func serveDatabaseRPCs(t *testing.T, s *Server, dir string) vmmdpb.VmmdClient {
	t.Helper()
	sock := filepath.Join(dir, "vmmd-db-test.sock")
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	gsrv := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(gsrv, s)
	go func() { _ = gsrv.Serve(ln) }()
	t.Cleanup(gsrv.Stop)
	conn, err := grpc.NewClient("unix://"+sock, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return vmmdpb.NewVmmdClient(conn)
}

func waitRPCReady(ctx context.Context, t *testing.T, client vmmdpb.VmmdClient, id string) {
	t.Helper()
	for {
		c, err := client.DatabaseControl(ctx, &vmmdpb.DatabaseControlRequest{DatabaseId: id, Op: "status"})
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		switch c.GetState() {
		case string(dbguest.StateReady):
			return
		case string(dbguest.StateFailed):
			t.Fatalf("database failed: %s", c.GetErrorCode())
		}
		select {
		case <-ctx.Done():
			t.Fatalf("not ready: %s", c.GetState())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
