package vmmdgrpc

// adr: 20261017 connection survival, §2 instance bridge unit (P1c)

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bridgeproc"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/sdunit"
)

type fakeUnits struct {
	mu      sync.Mutex
	started []string
	svcs    map[string]sdunit.Service
	running map[string]bool
	stopped []string
	listed  []string
	closed  bool
}

func (f *fakeUnits) StartService(_ context.Context, name string, svc sdunit.Service) (sdunit.Unit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, name)
	f.svcs[name] = svc
	f.running[name] = true
	return sdunit.Unit{Name: name, LoadState: "loaded", ActiveState: "active", MainPID: 4242,
		ControlGroup: "/faas.slice/faas-tenant.slice/faas-tenant-ibridge.slice/" + name, InvocationID: "0123456789abcdef0123456789abcdef"}, nil
}

func (f *fakeUnits) Stop(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, name)
	delete(f.running, name)
	return nil
}

func (f *fakeUnits) Unit(_ context.Context, name string) (sdunit.Unit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.running[name] {
		return sdunit.Unit{Name: name, LoadState: "not-found"}, nil
	}
	return sdunit.Unit{Name: name, LoadState: "loaded", ActiveState: "active"}, nil
}

func (f *fakeUnits) List(context.Context, string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	names := slices.Clone(f.listed)
	for name := range f.running {
		names = append(names, name)
	}
	return names, nil
}

func (f *fakeUnits) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeUnits) snapshot() (started, stopped []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.started), slices.Clone(f.stopped)
}

type fakeUnitProcess struct{ dead, closed atomic.Bool }

func (p *fakeUnitProcess) alive() bool { return !p.dead.Load() }
func (p *fakeUnitProcess) close()      { p.closed.Store(true) }

type unitFixture struct {
	manager *streamBridgeManager
	units   *fakeUnits
	spawns  atomic.Int32
	mu      sync.Mutex
	procs   []*fakeUnitProcess
	openErr error
	now     time.Time
}

func (f *unitFixture) proc(i int) *fakeUnitProcess {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.procs[i]
}

func newUnitFixture(t *testing.T) *unitFixture {
	t.Helper()
	bridgePath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(streamBridgePathEnv, bridgePath)
	savedRoot, savedChown, savedNetns := bridgeRunRoot, chownBridgeDir, netnsRunDir
	t.Cleanup(func() { bridgeRunRoot, chownBridgeDir, netnsRunDir = savedRoot, savedChown, savedNetns })
	bridgeRunRoot = filepath.Join(t.TempDir(), "ibridge")
	chownBridgeDir = func(string, int, int) error { return nil }
	netnsRunDir = t.TempDir()
	for _, name := range []string{"fc-a", "fc-b"} {
		if err := os.WriteFile(filepath.Join(netnsRunDir, name), nil, 0o444); err != nil {
			t.Fatal(err)
		}
	}
	f := &unitFixture{units: &fakeUnits{svcs: map[string]sdunit.Service{}, running: map[string]bool{}}, now: time.Unix(1_800_000_000, 0)}
	f.manager = newTestStreamBridgeManager(t, func() { f.spawns.Add(1) })
	f.manager.now = func() time.Time { return f.now }
	f.manager.unitsOn = func() bool { return true }
	f.manager.connectUnits = func(context.Context) (sdunit.Manager, error) { return f.units, nil }
	f.manager.openUnitProcess = func(sdunit.Unit, string) (unitProcess, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.openErr != nil {
			return nil, f.openErr
		}
		p := &fakeUnitProcess{}
		f.procs = append(f.procs, p)
		return p, nil
	}
	return f
}

func bridgeUID(slot int) bridgeIdentity {
	return bridgeIdentity{uid: fcvm.BridgeUIDBase + slot, gid: fcvm.BridgeUIDBase + slot}
}

func (f *unitFixture) acquire(t *testing.T, instance string, id bridgeIdentity) {
	t.Helper()
	lease, err := f.manager.acquire(context.Background(), &vmmdpb.ForwardHTTPRequestInit{Instance: instance, Port: 8080}, "fc-"+instance, id)
	if err != nil {
		t.Fatal(err)
	}
	lease.release()
}

// The bridge runs as a sandboxed unit in the instance's namespace as its
// bridge uid, survives idle time, and stops (unloaded, socket directory
// removed) only when the instance goes.
func TestInstanceBridgeUnitLivesAsLongAsItsInstance(t *testing.T) {
	f := newUnitFixture(t)
	f.acquire(t, "a", bridgeUID(1))
	f.acquire(t, "a", bridgeUID(1))
	started, _ := f.units.snapshot()
	if len(started) != 1 || f.spawns.Load() != 0 {
		t.Fatalf("units started %v, processes spawned %d; want one unit, no process", started, f.spawns.Load())
	}
	name := started[0]
	if instance, ok := bridgeUnitInstance(name); !ok || instance != "a" {
		t.Fatalf("unit name %q", name)
	}
	svc := f.units.svcs[name]
	socket := bridgeUID(1).streamBridgeSocket("a")
	if svc.UID != fcvm.BridgeUIDBase+1 || svc.GID != svc.UID || svc.NetworkNamespacePath != filepath.Join(netnsRunDir, "fc-a") ||
		svc.Slice != instanceBridgeSlice || svc.ExecStart[1] != socket || svc.ExecStart[2] != "10.0.0.2" || svc.ExecStart[3] != "8080" ||
		!slices.Equal(svc.ReadWritePaths, []string{filepath.Dir(socket)}) || svc.MemoryMaxBytes != api.InstanceBridgeMemoryMaxMB<<20 ||
		len(svc.IPAddressAllow) != 1 || svc.IPAddressAllow[0].String() != "10.0.0.2/32" {
		t.Fatalf("service = %+v", svc)
	}
	for _, kv := range svc.Environment {
		if strings.HasPrefix(kv, bridgeproc.ParentPIDEnv+"=") {
			t.Fatalf("unit bridge told to die with vmmd: %v", svc.Environment)
		}
	}
	if !slices.Contains(svc.Environment, bridgeproc.ExpectUIDEnv+"="+strconv.Itoa(fcvm.BridgeUIDBase+1)) {
		t.Fatalf("unit bridge not told its uid: %v", svc.Environment)
	}

	f.now = f.now.Add(time.Hour)
	f.manager.reapIdle(context.Background())
	if _, stopped := f.units.snapshot(); len(stopped) != 0 {
		t.Fatalf("idle reaper stopped %v", stopped)
	}
	if _, err := os.Stat(filepath.Dir(socket)); err != nil {
		t.Fatalf("socket directory: %v", err)
	}

	f.manager.forget(context.Background(), "a")
	if _, stopped := f.units.snapshot(); !slices.Equal(stopped, []string{name}) {
		t.Fatalf("park stopped %v, want %s", stopped, name)
	}
	if !f.proc(0).closed.Load() {
		t.Fatal("pidfd left open")
	}
	if _, err := os.Stat(filepath.Dir(socket)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket directory after park: %v", err)
	}
}

