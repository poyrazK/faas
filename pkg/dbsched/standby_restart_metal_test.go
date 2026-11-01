//go:build metal && linux

// adr: 1027 — a standby restarts alone under a running primary: the
// primary never restarts, a commit made meanwhile waits for the standby's
// boot, and losing the primary's node right after loses nothing.
package dbsched

import (
	"context"
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/onebox-faas/faas/pkg/dbarchive"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

func TestMetalRestartStandbyUnderARunningPrimary(t *testing.T) {
	workdir, base, guestInit := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_BASE"), os.Getenv("FAAS_GUEST_INIT")
	if workdir == "" || base == "" && guestInit == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_BASE or FAAS_GUEST_INIT to run the scheduler metal test")
	}
	if base == "" {
		base = metalBase(context.Background(), t, workdir, guestInit, 17)
	}
	bucket := &dbarchive.FileRemote{Dir: filepath.Join(workdir, "srbucket")}
	_ = os.RemoveAll(bucket.Dir)
	t.Cleanup(func() { _ = os.RemoveAll(bucket.Dir) })
	a := newMetalNode(t, workdir, map[int]string{17: base}, "ta", 20<<30, &rangeLeaser{next: fcvm.MaxSlots - 160}, bucket)
	b := newMetalNode(t, workdir, map[int]string{17: base}, "tb", 10<<30, &rangeLeaser{next: fcvm.MaxSlots - 180}, bucket)
	store := NewMemStore()
	seedNodes(t, store, a.node, b.node)
	vmm := NewGRPCVMMWithDial(nil, func(ctx context.Context, target string, _ *tls.Config) (*grpc.ClientConn, error) {
		return grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	})
	t.Cleanup(func() { _ = vmm.Close() })
	s := New(store, vmm, Config{IdleTimeout: time.Hour, ReadyTimeout: time.Minute, SharedArchive: true, StandbySeedTimeout: 5 * time.Minute}, nil)
	t.Cleanup(s.standbyWG.Wait)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	id, password := uuid.NewString(), uuid.NewString()
	t.Cleanup(func() { _ = s.Delete(context.Background(), id) })
	if vm, err := s.Provision(ctx, ProvisionRequest{DatabaseID: id, Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30, RestoreWindowSeconds: 3600, Replicated: true}); err != nil || vm.NodeID != "ta" || vm.StandbyNode != "tb" {
		t.Fatalf("provision = %+v, %v; want primary on ta, standby on tb", vm, err)
	}
	for _, sql := range []string{"CREATE ROLE app LOGIN PASSWORD '" + password + "'", "CREATE DATABASE app OWNER app"} {
		if resp, err := s.Admin(ctx, id, sql); err != nil || resp.Error != "" {
			t.Fatalf("admin: %+v %v", resp, err)
		}
	}
	s.standbyWG.Wait()
	query(ctx, t, a.rt, id, password, "CREATE TABLE kept(v text); INSERT INTO kept SELECT 'bulk' FROM generate_series(1, 5000)")
	booted := query(ctx, t, a.rt, id, password, "SELECT pg_postmaster_start_time()::text")

	restarted := time.Now()
	if err := s.RestartStandby(ctx, id); err != nil {
		t.Fatal(err)
	}
	// Committed while the standby reboots: it waits for it.
	committed := time.Now()
	query(ctx, t, a.rt, id, password, "INSERT INTO kept VALUES ('during the standby restart')")
	wait := time.Since(committed)
	s.standbyWG.Wait()
	took := time.Since(restarted)
	if got, _ := store.Get(ctx, id); !got.StandbyRunning || !got.StandbySynced || got.NodeID != "ta" {
		t.Fatalf("after the standby restart: %+v", got)
	}
	if now := query(ctx, t, a.rt, id, password, "SELECT pg_postmaster_start_time()::text"); now != booted {
		t.Fatalf("the primary restarted (%s, was %s)", now, booted)
	}

	// ta dies: the commit made during the restart is on the standby.
	killNodeVM(t, "ta", id)
	lost := a.node
	lost.Lifecycle = NodeUnavailable
	seedNodes(t, store, lost)
	if n, err := s.SettleLostNodes(ctx); err != nil || n != 1 {
		t.Fatalf("settle = %d, %v", n, err)
	}
	if _, err := s.Wake(ctx, id); err != nil {
		t.Fatal(err)
	}
	if got := query(ctx, t, b.rt, id, password, "SELECT count(*)::text FROM kept"); got != "5001" {
		t.Fatalf("rows on tb after the failover = %s; want 5001", got)
	}
	t.Logf("standby restarted in %v under a running primary; a commit meanwhile waited %v and survived the primary's loss",
		took.Round(time.Millisecond), wait.Round(time.Millisecond))
}
