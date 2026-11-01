//go:build metal && linux

// adr: 1027 — a production-class database woken after a clean park
// resumes its synchronized standby: the primary is synchronous before its
// first client, the first commit after the wake waits for the standby,
// and the primary's node dying right after that commit loses nothing.
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
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

func TestMetalWakeResumesTheSynchronizedStandby(t *testing.T) {
	workdir, base, guestInit := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_BASE"), os.Getenv("FAAS_GUEST_INIT")
	if workdir == "" || base == "" && guestInit == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_BASE or FAAS_GUEST_INIT to run the scheduler metal test")
	}
	if base == "" {
		base = metalBase(context.Background(), t, workdir, guestInit, 17)
	}
	bucket := &dbarchive.FileRemote{Dir: filepath.Join(workdir, "rsbucket")}
	_ = os.RemoveAll(bucket.Dir)
	t.Cleanup(func() { _ = os.RemoveAll(bucket.Dir) })
	a := newMetalNode(t, workdir, map[int]string{17: base}, "ra", 20<<30, &rangeLeaser{next: fcvm.MaxSlots - 80}, bucket)
	b := newMetalNode(t, workdir, map[int]string{17: base}, "rb", 10<<30, &rangeLeaser{next: fcvm.MaxSlots - 100}, bucket)
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
	if vm, err := s.Provision(ctx, ProvisionRequest{DatabaseID: id, Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30, RestoreWindowSeconds: 3600, Replicated: true}); err != nil || vm.NodeID != "ra" || vm.StandbyNode != "rb" {
		t.Fatalf("provision = %+v, %v; want primary on ra, standby on rb", vm, err)
	}
	for _, sql := range []string{"CREATE ROLE app LOGIN PASSWORD '" + password + "'", "CREATE DATABASE app OWNER app"} {
		if resp, err := s.Admin(ctx, id, sql); err != nil || resp.Error != "" {
			t.Fatalf("admin: %+v %v", resp, err)
		}
	}
	s.standbyWG.Wait()
	query(ctx, t, a.rt, id, password, "CREATE TABLE kept(v text); INSERT INTO kept SELECT 'bulk' FROM generate_series(1, 5000)")
	if err := s.Park(ctx, id); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Get(ctx, id); !got.StandbySynced || got.StandbyRunning {
		t.Fatalf("after a clean park: %+v", got)
	}

	// The wake returns with the primary synchronous and the standby still
	// trusted, before the standby is back.
	woke := time.Now()
	vm, err := s.Wake(ctx, id)
	wake := time.Since(woke)
	if err != nil || !vm.StandbySynced {
		t.Fatalf("wake = %+v, %v; want the standby still trusted", vm, err)
	}
	if status, err := vmm.Replication(ctx, a.node, id, "", ""); err != nil || status.Mode != dbguest.ReplicationSync {
		t.Fatalf("primary after the wake = %+v, %v; want synchronous before its first client", status, err)
	}
	// The first commit waits for the standby; once it returns, the
	// standby holds it.
	committed := time.Now()
	query(ctx, t, a.rt, id, password, "INSERT INTO kept VALUES ('first after the wake')")
	commit := time.Since(committed)

	// ra dies right after the commit was acknowledged.
	killNodeVM(t, "ra", id)
	lost := a.node
	lost.Lifecycle = NodeUnavailable
	seedNodes(t, store, lost)
	if n, err := s.SettleLostNodes(ctx); err != nil || n != 1 {
		t.Fatalf("settle = %d, %v; want the database failed over", n, err)
	}
	if _, err := s.Wake(ctx, id); err != nil {
		t.Fatal(err)
	}
	if got := query(ctx, t, b.rt, id, password, "SELECT count(*)::text FROM kept WHERE v = 'first after the wake'"); got != "1" {
		t.Fatalf("the commit acknowledged right after the wake is missing on rb (%s)", got)
	}
	t.Logf("wake %v (synchronous before its first client); first commit waited %v for the resumed standby; it survived the primary's loss",
		wake.Round(time.Millisecond), commit.Round(time.Millisecond))
}
