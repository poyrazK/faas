//go:build linux || darwin

package fcvm

// adr: 768
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The dial and pidfd below model native identity. They are portable behavior
// fixtures, not Linux credentials, Firecracker execution or KVM acceptance.
type nativeMachineHandleFixture struct {
	recoveryHandle
	liveErr error
	liveMu  sync.Mutex
}

func (h *nativeMachineHandleFixture) CheckLive() error {
	h.liveMu.Lock()
	defer h.liveMu.Unlock()
	return h.liveErr
}
func (h *nativeMachineHandleFixture) setLiveError(err error) {
	h.liveMu.Lock()
	defer h.liveMu.Unlock()
	h.liveErr = err
}

type nativeMachineFixture struct {
	mu       sync.Mutex
	machine  Machine
	state    string
	id       string
	requests []string
	loads    []bool
	raw      map[string]string
	status   map[string]int
	hook     func(string)
}

func (f *nativeMachineFixture) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	f.mu.Lock()
	key := req.Method + " " + req.URL.Path
	f.requests = append(f.requests, key)
	code, raw := f.status[key], f.raw[key]
	if code == 0 {
		code = http.StatusOK
		if req.Method != http.MethodGet {
			code = http.StatusNoContent
		}
	}
	if code < 300 {
		switch key {
		case "PUT /snapshot/load":
			var body struct {
				Resume bool `json:"resume_vm"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				f.mu.Unlock()
				http.Error(w, "invalid fixture load", http.StatusBadRequest)
				return
			}
			f.loads = append(f.loads, body.Resume)
			f.state = "Paused"
			if body.Resume {
				f.state = "Running"
			}
		case "PATCH /vm":
			f.state = "Running"
		case "GET /":
			if raw == "" {
				raw = fmt.Sprintf(`{"id":%q,"app_name":"Firecracker","vmm_version":"1.7.0","state":%q}`, f.id, f.state)
			}
		case "GET /machine-config":
			if raw == "" {
				b, _ := json.Marshal(f.machine)
				raw = string(b)
			}
		}
	}
	hook := f.hook
	f.mu.Unlock()
	if hook != nil {
		hook(key)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if code != http.StatusNoContent {
		_, _ = fmt.Fprint(w, raw)
	}
}

func nativeMachineAPIFixture(t *testing.T) (*JailerVMM, Lease, *nativeMachineFixture, *nativeMachineHandleFixture) {
	t.Helper()
	j, lease, _, resources := preparedNativeRegionalResources(t)
	source := nativeExecutableFixtureFile(t, "selected", []byte("\x7fELF-original-firecracker-fixture"))
	if err := j.captureExecutable(t.Context(), lease, source); err != nil {
		t.Fatal(err)
	}
	ticket, err := j.beginLaunch(t.Context(), lease)
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(ticket.authorize(42, 101), ticket.close()); err != nil {
		t.Fatal(err)
	}
	owner, err := j.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dir := writeNativeRecoveryProcess(t, root, owner.PID, lease.Instance, lease.UID, owner.StartTime)
	target := nativeExecutableFixtureFile(t, "copied", []byte("\x7fELF-original-firecracker-fixture"))
	if err := os.Symlink(target, filepath.Join(dir, "exe")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cgroup"), []byte("0::/"+ParentCgroupFor(lease.Plan)+"/"+lease.Instance+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := &nativeMachineHandleFixture{}
	f := &nativeMachineFixture{machine: Machine{VcpuCount: resources.VCPU, MemSizeMib: resources.RAMMB}, id: lease.Instance, state: "Not started", raw: make(map[string]string), status: make(map[string]int)}
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	r := &nativeProcessRecoveryRuntime{
		journal: j, owned: make(map[string]string),
		retirer: nativeProcessRetirer{probe: nativeProcessProbe{root: root}, open: func(int) (nativeProcessHandle, error) { return h, nil }},
		machineDial: func(ctx context.Context, _ string, _ nativeLaunchRecord) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
		},
	}
	if err := r.acquireDaemonOwnership(t.Context()); err != nil {
		t.Fatal(err)
	}
	lock := r.daemonLock
	t.Cleanup(func() {
		if err := lock.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			t.Error(err)
		}
	})
	r.remember(owner)
	// The native barrier requires a private/protected chroot root even when
	// the test runner's umask would make TempDir group-writable on Linux.
	if err := os.Chmod(filepath.Dir(j.root), 0o700); err != nil {
		t.Fatal(err)
	}
	v := NewJailerVMM(filepath.Dir(j.root), time.Second)
	v.nativeRecovery = r
	return v, lease, f, h
}

func (f *nativeMachineFixture) observedState() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

func (f *nativeMachineFixture) patchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, request := range f.requests {
		if request == "PATCH /vm" {
			count++
		}
	}
	return count
}

func TestNativeMachineRestoreLoadsPausedAndChecksBeforeResume(t *testing.T) {
	for _, paused := range []bool{false, true} {
		t.Run(fmt.Sprint(paused), func(t *testing.T) {
			v, lease, f, h := nativeMachineAPIFixture(t)
			if err := v.loadSnapshot(t.Context(), lease, "state", "mem", paused); err != nil {
				t.Fatal(err)
			}
			patches := f.patchCount()
			if !reflect.DeepEqual(f.loads, []bool{false}) || !h.closed || h.waited || len(h.signals) != 0 {
				t.Fatalf("load=%v handle=%+v", f.loads, h)
			}
			if paused && patches != 0 || !paused && patches != 1 {
				t.Fatalf("requests=%v", f.requests)
			}
			firstShape, firstPatch := -1, -1
			for i, request := range f.requests {
				if request == "GET /machine-config" && firstShape < 0 {
					firstShape = i
				}
				if request == "PATCH /vm" {
					firstPatch = i
				}
			}
			if firstShape < 0 || !paused && firstPatch <= firstShape {
				t.Fatalf("shape did not precede resume: %v", f.requests)
			}
		})
	}
}

func TestNativeMachineRestoreRejectsActualSnapshotShapeBeforeResume(t *testing.T) {
	for _, change := range []string{"ram", "vcpu", "zero", "negative", "overhead"} {
		t.Run(change, func(t *testing.T) {
			v, lease, f, _ := nativeMachineAPIFixture(t)
			f.mu.Lock()
			switch change {
			case "ram":
				f.machine.MemSizeMib++
			case "vcpu":
				f.machine.VcpuCount++
			case "zero":
				f.machine.VcpuCount = 0
			case "negative":
				f.machine.MemSizeMib = -1
			case "overhead":
				f.machine.MemSizeMib += 8
			}
			f.mu.Unlock()
			if err := v.loadSnapshot(t.Context(), lease, "state", "mem", false); err == nil || f.patchCount() != 0 || f.observedState() != "Paused" {
				t.Fatalf("mismatch resumed: err=%v requests=%v state=%s", err, f.requests, f.observedState())
			}
		})
	}
}

func TestNativeMachineWarmResumeObservesAgainAndRunningIsVerified(t *testing.T) {
	v, lease, f, _ := nativeMachineAPIFixture(t)
	if err := v.loadSnapshot(t.Context(), lease, "state", "mem", true); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.machine.VcpuCount++
	f.mu.Unlock()
	if err := v.ResumeVM(t.Context(), lease); err == nil || f.patchCount() != 0 {
		t.Fatalf("warm resume used stale shape: %v", err)
	}
	f.mu.Lock()
	f.machine.VcpuCount--
	f.mu.Unlock()
	if err := v.ResumeVM(t.Context(), lease); err != nil || f.patchCount() != 1 {
		t.Fatalf("warm resume: %v", err)
	}
	if err := v.ResumeVM(t.Context(), lease); err != nil || f.patchCount() != 1 {
		t.Fatalf("verified running retry: %v", err)
	}
	f.mu.Lock()
	f.machine.MemSizeMib++
	f.mu.Unlock()
	if err := v.ResumeVM(t.Context(), lease); err == nil {
		t.Fatal("running original with wrong shape passed")
	}
}

func TestNativeMachineResumeDoesNotTreatConflictAsSuccess(t *testing.T) {
	v, lease, f, _ := nativeMachineAPIFixture(t)
	f.mu.Lock()
	f.state, f.status["PATCH /vm"] = "Paused", http.StatusConflict
	f.mu.Unlock()
	if err := v.ResumeVM(t.Context(), lease); err == nil || f.patchCount() != 1 {
		t.Fatalf("conflict silently succeeded: %v", err)
	}
}

func TestNativeMachineBootChecksConcreteConfigAndRegionalFootprint(t *testing.T) {
	for _, scenario := range []string{"matches", "vcpu", "ram", "smt", "not-loaded", "regional"} {
		t.Run(scenario, func(t *testing.T) {
			v, lease, f, _ := nativeMachineAPIFixture(t)
			f.mu.Lock()
			f.state = "Running"
			expected := f.machine
			switch scenario {
			case "vcpu":
				expected.VcpuCount++
			case "ram":
				expected.MemSizeMib++
			case "smt":
				expected.Smt = true
			case "not-loaded":
				f.state = "Not started"
			case "regional":
				f.machine.VcpuCount++
				expected = f.machine
			}
			f.mu.Unlock()
			err := v.checkNativeBootMachine(t.Context(), lease, expected)
			if (err == nil) != (scenario == "matches") {
				t.Fatalf("boot check=%v", err)
			}
		})
	}
}

func TestNativeMachineRejectsUnboundOrRecycledProducerBeforeRequest(t *testing.T) {
	for _, scenario := range []string{"recovered", "generation", "lease", "revoked", "exit", "removed", "historical", "daemon", "closed-daemon", "pid-reuse", "uid", "dead", "unsupported-handle", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			v, lease, f, h := nativeMachineAPIFixture(t)
			r := v.nativeRecovery
			owner, err := r.journal.read(lease.Instance)
			if err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			switch scenario {
			case "recovered":
				delete(r.owned, lease.Instance)
			case "generation":
				r.owned[lease.Instance] = uuid.NewString()
			case "lease":
				lease.CPUMillicores++
			case "revoked":
				owner.Revoked = true
			case "exit":
				owner.Revoked, owner.ExitConfirmed = true, true
			case "removed":
				owner.Revoked, owner.ExitConfirmed, owner.ResourcesRemoved = true, true, true
			case "historical":
				owner.Version, owner.RegionalResources = 2, nil
			case "daemon":
				r.daemonLock = nil
			case "closed-daemon":
				if err := r.daemonLock.Close(); err != nil {
					t.Fatal(err)
				}
			case "pid-reuse":
				writeNativeRecoveryProcess(t, r.retirer.probe.root, 42, lease.Instance, lease.UID, 102)
			case "uid":
				writeNativeRecoveryProcess(t, r.retirer.probe.root, 42, lease.Instance, lease.UID+1, 101)
			case "dead":
				h.setLiveError(errors.New("modeled pidfd reports exit"))
			case "unsupported-handle":
				r.retirer.open = func(int) (nativeProcessHandle, error) { return &h.recoveryHandle, nil }
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if err := r.journal.write(owner); err != nil {
				t.Fatal(err)
			}
			if err := v.loadSnapshot(ctx, lease, "state", "mem", false); err == nil || len(f.requests) != 0 {
				t.Fatalf("unbound request: %v requests=%v", err, f.requests)
			}
		})
	}
}

func TestNativeMachineOwnerChangesDuringObservationRefuseResume(t *testing.T) {
	for _, scenario := range []string{"generation", "revoked", "footprint", "pid-reuse", "dead", "state", "daemon"} {
		t.Run(scenario, func(t *testing.T) {
			v, lease, f, h := nativeMachineAPIFixture(t)
			r := v.nativeRecovery
			f.mu.Lock()
			f.hook = func(key string) {
				if key != "GET /machine-config" {
					return
				}
				owner, err := r.journal.read(lease.Instance)
				if err != nil {
					t.Error(err)
					return
				}
				switch scenario {
				case "generation":
					owner.Generation = uuid.NewString()
				case "revoked":
					owner.Revoked = true
				case "footprint":
					owner.RegionalResources.VCPU++
				case "pid-reuse":
					writeNativeRecoveryProcess(t, r.retirer.probe.root, 42, lease.Instance, lease.UID, 102)
				case "dead":
					h.setLiveError(errors.New("modeled task exited during read"))
				case "state":
					f.mu.Lock()
					f.state = "Running"
					f.mu.Unlock()
				case "daemon":
					if err := r.daemonLock.Close(); err != nil {
						t.Error(err)
					}
				}
				if err := r.journal.write(owner); err != nil {
					t.Error(err)
				}
			}
			f.mu.Unlock()
			if err := v.loadSnapshot(t.Context(), lease, "state", "mem", false); err == nil || f.patchCount() != 0 {
				t.Fatalf("changed original resumed: %v requests=%v", err, f.requests)
			}
		})
	}
}

func TestNativeMachineRejectsMalformedAndUnexpectedResponses(t *testing.T) {
	tests := []struct {
		name, key, raw string
		status         int
	}{
		{"missing", "GET /machine-config", `{"mem_size_mib":128,"smt":false}`, 0},
		{"duplicate", "GET /machine-config", `{"vcpu_count":1,"vcpu_count":2,"mem_size_mib":128,"smt":false}`, 0},
		{"null", "GET /machine-config", `{"vcpu_count":null,"mem_size_mib":128,"smt":false}`, 0},
		{"fraction", "GET /machine-config", `{"vcpu_count":1.5,"mem_size_mib":128,"smt":false}`, 0},
		{"overflow", "GET /machine-config", `{"vcpu_count":999999999999999999999999,"mem_size_mib":128,"smt":false}`, 0},
		{"trailing", "GET /machine-config", `{"vcpu_count":1,"mem_size_mib":128,"smt":false} {}`, 0},
		{"oversized", "GET /machine-config", strings.Repeat(" ", 8193), 0},
		{"wrong-id", "GET /", `{"id":"replacement","app_name":"Firecracker","vmm_version":"1.7.0","state":"Paused"}`, 0},
		{"not-loaded", "GET /", `{"id":"replacement","app_name":"Firecracker","vmm_version":"1.7.0","state":"Not started"}`, 0},
		{"redirect", "GET /machine-config", "", http.StatusTemporaryRedirect},
		{"empty-success", "GET /machine-config", "", http.StatusNoContent},
		{"load-unexpected-success", "PUT /snapshot/load", "", http.StatusOK},
		{"load-error", "PUT /snapshot/load", "", http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, lease, f, _ := nativeMachineAPIFixture(t)
			f.mu.Lock()
			f.raw[tt.key], f.status[tt.key] = tt.raw, tt.status
			f.mu.Unlock()
			if err := v.loadSnapshot(t.Context(), lease, "state", "mem", false); err == nil || f.patchCount() != 0 {
				t.Fatalf("bad API response resumed: %v requests=%v", err, f.requests)
			}
		})
	}
}

func TestNativeMachineEffectTransportFailureIsNotReplayed(t *testing.T) {
	v, lease, _, _ := nativeMachineAPIFixture(t)
	var mu sync.Mutex
	requests := 0
	v.nativeRecovery.machineDial = func(context.Context, string, nativeLaunchRecord) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			buf := make([]byte, 4096)
			_, _ = server.Read(buf)
			mu.Lock()
			requests++
			mu.Unlock()
		}()
		return client, nil
	}
	if err := v.loadSnapshot(t.Context(), lease, "state", "mem", false); err == nil {
		t.Fatal("ambiguous load succeeded")
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 1 {
		t.Fatalf("ambiguous load replayed %d times", requests)
	}
}

func TestNativeMachineJournalLockWaitHonorsCancellation(t *testing.T) {
	v, lease, f, _ := nativeMachineAPIFixture(t)
	lock, err := v.nativeRecovery.journal.lock(t.Context(), lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := v.loadSnapshot(ctx, lease, "state", "mem", false); !errors.Is(err, context.DeadlineExceeded) || len(f.requests) != 0 {
		t.Fatalf("locked owner effect: %v requests=%v", err, f.requests)
	}
}

func TestNativeMachineSessionCannotEscapeOriginalOperation(t *testing.T) {
	v, lease, f, _ := nativeMachineAPIFixture(t)
	f.mu.Lock()
	f.state = "Paused"
	f.mu.Unlock()
	var escaped *nativeMachineAPI
	if err := v.withNativeMachineAPI(t.Context(), lease, func(s *nativeMachineAPI) error { escaped = s; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := escaped.resume(t.Context()); err == nil || len(f.requests) != 0 {
		t.Fatalf("expired session resumed: %v requests=%v", err, f.requests)
	}
}
