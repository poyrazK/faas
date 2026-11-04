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
	"errors"
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
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

// rangeLeaser hands out jail identities from its own range, never reusing
// one: two nodes' allocators, sharing one host.
type rangeLeaser struct {
	mu   sync.Mutex
	next int
	held map[string]fcvm.Lease
}

// Adopt is not exercised here: no VM outlives this test's runtime.
func (_ *rangeLeaser) Adopt(string, int) (fcvm.Lease, error) {
	return fcvm.Lease{}, errors.New("adopt: not used in this test")
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
	workdir, base, guestInit := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_BASE"), os.Getenv("FAAS_GUEST_INIT")
	if workdir == "" || base == "" && guestInit == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_BASE or FAAS_GUEST_INIT to run the scheduler metal test")
	}
	if base == "" {
		base = metalBase(context.Background(), t, workdir, guestInit, 17)
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
	// A standby reports its volume like its primary, and the storage
	// sample reads both.
	standby, err := vmm.Control(ctx, b.node, running, dbguest.Request{Op: dbguest.OpStatus})
	if err != nil || standby.State != dbguest.StateStandby || standby.Storage == nil || standby.Storage.TotalBytes < 900<<20 || standby.Storage.Low {
		t.Fatalf("standby status = %+v (storage %+v), %v; want its volume", standby, standby.Storage, err)
	}
	if got, err := s.SampleStorage(ctx); err != nil || got.Sampled != 2 || got.StandbyLow != 0 {
		t.Fatalf("storage sample = %+v, %v", got, err)
	}
	t.Logf("standby volume: %d of %d bytes free", standby.Storage.FreeBytes, standby.Storage.TotalBytes)
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

// adr: 1027 — automatic failover: the primary's node stops answering while
// its VM runs on (a partition). The monitor fails over to the synchronized
// standby without losing an acknowledged commit, and once the node answers
// again the orphan sweep stops the stale primary and frees its volume.
func TestMetalAutomaticFailoverOnASilentPrimaryNode(t *testing.T) {
	workdir, base := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_BASE")
	if workdir == "" || base == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_BASE to run the scheduler metal test")
	}
	bucket := &dbarchive.FileRemote{Dir: filepath.Join(workdir, "afbucket")}
	t.Cleanup(func() { _ = os.RemoveAll(bucket.Dir) })
	a := newMetalNode(t, workdir, map[int]string{17: base}, "fa", 20<<30, &rangeLeaser{next: fcvm.MaxSlots - 40}, bucket)
	b := newMetalNode(t, workdir, map[int]string{17: base}, "fb", 10<<30, &rangeLeaser{next: fcvm.MaxSlots - 60}, bucket)
	store := NewMemStore()
	seedNodes(t, store, a.node, b.node)
	vmm := NewGRPCVMMWithDial(nil, func(ctx context.Context, target string, _ *tls.Config) (*grpc.ClientConn, error) {
		return grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	})
	t.Cleanup(func() { _ = vmm.Close() })
	s := New(store, vmm, Config{IdleTimeout: time.Hour, ReadyTimeout: time.Minute, SharedArchive: true, AutoFailoverAfter: 3 * time.Second}, nil)
	t.Cleanup(s.standbyWG.Wait)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	id, password := uuid.NewString(), uuid.NewString()
	t.Cleanup(func() { _ = s.Delete(context.Background(), id) })
	if _, err := s.Provision(ctx, ProvisionRequest{DatabaseID: id, Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30, Replicated: true}); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{"CREATE ROLE app LOGIN PASSWORD '" + password + "'", "CREATE DATABASE app OWNER app"} {
		if resp, err := s.Admin(ctx, id, sql); err != nil || resp.Error != "" {
			t.Fatalf("admin: %+v %v", resp, err)
		}
	}
	s.standbyWG.Wait()
	if got, _ := store.Get(ctx, id); !got.StandbySynced {
		t.Fatalf("no synchronized standby: %+v", got)
	}
	query(ctx, t, a.rt, id, password, "CREATE TABLE kept(v text); INSERT INTO kept SELECT 'acknowledged' FROM generate_series(1, 5000)")

	a.partition()
	silent := time.Now()
	for {
		if _, err := s.CheckStandbys(ctx); err != nil {
			t.Fatal(err)
		}
		if got, _ := store.Get(ctx, id); got.NodeID == "fb" {
			break
		}
		if time.Since(silent) > time.Minute {
			t.Fatal("no automatic failover within a minute of the primary's node going silent")
		}
		time.Sleep(500 * time.Millisecond)
	}
	failedOver := time.Since(silent)
	if got := query(ctx, t, b.rt, id, password, "SELECT count(*)::text FROM kept"); got != "5000" {
		t.Fatalf("rows on the new primary = %s; want every acknowledged commit (5000)", got)
	}
	if !a.rt.Running(id) {
		t.Fatal("the partitioned primary's VM should still run until its node answers")
	}

	a.heal(t)
	for deadline := time.Now().Add(2 * time.Minute); a.rt.Running(id) || fileExists(a.rt.VolumePath(id)); {
		if _, err := s.CollectOrphans(ctx); err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("the stale primary survived its node answering again (running %v)", a.rt.Running(id))
		}
		time.Sleep(time.Second)
	}
	t.Logf("silent primary node -> failed over in %v (threshold 3 s); stale primary stopped once its node answered", failedOver.Round(time.Millisecond))
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// adr: 1027 — a database joins the production class and leaves it again
// on real VMs: the standby attaches and turns synchronous after the
// resize; leaving stops the database, the standby and removes its volume,
// and once the primary runs again its slot for the standby is gone. No
// row is lost either way.
func TestMetalProductionClassChange(t *testing.T) {
	workdir, base := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_BASE")
	if workdir == "" || base == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_BASE to run the scheduler metal test")
	}
	bucket := &dbarchive.FileRemote{Dir: filepath.Join(workdir, "ccbucket")}
	t.Cleanup(func() { _ = os.RemoveAll(bucket.Dir) })
	a := newMetalNode(t, workdir, map[int]string{17: base}, "ca", 20<<30, &rangeLeaser{next: fcvm.MaxSlots - 40}, bucket)
	b := newMetalNode(t, workdir, map[int]string{17: base}, "cb", 10<<30, &rangeLeaser{next: fcvm.MaxSlots - 60}, bucket)
	store := NewMemStore()
	seedNodes(t, store, a.node, b.node)
	vmm := NewGRPCVMMWithDial(nil, func(ctx context.Context, target string, _ *tls.Config) (*grpc.ClientConn, error) {
		return grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	})
	t.Cleanup(func() { _ = vmm.Close() })
	s := New(store, vmm, Config{IdleTimeout: time.Hour, ReadyTimeout: time.Minute, SharedArchive: true, StandbySeedTimeout: 5 * time.Minute}, nil)
	t.Cleanup(s.standbyWG.Wait)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	id, password := uuid.NewString(), uuid.NewString()
	t.Cleanup(func() { _ = s.Delete(context.Background(), id) })
	if vm, err := s.Provision(ctx, ProvisionRequest{DatabaseID: id, Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30}); err != nil || vm.NodeID != "ca" {
		t.Fatalf("provision = %+v, %v", vm, err)
	}
	for _, sql := range []string{"CREATE ROLE app LOGIN PASSWORD '" + password + "'", "CREATE DATABASE app OWNER app"} {
		if resp, err := s.Admin(ctx, id, sql); err != nil || resp.Error != "" {
			t.Fatalf("admin: %+v %v", resp, err)
		}
	}
	query(ctx, t, a.rt, id, password, "CREATE TABLE kept(v text); INSERT INTO kept SELECT 'standalone' FROM generate_series(1, 1000)")

	started := time.Now()
	vm, err := s.Resize(ctx, id, Shape{VcpuCount: 1, MemMiB: 512, VolumeBytes: 1 << 30, Replicated: true})
	if err != nil || !vm.Replicated || vm.StandbyNode != "cb" || vm.State != StateRunning {
		t.Fatalf("into production = %+v, %v", vm, err)
	}
	s.standbyWG.Wait()
	joined := time.Since(started)
	if got, _ := store.Get(ctx, id); !got.StandbySynced || !b.rt.Running(id) {
		t.Fatalf("after joining production: %+v (standby running %v)", got, b.rt.Running(id))
	}
	// A commit now waits for the standby.
	query(ctx, t, a.rt, id, password, "INSERT INTO kept SELECT 'replicated' FROM generate_series(1, 1000)")

	started = time.Now()
	vm, err = s.Resize(ctx, id, Shape{VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30})
	if err != nil || vm.Replicated || vm.StandbyNode != "" || vm.State != StateRunning || !vm.DropStandbySlot {
		t.Fatalf("out of production = %+v, %v", vm, err)
	}
	left := time.Since(started)
	if b.rt.Running(id) || fileExists(b.rt.VolumePath(id)) {
		t.Fatalf("the standby survived leaving production (running %v)", b.rt.Running(id))
	}
	if _, err := s.CheckStandbys(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Get(ctx, id); got.DropStandbySlot {
		t.Fatalf("the slot drop is still owed: %+v", got)
	}
	if n := query(ctx, t, a.rt, id, password, "SELECT count(*)::text FROM pg_replication_slots"); n != "0" {
		t.Fatalf("%s replication slots left on the primary", n)
	}
	if n := query(ctx, t, a.rt, id, password, "SELECT count(*)::text FROM kept"); n != "2000" {
		t.Fatalf("%s rows after the class changes; want 2000", n)
	}
	t.Logf("into production (restart, standby copy, synchronous) %v; out of production (restart, standby retired) %v",
		joined.Round(time.Millisecond), left.Round(time.Millisecond))
}

