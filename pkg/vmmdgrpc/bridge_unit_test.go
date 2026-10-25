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
	running map[string]sdunit.Unit
	stopped []string
	listed  []string
	closed  bool
}

func runningUnit(name string, pid int) sdunit.Unit {
	return sdunit.Unit{Name: name, LoadState: "loaded", ActiveState: "active", MainPID: pid,
		ControlGroup: "/faas.slice/faas-tenant.slice/faas-tenant-ibridge.slice/" + name, InvocationID: "0123456789abcdef0123456789abcdef"}
}

func (f *fakeUnits) StartService(_ context.Context, name string, svc sdunit.Service) (sdunit.Unit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, name)
	f.svcs[name] = svc
	f.running[name] = runningUnit(name, 4242)
	return f.running[name], nil
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
	u, ok := f.running[name]
	if !ok {
		return sdunit.Unit{Name: name, LoadState: "not-found"}, nil
	}
	return u, nil
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
	f := &unitFixture{units: &fakeUnits{svcs: map[string]sdunit.Service{}, running: map[string]sdunit.Unit{}}, now: time.Unix(1_800_000_000, 0)}
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
	// Each entry has its own socket directory (<instance>-<pid>-<seq>).
	socket := svc.ExecStart[1]
	if filepath.Dir(filepath.Dir(socket)) != bridgeRunRoot || !strings.HasPrefix(filepath.Base(filepath.Dir(socket)), "a-") {
		t.Fatalf("bridge socket %s is not one of instance a's under %s", socket, bridgeRunRoot)
	}
	if svc.UID != 0 || svc.Capabilities != sdunit.CapSetUID|sdunit.CapSetGID || svc.NetworkNamespacePath != filepath.Join(netnsRunDir, "fc-a") ||
		svc.Slice != instanceBridgeSlice || svc.ExecStart[2] != "10.0.0.2" || svc.ExecStart[3] != "8080" ||
		!slices.Equal(svc.ReadWritePaths, []string{filepath.Dir(socket)}) || svc.MemoryMaxBytes != api.InstanceBridgeMemoryMaxMB<<20 ||
		len(svc.IPAddressAllow) != 1 || svc.IPAddressAllow[0].String() != "10.0.0.2/32" {
		t.Fatalf("service = %+v", svc)
	}
	for _, kv := range svc.Environment {
		if strings.HasPrefix(kv, bridgeproc.ParentPIDEnv+"=") {
			t.Fatalf("unit bridge told to die with vmmd: %v", svc.Environment)
		}
	}
	if !slices.Contains(svc.Environment, bridgeproc.ExpectUIDEnv+"="+strconv.Itoa(fcvm.BridgeUIDBase+1)) ||
		!slices.Contains(svc.Environment, bridgeproc.DropToUIDEnv+"="+strconv.Itoa(fcvm.BridgeUIDBase+1)) {
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
	adopted, n, err := f.manager.reconcileUnits(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, stopped := f.units.snapshot(); adopted != 0 || n != 1 || !slices.Equal(stopped, []string{"faas-ibridge-a-0123abcd.service"}) {
		t.Fatalf("adopted %d, stopped %d: %v", adopted, n, stopped)
	}
	// The stopped run's socket directory goes; the live entry's stays.
	live := filepath.Dir(f.manager.entries["b"].socket)
	stale := filepath.Join(bridgeRunRoot, "a-999-1")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	f.manager.removeStaleSocketDirs()
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale socket directory kept: %v", err)
	}
	if _, err := os.Stat(live); err != nil {
		t.Fatalf("live socket directory removed: %v", err)
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

type fakeBridgeJournal struct {
	mu        sync.Mutex
	notLive   bool
	records   int
	recorded  map[string]fcvm.InstanceBridge
	forgotten []string
	adopted   map[string]fcvm.InstanceBridge
	proveErr  error
}

func (j *fakeBridgeJournal) RecordInstanceBridge(instance string, b fcvm.InstanceBridge) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.records++
	if j.notLive {
		return fcvm.ErrInstanceNotLive
	}
	j.recorded[instance] = b
	return nil
}

func (j *fakeBridgeJournal) ForgetInstanceBridge(_, unit string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.forgotten = append(j.forgotten, unit)
}

func (j *fakeBridgeJournal) AdoptedInstanceBridge(instance string) (fcvm.InstanceBridge, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	b, ok := j.adopted[instance]
	return b, ok
}

func (j *fakeBridgeJournal) ProveInstanceBridge(string, int) error { return j.proveErr }

func TestInstanceBridgeUnitRunIsRecordedAndForgotten(t *testing.T) {
	f := newUnitFixture(t)
	j := &fakeBridgeJournal{recorded: map[string]fcvm.InstanceBridge{}}
	f.manager.journal = j
	lease, err := f.manager.acquire(context.Background(), &vmmdpb.ForwardHTTPRequestInit{Instance: "a", Port: 8080, AppProtocol: "http2"}, "fc-a", bridgeUID(1))
	if err != nil {
		t.Fatal(err)
	}
	lease.release()
	started, _ := f.units.snapshot()
	rec := j.recorded["a"]
	if rec.Unit != started[0] || rec.PID != 4242 || rec.InvocationID == "" || rec.Socket != f.units.svcs[started[0]].ExecStart[1] || rec.Port != 8080 || rec.AppProtocol != "http2" {
		t.Fatalf("recorded %+v", rec)
	}
	f.manager.forget(context.Background(), "a")
	if !slices.Equal(j.forgotten, started) {
		t.Fatalf("forgotten %v, want %v", j.forgotten, started)
	}
}

// A planned stop whose successor adopts the guests leaves the units, their
// sockets and their records alone.
func TestInstanceBridgeUnitsKeptForTheSuccessor(t *testing.T) {
	f := newUnitFixture(t)
	f.manager.keepUnits = true
	f.acquire(t, "a", bridgeUID(1))
	if err := f.manager.close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, stopped := f.units.snapshot(); len(stopped) != 0 {
		t.Fatalf("handoff stopped %v", stopped)
	}
	if !f.proc(0).closed.Load() {
		t.Fatal("pidfd left open at handoff")
	}
	started, _ := f.units.snapshot()
	if _, err := os.Stat(filepath.Dir(f.units.svcs[started[0]].ExecStart[1])); err != nil {
		t.Fatalf("socket directory removed at handoff: %v", err)
	}
}

type bridgeCredVMM struct {
	VmmdAPI
	uid int
}

func (v bridgeCredVMM) BridgeCredential(string) (int, int, bool) { return v.uid, v.uid, true }

// adoptFixture is a successor with a predecessor's unit for instance a.
func adoptFixture(t *testing.T) (*unitFixture, *Server, *fakeBridgeJournal, string) {
	t.Helper()
	t.Setenv(bridgeUnprivilegedEnv, bridgeUnprivilegedValue)
	f := newUnitFixture(t)
	name := "faas-ibridge-a-0123abcd.service"
	f.units.running[name] = runningUnit(name, 7777)
	j := &fakeBridgeJournal{recorded: map[string]fcvm.InstanceBridge{}, adopted: map[string]fcvm.InstanceBridge{
		"a": {Unit: name, InvocationID: "0123456789abcdef0123456789abcdef", PID: 7777, Socket: filepath.Join(bridgeRunRoot, "a-77-1", bridgeSocketName),
			Port: 8080, AppProtocol: "http2", Netns: "fc-a"},
	}}
	f.manager.journal = j
	f.manager.probeSocket = func(string, bridgeIdentity) error { return nil }
	s := &Server{log: f.manager.log, streamBridges: f.manager, vmm: bridgeCredVMM{uid: fcvm.BridgeUIDBase + 1}}
	return f, s, j, name
}

// The successor keeps a proven unit and serves through it without starting
// a bridge of its own.
func TestReconcileAdoptsAProvenBridgeUnit(t *testing.T) {
	f, s, _, name := adoptFixture(t)
	adopted, stopped, err := f.manager.reconcileUnits(context.Background(), s.adoptBridgeUnit)
	if err != nil || adopted != 1 || stopped != 0 {
		t.Fatalf("adopted %d stopped %d: %v", adopted, stopped, err)
	}
	lease, err := f.manager.acquireSession(context.Background(), "a", "fc-a", bridgeUID(1), 5432)
	if err != nil {
		t.Fatal(err)
	}
	lease.release()
	if started, stopped := f.units.snapshot(); len(started) != 0 || len(stopped) != 0 || lease.entry.unit.name != name {
		t.Fatalf("started %v stopped %v via %s", started, stopped, lease.entry.unit.name)
	}
	if start := f.manager.started["a"]; start.appProtocol != "http2" || start.port != 8080 {
		t.Fatalf("adopted start parameters %+v", start)
	}
}

func TestReconcileStopsWhatItCannotProve(t *testing.T) {
	for name, spoil := range map[string]func(*unitFixture, *fakeBridgeJournal, string){
		"no record": func(_ *unitFixture, j *fakeBridgeJournal, _ string) { delete(j.adopted, "a") },
		"another run": func(f *unitFixture, _ *fakeBridgeJournal, unit string) {
			u := f.units.running[unit]
			u.InvocationID = "ffff" + u.InvocationID[4:]
			f.units.running[unit] = u
		},
		"another pid": func(f *unitFixture, _ *fakeBridgeJournal, unit string) {
			f.units.running[unit] = runningUnit(unit, 8888)
		},
		"process proof": func(_ *unitFixture, j *fakeBridgeJournal, _ string) { j.proveErr = errors.New("another executable") },
		"silent socket": func(f *unitFixture, _ *fakeBridgeJournal, _ string) {
			f.manager.probeSocket = func(string, bridgeIdentity) error { return errors.New("refused") }
		},
		"other socket": func(_ *unitFixture, j *fakeBridgeJournal, _ string) {
			b := j.adopted["a"]
			b.Socket = "/run/faas-ibridge/b/http.sock"
			j.adopted["a"] = b
		},
		"unprovable process": func(f *unitFixture, _ *fakeBridgeJournal, _ string) { f.openErr = errors.New("pid in another cgroup") },
	} {
		t.Run(name, func(t *testing.T) {
			f, s, j, unit := adoptFixture(t)
			spoil(f, j, unit)
			adopted, stopped, err := f.manager.reconcileUnits(context.Background(), s.adoptBridgeUnit)
			if err != nil || adopted != 0 || stopped != 1 {
				t.Fatalf("adopted %d stopped %d: %v", adopted, stopped, err)
			}
			if _, stopped := f.units.snapshot(); !slices.Equal(stopped, []string{unit}) {
				t.Fatalf("stopped %v", stopped)
			}
		})
	}
}

// The wake's prewarm starts the bridge before the instance is live, so the
// first record fails; the first lease after the wake records it, once.
func TestInstanceBridgeUnitRecordedOnceItsInstanceIsLive(t *testing.T) {
	f := newUnitFixture(t)
	j := &fakeBridgeJournal{recorded: map[string]fcvm.InstanceBridge{}, notLive: true}
	f.manager.journal = j
	f.acquire(t, "a", bridgeUID(1)) // the start, then the lease in the same call
	if _, ok := j.recorded["a"]; ok || j.records != 2 {
		t.Fatalf("recorded %v after %d attempts, want two failed attempts", j.recorded, j.records)
	}
	j.notLive = false
	f.acquire(t, "a", bridgeUID(1))
	if lease, err := f.manager.acquireSession(context.Background(), "a", "fc-a", bridgeUID(1), 5432); err != nil {
		t.Fatal(err)
	} else {
		lease.release()
	}
	started, _ := f.units.snapshot()
	if j.recorded["a"].Unit != started[0] || j.records != 3 {
		t.Fatalf("recorded %+v after %d attempts, want %s once more", j.recorded["a"], j.records, started[0])
	}
}

func TestIsInstanceBridgeSocket(t *testing.T) {
	saved := bridgeRunRoot
	t.Cleanup(func() { bridgeRunRoot = saved })
	bridgeRunRoot = "/run/faas-ibridge"
	for socket, ok := range map[string]bool{
		"/run/faas-ibridge/a-77-1/http.sock":      true,
		"/run/faas-ibridge/a/http.sock":           false, // not a run directory
		"/run/faas-ibridge/a-77-1/other.sock":     false,
		"/run/faas-ibridge/ab-77-1/http.sock":     false, // another instance
		"/run/faas-ibridge/a-b-77-1/http.sock":    false,
		"/run/faas/a-77-1/http.sock":              false,
		"/run/faas-ibridge/x/../a-77-1/http.sock": false,
	} {
		if got := isInstanceBridgeSocket(socket, "a"); got != ok {
			t.Errorf("isInstanceBridgeSocket(%q) = %v", socket, got)
		}
	}
}

// Reconcile keeps the adopted run's socket directory and removes the rest.
func TestReconcileRemovesStaleSocketDirectories(t *testing.T) {
	f, s, j, _ := adoptFixture(t)
	adoptedDir := filepath.Dir(j.adopted["a"].Socket)
	stale := filepath.Join(bridgeRunRoot, "b-77-2")
	for _, dir := range []string{adoptedDir, stale} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if adopted, _, err := f.manager.reconcileUnits(context.Background(), s.adoptBridgeUnit); err != nil || adopted != 1 {
		t.Fatalf("adopted %d: %v", adopted, err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale socket directory kept: %v", err)
	}
	if _, err := os.Stat(adoptedDir); err != nil {
		t.Fatalf("adopted run's socket directory removed: %v", err)
	}
}
