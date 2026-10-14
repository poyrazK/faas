//go:build metal && linux

// adr: 1027 — a database moves between two database nodes: its parked
// volume is copied, it runs on the new node with its data, the old node's
// volume and archive copy are gone, and the archive continues across the
// move, so a restore to after the move sees writes from both nodes.
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
)

func TestMetalMoveDatabase(t *testing.T) {
	workdir, base := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_BASE")
	if workdir == "" || base == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_BASE to run the scheduler metal test")
	}
	bucket := &dbarchive.FileRemote{Dir: filepath.Join(workdir, "mvbucket")}
	_ = os.RemoveAll(bucket.Dir)
	t.Cleanup(func() { _ = os.RemoveAll(bucket.Dir) })
	leases := &topLeaser{}
	a := newMetalNode(t, workdir, base, "ma", 20<<30, leases, bucket)
	b := newMetalNode(t, workdir, base, "mb", 10<<30, leases, bucket)
	store := NewMemStore()
	seedNodes(t, store, a.node, b.node)
	vmm := NewGRPCVMMWithDial(nil, func(ctx context.Context, target string, _ *tls.Config) (*grpc.ClientConn, error) {
		return grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	})
	t.Cleanup(func() { _ = vmm.Close() })
	s := New(store, vmm, Config{IdleTimeout: time.Hour, ReadyTimeout: time.Minute, SharedArchive: true}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	id, restored := uuid.NewString(), uuid.NewString()
	t.Cleanup(func() {
		_ = s.Delete(context.Background(), restored)
		_ = s.Delete(context.Background(), id)
	})
	vm, err := s.Provision(ctx, ProvisionRequest{DatabaseID: id, Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30, RestoreWindowSeconds: 3600})
	if err != nil || vm.NodeID != "ma" {
		t.Fatalf("provision = %+v, %v", vm, err)
	}
	password := uuid.NewString()
	for _, sql := range []string{"CREATE ROLE app LOGIN PASSWORD '" + password + "'", "CREATE DATABASE app OWNER app"} {
		if resp, err := s.Admin(ctx, id, sql); err != nil || resp.Error != "" {
			t.Fatalf("admin: %+v %v", resp, err)
		}
	}
	query(ctx, t, a.rt, id, password, "CREATE TABLE kept(v text); INSERT INTO kept SELECT 'before the move' FROM generate_series(1, 50000)")

	// The database is running on ma; the move parks it, copies the volume,
	// hands the archive to the bucket and wakes it on mb.
	started := time.Now()
	moved, err := s.Move(ctx, id, "mb")
	took := time.Since(started)
	if err != nil || moved.NodeID != "mb" || moved.State != StateRunning {
		t.Fatalf("move = %+v, %v; want running on mb", moved, err)
	}
	if a.rt.Running(id) || !b.rt.Running(id) {
		t.Fatalf("running on ma=%v mb=%v", a.rt.Running(id), b.rt.Running(id))
	}
	if got := query(ctx, t, b.rt, id, password, "SELECT count(*)::text FROM kept WHERE v = 'before the move'"); got != "50000" {
		t.Fatalf("rows on mb = %s", got)
	}
	if _, err := os.Stat(a.rt.VolumePath(id)); !os.IsNotExist(err) {
		t.Fatalf("the old node kept the volume: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a.archiveDir, id)); !os.IsNotExist(err) {
		t.Fatalf("the old node kept its archive copy: %v", err)
	}
	moveKeys, err := bucket.List(ctx, "native/"+id+"/")
	if err != nil || len(moveKeys) == 0 {
		t.Fatalf("bucket after the move = %d keys, %v", len(moveKeys), err)
	}
	t.Logf("moved a running database ma -> mb in %v (park, copy, archive handoff, wake); %d archive objects in the bucket", took.Round(time.Millisecond), len(moveKeys))

	// The archive continues on mb: a restore to after a write there sees
	// both nodes' rows.
	query(ctx, t, b.rt, id, password, "INSERT INTO kept VALUES ('after the move')")
	time.Sleep(10 * time.Millisecond)
	target := time.Now()
	if _, err := (&dbarchive.Uploader{Store: b.archive, Remote: bucket, Prefix: "native"}).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	vm, err = s.Restore(ctx, RestoreRequest{
		ProvisionRequest: ProvisionRequest{DatabaseID: restored, Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30, RestoreWindowSeconds: 3600},
		Source:           id, Target: target,
	})
	if err != nil {
		t.Fatal(err)
	}
	for vm.State == StateProvisioning {
		if time.Since(began) > 5*time.Minute {
			t.Fatal("restore never finished")
		}
		time.Sleep(200 * time.Millisecond)
		vm, _ = store.Get(ctx, restored)
	}
	if vm.State != StateParked {
		t.Fatalf("restore = %+v", vm)
	}
	if _, err := s.Wake(ctx, restored); err != nil {
		t.Fatal(err)
	}
	node := a.rt
	if vm.NodeID == "mb" {
		node = b.rt
	}
	if got := query(ctx, t, node, restored, password, "SELECT count(*)::text FROM kept"); got != "50001" {
		t.Fatalf("restore across the move has %s rows, want 50001", got)
	}
	t.Logf("restored across the move onto %s in %v", vm.NodeID, time.Since(began).Round(time.Millisecond))
}
