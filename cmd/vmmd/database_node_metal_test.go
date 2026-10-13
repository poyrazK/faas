//go:build metal && linux

// adr: 1027 — vmmd in database-node mode: builds the PostgreSQL base from a
// digest-pinned image on the node, serves only the database RPCs, and runs
// a database through its lifecycle. It runs in-process with a top-of-range
// jail leaser because the metal host also runs a compute vmmd.
package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
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
	"github.com/onebox-faas/faas/pkg/wire"
)

type nodeTestLeaser struct {
	mu   sync.Mutex
	held map[string]fcvm.Lease
}

func (l *nodeTestLeaser) Acquire(instance string) (fcvm.Lease, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held == nil {
		l.held = map[string]fcvm.Lease{}
	}
	slot := fcvm.MaxSlots - 100 - len(l.held)
	lease := fcvm.Lease{Instance: instance, Slot: slot, UID: fcvm.JailUIDBase + slot, GID: fcvm.JailUIDBase + slot}
	l.held[instance] = lease
	return lease, nil
}

func (l *nodeTestLeaser) Release(instance string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.held, instance)
	return nil
}

func TestMetalDatabaseNodeMode(t *testing.T) {
	workdir, guestInit := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_GUEST_INIT")
	if workdir == "" || guestInit == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_GUEST_INIT to run the database node metal test")
	}
	image := os.Getenv("FAAS_DBVM_BASE_REF")
	if image == "" {
		image = "docker.io/library/postgres@sha256:66aafa11cf15800a3c94763f7e11d1e7b2e37e5e84bc4ce027cb6e1e4bfaf4df"
	}
	parent := "dbnode-metal-test"
	if err := os.MkdirAll(filepath.Join("/sys/fs/cgroup", parent), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Join("/sys/fs/cgroup", parent)) })
	baseDir := filepath.Join(workdir, "nodebase")
	sock := filepath.Join(workdir, "dbnode-vmmd.sock")
	_ = os.Remove(sock)
	metricsLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	metricsAddr := metricsLn.Addr().String()
	_ = metricsLn.Close()
	cfg := &Config{
		Role:        "database",
		ListenAddr:  "unix://" + sock,
		MetricsAddr: metricsAddr,
		DatabaseVMs: &DatabaseVMConfig{
			KernelPath:   envOr("FAAS_DBVM_KERNEL", "/srv/fc/kernel/1.7.0"),
			JailBase:     filepath.Join(workdir, "nodejail"),
			VolumeRoot:   filepath.Join(workdir, "nodevolumes"),
			BaseDir:      baseDir,
			GuestInit:    guestInit,
			ParentCgroup: parent,
			Images:       map[string]string{"17": image},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runDatabaseNode(ctx, cfg, wire.NewOpsMetrics("vmmd"), slog.Default(), runDeps{databaseLeases: &nodeTestLeaser{}})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("runDatabaseNode = %v", err)
			}
		case <-time.After(30 * time.Second):
			t.Error("runDatabaseNode did not return after cancel")
		}
	})

	// /readyz waits for the base, built from the pinned image on the node.
	started := time.Now()
	deadline := time.Now().Add(10 * time.Minute)
	for {
		resp, err := http.Get("http://" + metricsAddr + "/readyz") //nolint:noctx // test-only loopback probe
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("database node never became ready")
		}
		time.Sleep(2 * time.Second)
	}
	readyAfter := time.Since(started)
	digest, err := dbvm.FileDigest(guestInit)
	if err != nil {
		t.Fatal(err)
	}
	base := dbvm.BasePath(baseDir, 17, image, digest)
	if info, err := os.Stat(base); err != nil || info.Mode().Perm() != 0o444 {
		t.Fatalf("base %s: %v %v", base, info, err)
	}

	conn, err := grpc.NewClient("unix://"+sock, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := vmmdpb.NewVmmdClient(conn)
	rpcCtx, rpcCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer rpcCancel()
	if _, err := client.CreateColdBoot(rpcCtx, &vmmdpb.CreateColdBootRequest{}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("compute RPC on a database node = %v, want Unimplemented", err)
	}

	id := "4d0f6c8b-2e3a-4f7b-9c1d-0e2f3a4b5c6d"
	t.Cleanup(func() {
		_, _ = client.StopDatabase(context.Background(), &vmmdpb.StopDatabaseRequest{DatabaseId: id})
		_, _ = client.DeleteDatabaseVolume(context.Background(), &vmmdpb.DeleteDatabaseVolumeRequest{DatabaseId: id})
	})
	if _, err := client.CreateDatabaseVolume(rpcCtx, &vmmdpb.CreateDatabaseVolumeRequest{DatabaseId: id, SizeBytes: 1 << 30}); err != nil {
		t.Fatal(err)
	}
	start := &vmmdpb.StartDatabaseRequest{DatabaseId: id, PostgresMajor: 17, VcpuCount: 1, MemSizeMib: 384, VolumeOpsPerSec: 5000}
	if resp, err := client.StartDatabase(rpcCtx, start); err != nil || resp.GetState() != string(dbguest.StateNeedsBootstrap) {
		t.Fatalf("start = %v, %v", resp, err)
	}
	if c, err := client.DatabaseControl(rpcCtx, &vmmdpb.DatabaseControlRequest{DatabaseId: id, Op: "bootstrap"}); err != nil || c.GetErrorCode() != "" {
		t.Fatalf("bootstrap = %v, %v", c, err)
	}
	for {
		c, err := client.DatabaseControl(rpcCtx, &vmmdpb.DatabaseControlRequest{DatabaseId: id, Op: "status"})
		if err == nil && c.GetState() == string(dbguest.StateReady) {
			break
		}
		if rpcCtx.Err() != nil {
			t.Fatalf("database never ready: %v %v", c, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if c, err := client.DatabaseControl(rpcCtx, &vmmdpb.DatabaseControlRequest{DatabaseId: id, Op: "admin_sql", Sql: "SELECT 1"}); err != nil || c.GetErrorCode() != "" {
		t.Fatalf("admin_sql = %v, %v", c, err)
	}
	if _, err := client.StopDatabase(rpcCtx, &vmmdpb.StopDatabaseRequest{DatabaseId: id}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.DeleteDatabaseVolume(rpcCtx, &vmmdpb.DeleteDatabaseVolumeRequest{DatabaseId: id}); err != nil {
		t.Fatal(err)
	}
	t.Logf("ready (base built or reused) after %v; base %s", readyAfter.Round(time.Millisecond), filepath.Base(base))
}
