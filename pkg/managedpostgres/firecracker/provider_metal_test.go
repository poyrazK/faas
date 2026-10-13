//go:build metal && linux

// adr: 1027 — the native driver end to end: managedpostgres.Provider calls
// schedd over gRPC, schedd's scheduler drives vmmd, vmmd runs a jailed
// Firecracker VM, and a client logs in through pgproxy with the issued
// CredentialMaterial.
package firecracker

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"net/netip"
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
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbarchive"
	"github.com/onebox-faas/faas/pkg/dbcreds"
	"github.com/onebox-faas/faas/pkg/dbroute"
	"github.com/onebox-faas/faas/pkg/dbsched"
	"github.com/onebox-faas/faas/pkg/dbvm"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/pgproxy"
	"github.com/onebox-faas/faas/pkg/pgscram"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
)

// Verifier and Forget let the fake verifier table serve pgproxy directly.
func (f *fakeVerifiers) Verifier(_ context.Context, databaseID, role string) (pgscram.Verifier, error) {
	if v, ok := f.get(databaseID, role); ok {
		return v, nil
	}
	return pgscram.Verifier{}, dbcreds.ErrNoVerifier
}

func (f *fakeVerifiers) Forget(string, string) {}

type driverLeaser struct {
	mu   sync.Mutex
	held map[string]fcvm.Lease
}

func (l *driverLeaser) Acquire(instance string) (fcvm.Lease, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held == nil {
		l.held = map[string]fcvm.Lease{}
	}
	slot := fcvm.MaxSlots - 80 - len(l.held)
	lease := fcvm.Lease{Instance: instance, Slot: slot, UID: fcvm.JailUIDBase + slot, GID: fcvm.JailUIDBase + slot}
	l.held[instance] = lease
	return lease, nil
}

func (l *driverLeaser) Release(instance string) error {
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

func proxyTLS(t *testing.T, suffix string) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "*." + suffix},
		DNSNames: []string{"*." + suffix}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
}

func TestMetalDriverEndToEnd(t *testing.T) {
	workdir, base := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_BASE")
	if workdir == "" || base == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_BASE to run the driver metal test")
	}
	st := newMetalStack(t, workdir, base)
	p, sched := st.p, st.sched
	_, proxyPort, _ := net.SplitHostPort(st.proxyAddr)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	id := uuid.NewString()
	t.Cleanup(func() { _ = sched.Delete(context.Background(), id) })

	started := time.Now()
	obs, err := p.Provision(ctx, managedpostgres.ProvisionRequest{ResourceID: id, Spec: devSpec()})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	provisioned := time.Since(started)
	if obs.Status != managedpostgres.ProviderStatusReady || obs.Spec != devSpec() {
		t.Fatalf("observed = %+v", obs)
	}
	started = time.Now()
	mig, err := p.IssueCredentials(ctx, managedpostgres.CredentialRequest{ProviderResourceID: id, IdentityKey: "mig-1", Access: managedpostgres.CredentialMigration})
	if err != nil {
		t.Fatalf("issue migration: %v", err)
	}
	issued := time.Since(started)
	rwReq := managedpostgres.CredentialRequest{ProviderResourceID: id, IdentityKey: "rw-1", Access: managedpostgres.CredentialReadWrite}
	rw, err := p.IssueCredentials(ctx, rwReq)
	if err != nil {
		t.Fatalf("issue read-write: %v", err)
	}
	if err := sched.Park(ctx, id); err != nil {
		t.Fatal(err)
	}

	// The app route (ADR-1027): the node-local forwarder relays bytes to
	// pgproxyd. The host DNAT is proven by pkg/netns's metal test; here the
	// original destination is the route's, as SO_ORIGINAL_DST reports it.
	route, err := dbroute.New(dbroute.Config{
		Upstreams:   []string{st.proxyAddr},
		Destination: netip.AddrPortFrom(api.DatabaseRouteAddress(), api.DatabaseRoutePort),
		OriginalDestination: func(net.Conn) (netip.AddrPort, error) {
			return netip.AddrPortFrom(api.DatabaseRouteAddress(), api.DatabaseRoutePort), nil
		},
		DialTimeout:          api.DatabaseRouteDialTimeout,
		MaxSessions:          api.DatabaseRouteSessionsPerNodeMax,
		MaxSessionsPerSource: api.DatabaseRouteSessionsPerSourceMax,
	})
	if err != nil {
		t.Fatal(err)
	}
	routeLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = route.Serve(ctx, routeLn) }()
	_, routePort, _ := net.SplitHostPort(routeLn.Addr().String())

	// connectVia uses the material as an app would, except that the
	// endpoint resolves to a local port: the proxy or the route.
	connectVia := func(port string, m managedpostgres.CredentialMaterial) (*pgx.Conn, error) {
		ep := m.Endpoints[0]
		pc, err := pgx.ParseConfig("host=" + ep.Host + " port=" + port + " user=" + m.Username + " password=" + m.Password +
			" dbname=" + m.Database + " sslmode=" + m.TLSMode + " connect_timeout=30")
		if err != nil {
			return nil, err
		}
		if ep.Port != 5432 {
			t.Fatalf("endpoint port = %d", ep.Port)
		}
		pc.LookupFunc = func(context.Context, string) ([]string, error) { return []string{"127.0.0.1"}, nil }
		pc.TLSConfig.ServerName = ep.Host
		return pgx.ConnectConfig(ctx, pc)
	}
	connect := func(m managedpostgres.CredentialMaterial) (*pgx.Conn, error) { return connectVia(proxyPort, m) }

	// The cold connection takes the app route: TLS, SNI routing, SCRAM and
	// the wake all cross the L4 hop.
	started = time.Now()
	conn, err := connectVia(routePort, mig)
	if err != nil {
		t.Fatalf("migration login through the app route: %v", err)
	}
	coldConnect := time.Since(started)
	if _, err := conn.Exec(ctx, "CREATE TABLE notes (id bigserial PRIMARY KEY, body text)"); err != nil {
		t.Fatalf("migration: %v", err)
	}
	_ = conn.Close(ctx)

	conn, err = connect(rw)
	if err != nil {
		t.Fatalf("runtime login through the proxy: %v", err)
	}
	var body string
	if err := conn.QueryRow(ctx, "INSERT INTO notes (body) VALUES ('hello') RETURNING body").Scan(&body); err != nil || body != "hello" {
		t.Fatalf("runtime insert = %q, %v", body, err)
	}
	if _, err := conn.Exec(ctx, "CREATE TABLE evil (id int)"); pgState(err) != "42501" {
		t.Fatalf("runtime create = %v, want 42501", err)
	}
	_ = conn.Close(ctx)

	if err := p.RevokeCredentials(ctx, rwReq); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := connect(rw); pgState(err) != "28P01" {
		t.Fatalf("revoked login = %v, want 28P01 at the proxy", err)
	}

	if res, err := p.Delete(ctx, managedpostgres.DeleteRequest{ResourceID: id, ProviderResourceID: id}); err != nil || !res.Done {
		t.Fatalf("delete = %+v, %v", res, err)
	}
	if _, err := p.Inspect(ctx, id); !errors.Is(err, managedpostgres.ErrNotFound) {
		t.Fatalf("inspect after delete = %v", err)
	}
	if _, err := os.Stat(filepath.Join(workdir, "drivervolumes", id+".img")); !os.IsNotExist(err) {
		t.Fatalf("volume survived delete: %v", err)
	}
	t.Logf("provision %v; issue credential %v; parked -> first query through the app route %v",
		provisioned.Round(time.Millisecond), issued.Round(time.Millisecond), coldConnect.Round(time.Millisecond))
}

