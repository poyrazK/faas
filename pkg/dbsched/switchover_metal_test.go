//go:build metal && linux

// adr: 1027 — a planned switchover hands a running production-class
// database to its standby on another node in seconds: the primary stops
// cleanly, the standby is promoted, every commit is there, and the old
// primary is stopped.
package dbsched

import (
	"context"
	"crypto/tls"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/onebox-faas/faas/pkg/dbarchive"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

func TestMetalSwitchover(t *testing.T) {
	workdir, base, guestInit := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_BASE"), os.Getenv("FAAS_GUEST_INIT")
	if workdir == "" || base == "" && guestInit == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_BASE or FAAS_GUEST_INIT to run the scheduler metal test")
	}
	if base == "" {
		base = metalBase(context.Background(), t, workdir, guestInit, 17)
	}
	bucket := &dbarchive.FileRemote{Dir: filepath.Join(workdir, "swbucket")}
	_ = os.RemoveAll(bucket.Dir)
	t.Cleanup(func() { _ = os.RemoveAll(bucket.Dir) })
	a := newMetalNode(t, workdir, map[int]string{17: base}, "sa", 20<<30, &rangeLeaser{next: fcvm.MaxSlots - 120}, bucket)
	b := newMetalNode(t, workdir, map[int]string{17: base}, "sb", 10<<30, &rangeLeaser{next: fcvm.MaxSlots - 140}, bucket)
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
	if vm, err := s.Provision(ctx, ProvisionRequest{DatabaseID: id, Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30, RestoreWindowSeconds: 3600, Replicated: true}); err != nil || vm.NodeID != "sa" || vm.StandbyNode != "sb" {
		t.Fatalf("provision = %+v, %v; want primary on sa, standby on sb", vm, err)
	}
	for _, sql := range []string{"CREATE ROLE app LOGIN PASSWORD '" + password + "'", "CREATE DATABASE app OWNER app"} {
		if resp, err := s.Admin(ctx, id, sql); err != nil || resp.Error != "" {
			t.Fatalf("admin: %+v %v", resp, err)
		}
	}
	s.standbyWG.Wait()
	query(ctx, t, a.rt, id, password, "CREATE TABLE kept(v text); INSERT INTO kept SELECT 'bulk' FROM generate_series(1, 20000); INSERT INTO kept VALUES ('last before the switchover')")

	started := time.Now()
	if err := s.Switchover(ctx, id); err != nil {
		t.Fatal(err)
	}
	vm, err := s.Wake(ctx, id)
	if err != nil || vm.NodeID != "sb" || vm.State != StateRunning {
		t.Fatalf("after the switchover = %+v, %v; want running on sb", vm, err)
	}
	if got := query(ctx, t, b.rt, id, password, "SELECT count(*)::text FROM kept"); got != "20001" {
		t.Fatalf("rows on sb = %s; want every commit (20001)", got)
	}
	downtime := time.Since(started)
	if a.rt.Running(id) {
		t.Fatal("the old primary still runs")
	}
	query(ctx, t, b.rt, id, password, "INSERT INTO kept VALUES ('first after the switchover')")
	if wal := query(ctx, t, b.rt, id, password, "SELECT pg_walfile_name(pg_current_wal_lsn())"); !strings.HasPrefix(wal, "00000002") {
		t.Fatalf("the new primary writes WAL file %s; want timeline 2", wal)
	}
	t.Logf("switched over from sa to sb in %v (primary stop, promotion, first query); all 20001 rows there", downtime.Round(time.Millisecond))
}