// adr: 1027 — automatic failover of a parked database: its node goes
// silent with nothing running there. The monitor probes the node, fails
// the database over after the threshold, and the next wake promotes the
// standby volume on its node with every acknowledged commit. The old
// volume goes once the node answers again.
func TestMetalAutomaticFailoverOfAParkedDatabase(t *testing.T) {
	workdir, base := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_BASE")
	if workdir == "" || base == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_BASE to run the scheduler metal test")
	}
	bucket := &dbarchive.FileRemote{Dir: filepath.Join(workdir, "apbucket")}
	t.Cleanup(func() { _ = os.RemoveAll(bucket.Dir) })
	a := newMetalNode(t, workdir, map[int]string{17: base}, "ka", 20<<30, &rangeLeaser{next: fcvm.MaxSlots - 40}, bucket)
	b := newMetalNode(t, workdir, map[int]string{17: base}, "kb", 10<<30, &rangeLeaser{next: fcvm.MaxSlots - 60}, bucket)
	store := NewMemStore()
	seedNodes(t, store, a.node, b.node)
	vmm := NewGRPCVMMWithDial(nil, func(ctx context.Context, target string, _ *tls.Config) (*grpc.ClientConn, error) {
		return grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	})
	t.Cleanup(func() { _ = vmm.Close() })
	s := New(store, vmm, Config{IdleTimeout: time.Hour, ReadyTimeout: time.Minute, SharedArchive: true, AutoFailoverAfter: 3 * time.Second}, nil)
	t.Cleanup(s.standbyWG.Wait)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	id, password := uuid.NewString(), uuid.NewString()
	t.Cleanup(func() { _ = s.Delete(context.Background(), id) })
	if _, err := s.Provision(ctx, ProvisionRequest{DatabaseID: id, Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30, Replicated: true}); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{"CREATE ROLE app LOGIN PASSWORD '" + password + "'", "CREATE DATABASE app OWNER app"} {
		if resp, err := s.Admin(ctx, id, sql); err != nil || resp.Error != "" {
			t.Fatalf("admin: %+v %v", resp, err)
		}
	}
	s.standbyWG.Wait()
	query(ctx, t, a.rt, id, password, "CREATE TABLE kept(v text); INSERT INTO kept SELECT 'acknowledged' FROM generate_series(1, 5000)")
	if err := s.Park(ctx, id); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Get(ctx, id); got.State != StateParked || !got.StandbySynced {
		t.Fatalf("after the park: %+v", got)
	}

	a.partition()
	silent := time.Now()
	for {
		if _, err := s.CheckStandbys(ctx); err != nil {
			t.Fatal(err)
		}
		if got, _ := store.Get(ctx, id); got.NodeID == "kb" {
			if !got.PromotePending || got.State != StateParked {
				t.Fatalf("after the failover: %+v; want parked with its promotion pending", got)
			}
			break
		}
		if time.Since(silent) > time.Minute {
			t.Fatal("no automatic failover within a minute of the parked database's node going silent")
		}
		time.Sleep(500 * time.Millisecond)
	}
	failedOver := time.Since(silent)
	started := time.Now()
	if vm, err := s.Wake(ctx, id); err != nil || vm.NodeID != "kb" || vm.PromotePending {
		t.Fatalf("wake after the failover = %+v, %v", vm, err)
	}
	promoted := time.Since(started)
	if got := query(ctx, t, b.rt, id, password, "SELECT count(*)::text FROM kept"); got != "5000" {
		t.Fatalf("rows on the new primary = %s; want every acknowledged commit (5000)", got)
	}
	query(ctx, t, b.rt, id, password, "INSERT INTO kept VALUES ('after the failover')")

	a.heal(t)
	for deadline := time.Now().Add(2 * time.Minute); fileExists(a.rt.VolumePath(id)); {
		if _, err := s.CollectOrphans(ctx); err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("the old primary volume survived its node answering again")
		}
		time.Sleep(time.Second)
	}
	t.Logf("silent node -> parked database failed over in %v (threshold 3 s); promotion wake %v", failedOver.Round(time.Millisecond), promoted.Round(time.Millisecond))
}
