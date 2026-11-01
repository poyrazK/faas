//go:build metal && linux

// adr: 1027 — restore onto another node after node loss: a database lives
// on node A, A is lost with its archive disk, and the scheduler restores it
// onto node B from the bucket, to the end of what was archived.
package dbsched

import (
	"context"
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbarchive"
	"github.com/onebox-faas/faas/pkg/dbvm"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
)

// metalNode is one database node: a runtime, its dbbackupd (archive store
// plus the shared bucket) and its vmmd, all in process.
type metalNode struct {
	node       Node
	rt         *dbvm.Runtime
	archive    *dbarchive.LocalStore
	archiveDir string
	// offsite mirrors the archive when the node has a mirror.
	offsite *dbarchive.Offsite
	// sock is where vmmd serves; partition and heal stop and resume it
	// while the node's VMs keep running.
	sock string
	vmmd *vmmdgrpc.Server
	mu   sync.Mutex
	gsrv *grpc.Server
}

func (n *metalNode) serve(t *testing.T) {
	t.Helper()
	_ = os.Remove(n.sock)
	lis, err := net.Listen("unix", n.sock)
	if err != nil {
		t.Fatal(err)
	}
	gsrv := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(gsrv, n.vmmd)
	go func() { _ = gsrv.Serve(lis) }()
	n.mu.Lock()
	n.gsrv = gsrv
	n.mu.Unlock()
}

// partition makes the node's vmmd stop answering; its VMs run on.
func (n *metalNode) partition() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.gsrv.Stop()
}

// heal makes the node's vmmd answer again.
func (n *metalNode) heal(t *testing.T) { n.serve(t) }

func newMetalNode(t *testing.T, workdir string, bases map[int]string, name string, capacity int64, leases dbvm.Leaser, bucket dbarchive.Remote) *metalNode {
	t.Helper()
	return newMirroredMetalNode(t, workdir, bases, name, capacity, leases, bucket, nil)
}