// metalStack is the native backend end to end: a jailed database runtime
// behind vmmd's database RPCs, schedd's database scheduler over gRPC, the
// driver, and pgproxy on a local port.
type metalStack struct {
	p         *Provider
	sched     *dbsched.Scheduler
	store     *dbsched.MemStore
	client    scheddpb.ScheddClient
	cfg       Config
	proxyAddr string
}

func newMetalStack(t *testing.T, workdir, base string) *metalStack {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	parent := "dbdriver-metal-test"
	if err := os.MkdirAll(filepath.Join("/sys/fs/cgroup", parent), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Join("/sys/fs/cgroup", parent)) })
	// dbbackupd's role: every VM archives WAL and base backups here.
	archiveDir, archiveSock := filepath.Join(workdir, "driver-archive"), filepath.Join(workdir, "driver-archive.sock")
	_ = os.RemoveAll(archiveDir)
	t.Cleanup(func() { _ = os.RemoveAll(archiveDir) })
	archive, err := dbarchive.NewLocalStore(archiveDir)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(archiveSock)
	archiveLn, err := net.Listen("unix", archiveSock)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = (&dbarchive.Server{Store: archive, Backups: archive}).Serve(ctx, archiveLn) }()
	rt, err := dbvm.NewRuntime(dbvm.RuntimeConfig{
		JailBase: filepath.Join(workdir, "djail"), VolumeRoot: filepath.Join(workdir, "drivervolumes"),
		KernelPath: "/srv/fc/kernel/1.7.0", Bases: map[int]string{17: base}, ParentCgroup: parent,
		JailerBin: "/usr/local/bin/jailer", FirecrackerBin: "/usr/local/bin/firecracker", Leases: &driverLeaser{},
		ArchiveSocket: archiveSock,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Recover(); err != nil {
		t.Fatal(err)
	}
	vmmdSock, scheddSock := filepath.Join(workdir, "driver-vmmd.sock"), filepath.Join(workdir, "driver-schedd.sock")
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
	scheddConn, err := dialUnix(context.Background(), "unix://"+scheddSock, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scheddConn.Close() })
	client := scheddpb.NewScheddClient(scheddConn)

	cfg, err := ParseConfig(managedpostgres.BackendConfig{Region: "eu-central", Settings: map[string]string{settingHostSuffix: "db.metal.test", settingTLSMode: "require"}})
	if err != nil {
		t.Fatal(err)
	}
	cfg.CredentialKey = testCredentialKey
	cfg.MaxRestoreWindowSeconds = 7 * 86400
	verifiers := newFakeVerifiers()
	p, err := New(cfg, client, verifiers)
	if err != nil {
		t.Fatal(err)
	}
	dialer := pgproxy.NewVmmdDialerWith(nil, dialUnix)
	t.Cleanup(func() { _ = dialer.Close() })
	proxy, err := pgproxy.New(pgproxy.Config{TLS: proxyTLS(t, cfg.HostSuffix), HostSuffix: cfg.HostSuffix, DecoySecret: []byte(strings.Repeat("k", 32))},
		verifiers, pgproxy.NewScheddWaker(client), dialer, nil)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() { _ = proxy.Serve(ctx, ln) }()
	return &metalStack{p: p, sched: sched, store: store, client: client, cfg: cfg, proxyAddr: ln.Addr().String()}
}

