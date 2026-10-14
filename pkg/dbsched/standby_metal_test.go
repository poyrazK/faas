//go:build metal && linux

// adr: 1027 — production class end to end through vmmd's gRPC handlers:
// two replicated databases get synchronous standbys on a second node; one
// keeps running, one parks. The first node dies with no clean shutdown.
// The running database's standby is promoted at once, the parked one's on
// its next boot, and neither loses an acknowledged commit.
package dbsched

import (
	"context"
	"crypto/tls"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/onebox-faas/faas/pkg/dbarchive"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

// rangeLeaser hands out jail identities from its own range, never reusing
// one: two nodes' allocators, sharing one host.
type rangeLeaser struct {
	mu   sync.Mutex
	next int
	held map[string]fcvm.Lease
}

func (l *rangeLeaser) Acquire(instance string) (fcvm.Lease, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held == nil {
		l.held = map[string]fcvm.Lease{}
	}
	slot := l.next
	l.next--
	lease := fcvm.Lease{Instance: instance, Slot: slot, UID: fcvm.JailUIDBase + slot, GID: fcvm.JailUIDBase + slot}
	l.held[instance] = lease
	return lease, nil
}

func (l *rangeLeaser) Release(instance string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.held, instance)
	return nil
}

// killNodeVM kills a database's VM on one test node the way a lost node
// does: SIGKILL to the Firecracker in its jail cgroup, no shutdown.
func killNodeVM(t *testing.T, nodeName, id string) {
	t.Helper()
	procs, err := os.ReadFile(filepath.Join("/sys/fs/cgroup", "dbsched-metal-"+nodeName, "db-"+id, "cgroup.procs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range strings.Fields(string(procs)) {
		pid, err := strconv.Atoi(field)
		if err == nil {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

func TestMetalProductionClassFailover(t *testing.T) {
	workdir, base := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_BASE")
	if workdir == "" || base == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_BASE to run the scheduler metal test")
	}
	bucket := &dbarchive.FileRemote{Dir: filepath.Join(workdir, "pcbucket")}
	_ = os.RemoveAll(bucket.Dir)
	t.Cleanup(func() { _ = os.RemoveAll(bucket.Dir) })
	a := newMetalNode(t, workdir, map[int]string{17: base}, "pa", 20<<30, &rangeLeaser{next: fcvm.MaxSlots - 40}, bucket)
	b := newMetalNode(t, workdir, map[int]string{17: base}, "pb", 10<<30, &rangeLeaser{next: fcvm.MaxSlots - 60}, bucket)
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

	password := uuid.NewString()
	running, parked := uuid.NewString(), uuid.NewString()
	t.Cleanup(func() {
		_ = s.Delete(context.Background(), running)
		_ = s.Delete(context.Background(), parked)
	})
	attach := map[string]time.Duration{}
	for _, id := range []string{running, parked} {
		vm, err := s.Provision(ctx, ProvisionRequest{DatabaseID: id, Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30, RestoreWindowSeconds: 3600, Replicated: true})
		if err != nil || vm.NodeID != "pa" || vm.StandbyNode != "pb" {
			t.Fatalf("provision = %+v, %v; want primary on pa, standby on pb", vm, err)
		}
		started := time.Now()
		for _, sql := range []string{"CREATE ROLE app LOGIN PASSWORD '" + password + "'", "CREATE DATABASE app OWNER app"} {
			if resp, err := s.Admin(ctx, id, sql); err != nil || resp.Error != "" {
				t.Fatalf("admin: %+v %v", resp, err)
			}
		}
		s.standbyWG.Wait()
		attach[id] = time.Since(started)
		if got, _ := store.Get(ctx, id); !got.StandbySynced || !got.StandbyRunning {
			t.Fatalf("after the wake: %+v; want a synchronized standby", got)
		}
		query(ctx, t, a.rt, id, password, "CREATE TABLE kept(v text); INSERT INTO kept SELECT 'bulk' FROM generate_series(1, 20000); INSERT INTO kept VALUES ('acknowledged')")
	}
	if err := s.Park(ctx, parked); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Get(ctx, parked); !got.StandbySynced || got.StandbyRunning || b.rt.Running(parked) {
		t.Fatalf("after a clean park: %+v (standby running %v)", got, b.rt.Running(parked))
	}

	// pa dies: the running primary gets no shutdown.
	killNodeVM(t, "pa", running)
	lost := a.node
	lost.Lifecycle = NodeUnavailable
	seedNodes(t, store, lost)
	settleStarted := time.Now()
	if n, err := s.SettleLostNodes(ctx); err != nil || n != 2 {
		t.Fatalf("settle = %d, %v; want both databases failed over", n, err)
	}
	settle := time.Since(settleStarted)
	if got, _ := store.Get(ctx, running); got.NodeID != "pb" || got.State != StateRunning {
		t.Fatalf("running database after failover: %+v", got)
	}
	wakeStarted := time.Now()
	if vm, err := s.Wake(ctx, parked); err != nil || vm.NodeID != "pb" || vm.PromotePending {
		t.Fatalf("wake of the parked database = %+v, %v", vm, err)
	}
	promoteBoot := time.Since(wakeStarted)
	for _, id := range []string{running, parked} {
		if got := query(ctx, t, b.rt, id, password, "SELECT count(*)::text FROM kept"); got != "20001" {
			t.Fatalf("%s: %s rows on pb; want every acknowledged commit (20001)", id, got)
		}
		query(ctx, t, b.rt, id, password, "INSERT INTO kept VALUES ('after the failover')")
		if wal := query(ctx, t, b.rt, id, password, "SELECT pg_walfile_name(pg_current_wal_lsn())"); !strings.HasPrefix(wal, "00000002") {
			t.Fatalf("%s writes WAL file %s; want timeline 2", id, wal)
		}
	}
	t.Logf("wake + admin + standby synchronous %v / %v; failover of both %v; parked failover's promotion boot %v",
		attach[running].Round(time.Millisecond), attach[parked].Round(time.Millisecond), settle.Round(time.Millisecond), promoteBoot.Round(time.Millisecond))
}
