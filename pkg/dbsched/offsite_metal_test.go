//go:build metal && linux

// adr: 1027 — with the source's node and the bucket both gone, a
// point-in-time restore onto another node recovers from the off-site
// mirror alone.
package dbsched

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/onebox-faas/faas/pkg/dbarchive"
)

// goneBucket is a bucket whose account is gone: every call fails.
type goneBucket struct{}

var errGoneBucket = errors.New("bucket gone")

func (goneBucket) Put(context.Context, string, io.ReadSeeker, int64) error { return errGoneBucket }
func (goneBucket) Get(context.Context, string, io.Writer) error            { return errGoneBucket }
func (goneBucket) Delete(context.Context, string) error                    { return errGoneBucket }
func (goneBucket) List(context.Context, string) ([]string, error)          { return nil, errGoneBucket }
func (goneBucket) Sizes(context.Context, string) (map[string]int64, error) {
	return nil, errGoneBucket
}

func TestMetalRestoreFromTheMirrorAfterLosingTheBucket(t *testing.T) {
	workdir, base, guestInit := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_BASE"), os.Getenv("FAAS_GUEST_INIT")
	if workdir == "" || base == "" && guestInit == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_BASE or FAAS_GUEST_INIT to run the scheduler metal test")
	}
	if base == "" {
		base = metalBase(context.Background(), t, workdir, guestInit, 17)
	}
	bucket, mirror := &dbarchive.FileRemote{Dir: filepath.Join(workdir, "mrbucket")}, &dbarchive.FileRemote{Dir: filepath.Join(workdir, "mrmirror")}
	for _, dir := range []string{bucket.Dir, mirror.Dir} {
		_ = os.RemoveAll(dir)
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
	}
	leases := &topLeaser{}
	a := newMirroredMetalNode(t, workdir, map[int]string{17: base}, "ma", 20<<30, leases, bucket, mirror)
	// Node B's bucket is gone with its account; it still reads the mirror.
	b := newMirroredMetalNode(t, workdir, map[int]string{17: base}, "mb", 10<<30, leases, goneBucket{}, mirror)
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
	t.Cleanup(func() {
		cleanup := context.Background()
		_ = b.rt.Stop(cleanup, restored)
		_ = b.rt.DeleteVolume(cleanup, restored)
		_ = a.rt.Stop(cleanup, source)
		_ = a.rt.DeleteVolume(cleanup, source)
	})
	if vm, err := s.Provision(ctx, ProvisionRequest{DatabaseID: source, Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30}); err != nil || vm.NodeID != "ma" {
		t.Fatalf("provision = %+v, %v", vm, err)
	}
	password := uuid.NewString()
	for _, sql := range []string{"CREATE ROLE app LOGIN PASSWORD '" + password + "'", "CREATE DATABASE app OWNER app"} {
		if resp, err := s.Admin(ctx, source, sql); err != nil || resp.Error != "" {
			t.Fatalf("admin: %+v %v", resp, err)
		}
	}
	query(ctx, t, a.rt, source, password, "CREATE TABLE kept(v text); INSERT INTO kept SELECT 'row ' || g FROM generate_series(1, 3000) g")
	// Parking drains the archive; both uploaders ship it off the node.
	if err := s.Park(ctx, source); err != nil {
		t.Fatal(err)
	}
	if _, err := (&dbarchive.Uploader{Store: a.archive, Remote: bucket, Prefix: "native"}).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.offsite.RunOnce(ctx); err != nil {
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

	started := time.Now()
	vm, err := s.Restore(ctx, RestoreRequest{
		ProvisionRequest: ProvisionRequest{DatabaseID: restored, Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30, RestoreWindowSeconds: 3600},
		Source:           source, Target: target,
	})
	if err != nil || vm.NodeID != "mb" {
		t.Fatalf("restore = %+v, %v; want placement on mb", vm, err)
	}
	s.restoreWG.Wait()
	if vm, err = store.Get(ctx, restored); err != nil || vm.State != StateParked || !vm.Bootstrapped {
		t.Fatalf("restored = %+v, %v", vm, err)
	}
	took := time.Since(started)
	if _, err := s.Wake(ctx, restored); err != nil {
		t.Fatal(err)
	}
	if n := query(ctx, t, b.rt, restored, password, "SELECT count(*)::text FROM kept"); n != "3000" {
		t.Fatalf("rows restored from the mirror = %s", n)
	}
	t.Logf("restored onto mb from the off-site mirror alone in %v", took.Round(time.Millisecond))
}