func TestInstanceBridgeUnitReplacedWhenItsProcessDies(t *testing.T) {
	f := newUnitFixture(t)
	f.acquire(t, "a", bridgeUID(1))
	f.proc(0).dead.Store(true)
	f.acquire(t, "a", bridgeUID(1))
	started, stopped := f.units.snapshot()
	if len(started) != 2 || !slices.Equal(stopped, started[:1]) {
		t.Fatalf("started %v stopped %v, want the dead run stopped and a new one", started, stopped)
	}
}

// A run whose process cannot be proven the unit's is stopped, never kept.
func TestInstanceBridgeUnitUnprovenRunIsStopped(t *testing.T) {
	f := newUnitFixture(t)
	f.openErr = errors.New("pid 4242 is in another cgroup")
	_, err := f.manager.acquire(context.Background(), &vmmdpb.ForwardHTTPRequestInit{Instance: "a", Port: 8080}, "fc-a", bridgeUID(1))
	if err == nil {
		t.Fatal("acquire succeeded on an unproven unit")
	}
	started, stopped := f.units.snapshot()
	if len(started) != 1 || !slices.Equal(stopped, started) {
		t.Fatalf("started %v stopped %v", started, stopped)
	}
}

// Root bridges (switch FAAS_BRIDGE_UNPRIVILEGED off, or a public target)
// keep the process path.
func TestInstanceBridgeUnitOnlyForBridgeUIDs(t *testing.T) {
	f := newUnitFixture(t)
	f.acquire(t, "a", bridgeIdentity{})
	if started, _ := f.units.snapshot(); len(started) != 0 || f.spawns.Load() != 1 {
		t.Fatalf("units %v, spawns %d; want the process path", started, f.spawns.Load())
	}
}

func TestInstanceBridgeUnitsStopAtShutdown(t *testing.T) {
	f := newUnitFixture(t)
	f.acquire(t, "a", bridgeUID(1))
	f.acquire(t, "b", bridgeUID(2))
	if err := f.manager.close(context.Background()); err != nil {
		t.Fatal(err)
	}
	started, stopped := f.units.snapshot()
	slices.Sort(started)
	slices.Sort(stopped)
	if !slices.Equal(started, stopped) || !f.units.closed {
		t.Fatalf("started %v stopped %v closed %v", started, stopped, f.units.closed)
	}
}

// The startup sweep stops a predecessor's bridge units and nothing else.
func TestSweepInstanceBridgeUnits(t *testing.T) {
	f := newUnitFixture(t)
	f.acquire(t, "b", bridgeUID(2))
	f.units.listed = []string{"faas-ibridge-a-0123abcd.service", "faas-ibridge-b-89abcdef.service", "faas-ibridge-bogus.service", "sshd.service"}
	n, err := f.manager.sweepUnits(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, stopped := f.units.snapshot(); n != 1 || !slices.Equal(stopped, []string{"faas-ibridge-a-0123abcd.service"}) {
		t.Fatalf("swept %d: %v", n, stopped)
	}
}

func TestBridgeUnitNames(t *testing.T) {
	name, err := bridgeUnitName("0192f3c4-aaaa-7bbb-8ccc-0123456789ab")
	if err != nil || !sdunit.ValidServiceName(name) {
		t.Fatalf("name %q, %v", name, err)
	}
	if instance, ok := bridgeUnitInstance(name); !ok || instance != "0192f3c4-aaaa-7bbb-8ccc-0123456789ab" {
		t.Fatalf("instance of %q = %q %v", name, instance, ok)
	}
	if other, _ := bridgeUnitName("0192f3c4-aaaa-7bbb-8ccc-0123456789ab"); other == name {
		t.Fatal("two runs share a unit name")
	}
	for _, bad := range []string{"", "a/b", "a.b", "a b", strings.Repeat("x", 201)} {
		if _, err := bridgeUnitName(bad); err == nil {
			t.Errorf("named a unit for instance %q", bad)
		}
	}
	for _, bad := range []string{"faas-ibridge-a.service", "faas-ibridge-a-0123abcz.service", "faas-ibridge--0123abcd.service", "faas-ibridge-a-0123abcd.scope", "x-a-0123abcd.service"} {
		if _, ok := bridgeUnitInstance(bad); ok {
			t.Errorf("parsed %q", bad)
		}
	}
}