// newMirroredMetalNode is a node whose dbbackupd also mirrors its archive
// to mirror (nil: none), under the prefix "dr".
func newMirroredMetalNode(t *testing.T, workdir string, bases map[int]string, name string, capacity int64, leases dbvm.Leaser, bucket, mirror dbarchive.Remote) *metalNode {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	dir, err := os.MkdirTemp(workdir, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	archiveDir := filepath.Join(dir, "archive")
	archive, err := dbarchive.NewLocalStore(archiveDir)
	if err != nil {
		t.Fatal(err)
	}
	// A floor this short lets the test prune right away.
	tiered := &dbarchive.TieredStore{LocalStore: archive, Remote: bucket, Prefix: "native", MinRetention: time.Millisecond}
	var offsite *dbarchive.Offsite
	if mirror != nil {
		offsite = dbarchive.NewOffsite(archive, bucket, "native", mirror, "dr")
		tiered.Offsite, tiered.OffsitePrefix = mirror, "dr"
	}
	archiveLn, err := net.Listen("unix", filepath.Join(dir, "a.sock"))
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		handoff := &dbarchive.Uploader{Store: archive, Remote: bucket, Prefix: "native"}
		_ = (&dbarchive.Server{Store: tiered, Backups: tiered, Retention: tiered, Handoff: handoff}).Serve(ctx, archiveLn)
	}()
	parent := "dbsched-metal-" + name
	if err := os.MkdirAll(filepath.Join("/sys/fs/cgroup", parent), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Join("/sys/fs/cgroup", parent)) })
	rt, err := dbvm.NewRuntime(dbvm.RuntimeConfig{
		JailBase: filepath.Join(workdir, name+"j"), VolumeRoot: filepath.Join(dir, "volumes"),
		KernelPath: "/srv/fc/kernel/1.7.0", Bases: bases, ParentCgroup: parent,
		JailerBin: "/usr/local/bin/jailer", FirecrackerBin: "/usr/local/bin/firecracker", Leases: leases,
		ArchiveSocket: filepath.Join(dir, "a.sock"),
		// Import tests trust their own source CA on loopback.
		ImportRoots: metalImportRoots, ImportAllowPrivate: metalImportRoots != nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Recover(); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "v.sock")
	// Standbys here reach their primaries' nodes over the same unix
	// sockets, without TLS.
	peers := func(_ context.Context, target, _ string) (*grpc.ClientConn, error) {
		return grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	n := &metalNode{rt: rt, archive: archive, archiveDir: archiveDir, sock: sock, offsite: offsite,
		vmmd: vmmdgrpc.New(nil, nil, "test", nil).WithDatabases(rt).WithDatabasePeers(peers),
		node: Node{ID: name, TargetURL: "unix://" + sock, AdmissionCeilingMB: 2048, VolumeCapacityBytes: capacity, Lifecycle: NodeActive}}
	n.serve(t)
	t.Cleanup(n.partition)
	return n
}

func TestMetalRestoreOntoAnotherNodeAfterNodeLoss(t *testing.T) {
	workdir, base := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_BASE")
	if workdir == "" || base == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_BASE to run the scheduler metal test")
	}
	bucket := &dbarchive.FileRemote{Dir: filepath.Join(workdir, "nlbucket")}
	_ = os.RemoveAll(bucket.Dir)
	t.Cleanup(func() { _ = os.RemoveAll(bucket.Dir) })
	leases := &topLeaser{}
	a := newMetalNode(t, workdir, map[int]string{17: base}, "na", 20<<30, leases, bucket)
	b := newMetalNode(t, workdir, map[int]string{17: base}, "nb", 10<<30, leases, bucket)
	store := NewMemStore()
	seedNodes(t, store, a.node, b.node)
	vmm := NewGRPCVMMWithDial(nil, func(ctx context.Context, target string, _ *tls.Config) (*grpc.ClientConn, error) {
		return grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	})
	t.Cleanup(func() { _ = vmm.Close() })
	s := New(store, vmm, Config{IdleTimeout: time.Minute, ReadyTimeout: time.Minute, SharedArchive: true}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	source, restored := uuid.NewString(), uuid.NewString()
	t.Cleanup(func() { _ = s.Delete(context.Background(), restored) })
	t.Cleanup(func() {
		_ = a.rt.Stop(context.Background(), source)
		_ = a.rt.DeleteVolume(context.Background(), source)
	})
	vm, err := s.Provision(ctx, ProvisionRequest{DatabaseID: source, Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30})
	if err != nil || vm.NodeID != "na" {
		t.Fatalf("provision = %+v, %v", vm, err)
	}
	password := uuid.NewString()
	for _, sql := range []string{"CREATE ROLE app LOGIN PASSWORD '" + password + "'", "CREATE DATABASE app OWNER app"} {
		if resp, err := s.Admin(ctx, source, sql); err != nil || resp.Error != "" {
			t.Fatalf("admin: %+v %v", resp, err)
		}
	}
	query(ctx, t, a.rt, source, password, "CREATE TABLE kept(v text); INSERT INTO kept VALUES ('before the loss')")
	// A second backup, so WAL before it is prunable.
	if !s.takeBackup(ctx, a.node, source) {
		t.Fatal("second base backup failed")
	}
	// Parking drains the archive; the uploader ships it off the node.
	if err := s.Park(ctx, source); err != nil {
		t.Fatal(err)
	}
	if _, err := (&dbarchive.Uploader{Store: a.archive, Remote: bucket, Prefix: "native"}).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	target := time.Now()

	// Node A is lost, archive disk and all.
	lostNode := a.node
	lostNode.Lifecycle = NodeUnavailable
	seedNodes(t, store, lostNode)
	if err := os.RemoveAll(filepath.Join(a.archiveDir, source)); err != nil {
		t.Fatal(err)
	}
	// Nothing prunes a lost node's archive but schedd's sweep, through B.
	before, err := bucket.List(ctx, "native/"+source+"/wal/")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.PruneLostArchives(ctx); err != nil || n != 1 {
		t.Fatalf("prune lost archives = %d, %v", n, err)
	}
	after, err := bucket.List(ctx, "native/"+source+"/wal/")
	if err != nil || len(after) >= len(before) {
		t.Fatalf("bucket WAL %d -> %d files, %v; want WAL before the newest backup pruned", len(before), len(after), err)
	}
	t.Logf("pruned the lost database's bucket WAL from %d to %d files", len(before), len(after))

	started := time.Now()
	vm, err = s.Restore(ctx, RestoreRequest{
		ProvisionRequest: ProvisionRequest{DatabaseID: restored, Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30, RestoreWindowSeconds: 3600},
		Source:           source, Target: target,
	})
	if err != nil || vm.NodeID != "nb" {
		t.Fatalf("restore = %+v, %v; want placement on nb", vm, err)
	}
	for vm.State == StateProvisioning {
		if time.Now().After(started.Add(5 * time.Minute)) {
			t.Fatal("restore never finished")
		}
		time.Sleep(200 * time.Millisecond)
		vm, _ = store.Get(ctx, restored)
	}
	took := time.Since(started)
	// The pruned archive starts at the newest backup, taken after the last
	// commit, so recovery replays no transaction and reports no point.
	if vm.State != StateParked || !vm.Bootstrapped || vm.RestoreRecoveredTo.After(target) {
		t.Fatalf("restored = %+v", vm)
	}
	if _, err := s.Wake(ctx, restored); err != nil {
		t.Fatal(err)
	}
	if got := query(ctx, t, b.rt, restored, password, "SELECT v FROM kept"); got != "before the loss" {
		t.Fatalf("data on node B = %q", got)
	}
	t.Logf("restored onto nb from the bucket in %v; recovered to %v (target %v)", took.Round(time.Millisecond),
		vm.RestoreRecoveredTo.Format(time.RFC3339Nano), target.UTC().Format(time.RFC3339Nano))

	// The lost original is deleted without its node: node B drops its
	// bucket archive.
	if err := s.Delete(ctx, source); err != nil {
		t.Fatalf("delete on the lost node: %v", err)
	}
	if keys, err := bucket.List(ctx, "native/"+source+"/"); err != nil || len(keys) != 0 {
		t.Fatalf("bucket after delete = %v, %v", keys, err)
	}
	if _, err := os.Stat(a.rt.VolumePath(source)); err != nil {
		t.Fatalf("the lost node's volume should still be there: %v", err)
	}
	// Node A comes back: its leftover volume is collected, the restored
	// database on B is untouched.
	seedNodes(t, store, a.node)
	if n, err := s.CollectOrphans(ctx); err != nil || n != 1 {
		t.Fatalf("collect orphans = %d, %v", n, err)
	}
	if _, err := os.Stat(a.rt.VolumePath(source)); !os.IsNotExist(err) {
		t.Fatalf("orphaned volume survived: %v", err)
	}
	if _, err := os.Stat(b.rt.VolumePath(restored)); err != nil {
		t.Fatalf("the restored database's volume was touched: %v", err)
	}
}
