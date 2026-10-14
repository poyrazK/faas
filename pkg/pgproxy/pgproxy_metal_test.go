//go:build metal && linux

// adr: 1027 — a client connects through pgproxy to a parked database: SCRAM
// at the proxy, wake through schedd's scheduler, session through vmmd into
// a jailed Firecracker VM, login with the proven key.
package pgproxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbsched"
	"github.com/onebox-faas/faas/pkg/dbvm"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/pgscram"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
)

type metalLeaser struct {
	mu   sync.Mutex
	held map[string]fcvm.Lease
}

func (l *metalLeaser) Acquire(instance string) (fcvm.Lease, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held == nil {
		l.held = map[string]fcvm.Lease{}
	}
	slot := fcvm.MaxSlots - 60 - len(l.held)
	lease := fcvm.Lease{Instance: instance, Slot: slot, UID: fcvm.JailUIDBase + slot, GID: fcvm.JailUIDBase + slot}
	l.held[instance] = lease
	return lease, nil
}

func (l *metalLeaser) Release(instance string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.held, instance)
	return nil
}

func serveUnix(t *testing.T, path string, register func(*grpc.Server)) {
	t.Helper()
	_ = os.Remove(path)
	lis, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	gsrv := grpc.NewServer()
	register(gsrv)
	go func() { _ = gsrv.Serve(lis) }()
	t.Cleanup(gsrv.Stop)
}

func dialUnix(_ context.Context, target string, _ *tls.Config) (*grpc.ClientConn, error) {
	return grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
}