// connectThroughProxy dials pgproxy with the material as an app would, the
// endpoint resolving to the local proxy.
func connectThroughProxy(addr string) Connector {
	return func(ctx context.Context, m managedpostgres.CredentialMaterial) (*pgx.Conn, error) {
		host, port, _ := net.SplitHostPort(addr)
		ep := m.Endpoints[0]
		pc, err := pgx.ParseConfig("host=" + ep.Host + " port=" + port + " user=" + m.Username + " password=" + m.Password +
			" dbname=" + m.Database + " sslmode=" + m.TLSMode + " connect_timeout=30")
		if err != nil {
			return nil, err
		}
		pc.LookupFunc = func(context.Context, string) ([]string, error) { return []string{host}, nil }
		pc.TLSConfig.ServerName = ep.Host
		return pgx.ConnectConfig(ctx, pc)
	}
}

// adr: 1027 — the platform's provider qualification against the real stack:
// QualifyProvider provisions a database VM, issues credentials, and runs
// all four probers through pgproxy, including a real suspend and wake.
func TestMetalQualifyProvider(t *testing.T) {
	workdir, base := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_BASE")
	if workdir == "" || base == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_BASE to run the driver metal test")
	}
	st := newMetalStack(t, workdir, base)
	st.p.WithConnector(connectThroughProxy(st.proxyAddr))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	id := uuid.NewString()
	t.Cleanup(func() { _ = st.sched.Delete(context.Background(), id) })
	started := time.Now()
	report, err := managedpostgres.QualifyProvider(ctx, st.p, managedpostgres.QualificationOptions{
		ProviderName: Driver, ResourceID: id, Spec: devSpec(), Mutating: true, Timeout: 8 * time.Minute, PollInterval: 200 * time.Millisecond,
	})
	for _, check := range report.Checks {
		if !check.Passed {
			t.Logf("check %s failed: %s", check.Name, check.Error)
		}
	}
	if err != nil {
		t.Fatalf("QualifyProvider = %v", err)
	}
	if report.ScaleToZero == nil || report.CredentialPrivileges == nil || report.ReadOnlyCredentials == nil || report.ComputePolicy == nil {
		t.Fatalf("report lacks prober evidence: %+v", report)
	}
	t.Logf("qualified in %v: %d checks; scale-to-zero wake %d ms", time.Since(started).Round(time.Millisecond), len(report.Checks), report.ScaleToZero.WakeLatencyMS)
}

// adr: 1027 — point-in-time restore qualified against the real stack:
// archiving VMs, schedd's background restore (source flush, guest
// recovery, promotion, first backup) and the restore probers through
// pgproxy, including that the source's login cannot reach the restore.
func TestMetalQualifyProviderWithRestore(t *testing.T) {
	workdir, base := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_BASE")
	if workdir == "" || base == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_BASE to run the driver metal test")
	}
	st := newMetalStack(t, workdir, base)
	st.p.WithConnector(connectThroughProxy(st.proxyAddr))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	id := uuid.NewString()
	t.Cleanup(func() { _ = st.sched.Delete(context.Background(), id) })
	spec := devSpec()
	spec.RestoreWindowSeconds = 86400
	started := time.Now()
	report, err := managedpostgres.QualifyProvider(ctx, st.p, managedpostgres.QualificationOptions{
		ProviderName: Driver, ResourceID: id, Spec: spec, Mutating: true, Timeout: 12 * time.Minute, PollInterval: 200 * time.Millisecond,
	})
	for _, check := range report.Checks {
		if !check.Passed {
			t.Logf("check %s failed: %s", check.Name, check.Error)
		}
	}
	if err != nil {
		t.Fatalf("QualifyProvider = %v", err)
	}
	if report.Restore == nil || !report.Restore.Restored || !report.Restore.DataVerified || !report.Restore.CredentialsIsolated || !report.Restore.Deleted {
		t.Fatalf("restore evidence = %+v", report.Restore)
	}
	t.Logf("qualified with restore in %v: %d checks", time.Since(started).Round(time.Millisecond), len(report.Checks))
}