func TestMetalProxyWakesParkedDatabase(t *testing.T) {
	workdir, base := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_BASE")
	if workdir == "" || base == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_BASE to run the proxy metal test")
	}
	parent := "pgproxy-metal-test"
	if err := os.MkdirAll(filepath.Join("/sys/fs/cgroup", parent), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Join("/sys/fs/cgroup", parent)) })
	rt, err := dbvm.NewRuntime(dbvm.RuntimeConfig{
		JailBase: filepath.Join(workdir, "pjail"), VolumeRoot: filepath.Join(workdir, "proxyvolumes"),
		KernelPath: "/srv/fc/kernel/1.7.0", Bases: map[int]string{17: base}, ParentCgroup: parent,
		JailerBin: "/usr/local/bin/jailer", FirecrackerBin: "/usr/local/bin/firecracker", Leases: &metalLeaser{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Recover(); err != nil {
		t.Fatal(err)
	}
	vmmdSock, scheddSock := filepath.Join(workdir, "proxy-vmmd.sock"), filepath.Join(workdir, "proxy-schedd.sock")
	serveUnix(t, vmmdSock, func(g *grpc.Server) {
		vmmdpb.RegisterVmmdServer(g, vmmdgrpc.New(nil, nil, "test", nil).WithDatabases(rt))
	})
	store := dbsched.NewMemStore()
	if err := store.UpsertNode(context.Background(), dbsched.Node{ID: "db-test", TargetURL: "unix://" + vmmdSock, AdmissionCeilingMB: 2048, VolumeCapacityBytes: 20 << 30, Lifecycle: dbsched.NodeActive}); err != nil {
		t.Fatal(err)
	}
	vmm := dbsched.NewGRPCVMMWithDial(nil, dialUnix)
	t.Cleanup(func() { _ = vmm.Close() })
	sched := dbsched.New(store, vmm, dbsched.Config{ReadyTimeout: time.Minute}, nil)
	serveUnix(t, scheddSock, func(g *grpc.Server) {
		scheddpb.RegisterScheddServer(g, scheddgrpc.New(nil, nil, nil).WithDatabases(sched))
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	id := uuid.NewString()
	t.Cleanup(func() { _ = sched.Delete(context.Background(), id) })
	if _, err := sched.Provision(ctx, dbsched.ProvisionRequest{DatabaseID: id, Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30}); err != nil {
		t.Fatal(err)
	}
	password := "proxy-" + uuid.NewString()
	verifier, err := pgscram.NewVerifier(password, pgscram.DefaultIterations)
	if err != nil {
		t.Fatal(err)
	}
	// The driver sets the verifier, never the password, inside the VM.
	for _, sql := range []string{"CREATE ROLE app LOGIN PASSWORD '" + verifier.String() + "'", "CREATE DATABASE app OWNER app"} {
		if resp, err := sched.Admin(ctx, id, sql); err != nil || resp.Error != "" {
			t.Fatalf("admin: %+v %v", resp, err)
		}
	}
	if err := sched.Park(ctx, id); err != nil {
		t.Fatal(err)
	}

	scheddConn, err := dialUnix(ctx, "unix://"+scheddSock, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scheddConn.Close() })
	dialer := NewVmmdDialerWith(nil, dialUnix)
	t.Cleanup(func() { _ = dialer.Close() })
	creds := &mapCredentials{verifiers: map[string]pgscram.Verifier{id + "|app": verifier}}
	proxy, err := New(Config{TLS: selfSignedTLS(t), HostSuffix: testSuffix, DecoySecret: []byte(strings.Repeat("k", 32))},
		creds, NewScheddWaker(scheddpb.NewScheddClient(scheddConn)), dialer, nil)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() { _ = proxy.Serve(ctx, ln) }()
	h := &harness{proxyAddr: ln.Addr().String()}
	host := id + "." + testSuffix

	if _, err := h.connect(ctx, host, "app", "wrong-password", "app", "require", nil); sqlState(err) != "28P01" {
		t.Fatalf("wrong password = %v, want 28P01", err)
	}
	if vm, _ := store.Get(ctx, id); vm.State != dbsched.StateParked {
		t.Fatalf("a refused login woke the database: %s", vm.State)
	}

	started := time.Now()
	conn, err := h.connect(ctx, host, "app", password, "app", "require", nil)
	if err != nil {
		t.Fatalf("connect through the proxy: %v", err)
	}
	var who string
	if err := conn.QueryRow(ctx, "select current_user").Scan(&who); err != nil || who != "app" {
		t.Fatalf("current_user = %q, %v", who, err)
	}
	coldConnect := time.Since(started)
	if _, err := conn.Exec(ctx, "create table t(v int); insert into t values (42)"); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(ctx)

	started = time.Now()
	conn, err = h.connect(ctx, host, "app", password, "app", "require", nil)
	if err != nil {
		t.Fatal(err)
	}
	var v int
	if err := conn.QueryRow(ctx, "select v from t").Scan(&v); err != nil || v != 42 {
		t.Fatalf("select = %d, %v", v, err)
	}
	warmConnect := time.Since(started)
	_ = conn.Close(ctx)
	if vm, _ := store.Get(ctx, id); vm.State != dbsched.StateRunning {
		t.Fatalf("state after sessions = %s", vm.State)
	}
	t.Logf("parked -> first query through proxy %v; warm connect -> first query %v",
		coldConnect.Round(time.Millisecond), warmConnect.Round(time.Millisecond))

	// The pooled endpoint: 40 clients connected at once to a database
	// that allows 25 sessions, sharing the proxy's pool of 10.
	const clients = 40
	pooled := make([]*pgx.Conn, 0, clients)
	for i := 0; i < clients; i++ {
		c, err := h.connect(ctx, id+pooledSuffix+"."+testSuffix, "app", password, "app", "require", nil)
		if err != nil {
			t.Fatalf("pooled client %d: %v", i, err)
		}
		pooled = append(pooled, c)
	}
	started = time.Now()
	var wg sync.WaitGroup
	errs := make(chan error, clients)
	for _, c := range pooled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < 20; r++ {
				var n int
				if err := c.QueryRow(ctx, "select v + $1::int from t", r).Scan(&n); err != nil || n != 42+r {
					errs <- fmt.Errorf("pooled query = %d, %w", n, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	pooledQueries := time.Since(started)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	for _, c := range pooled {
		_ = c.Close(ctx)
	}
	// The same number of direct sessions does not fit.
	var direct []*pgx.Conn
	refused := 0
	for i := 0; i < clients; i++ {
		c, err := h.connect(ctx, host, "app", password, "app", "require", nil)
		if err != nil {
			refused++
			continue
		}
		direct = append(direct, c)
	}
	for _, c := range direct {
		_ = c.Close(ctx)
	}
	if refused == 0 {
		t.Fatalf("%d direct sessions fit a database that allows 25", clients)
	}
	t.Logf("%d pooled clients ran %d queries in %v; %d of %d direct sessions were refused", clients, clients*20, pooledQueries.Round(time.Millisecond), refused, clients)
}
