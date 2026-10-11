//go:build linux || darwin

// adr: 796 — modeled process, mounts and cgroup writes are not native acceptance.
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/storage"
)

type nativeCaptureMemoryFixture struct {
	owner                         nativeLaunchRecord
	events                        []string
	widened                       atomic.Bool
	openErr, widenErr, restoreErr error
	widen                         func()
}

func (*nativeCaptureMemoryFixture) CheckSupport() error { return nil }
func (f *nativeCaptureMemoryFixture) Open(ctx context.Context, owner nativeLaunchRecord) (nativeSnapshotMemoryFence, error) {
	if ctx.Err() != nil || !sameNativeSnapshotProcess(f.owner, owner) {
		return nil, errors.Join(ctx.Err(), errors.New("fixture: memory scope is not original"))
	}
	f.events = append(f.events, "open")
	return f, f.openErr
}
func (f *nativeCaptureMemoryFixture) Widen(ctx context.Context) error {
	f.events = append(f.events, "widen")
	f.widened.Store(true)
	if f.widen != nil {
		f.widen()
	}
	return errors.Join(ctx.Err(), f.widenErr)
}
func (f *nativeCaptureMemoryFixture) Restore(ctx context.Context) error {
	f.events = append(f.events, "restore")
	if ctx.Err() != nil || f.restoreErr != nil {
		return errors.Join(ctx.Err(), f.restoreErr)
	}
	f.widened.Store(false)
	return nil
}
func (f *nativeCaptureMemoryFixture) Close() error {
	f.events = append(f.events, "close")
	return nil
}

// A named birth file supplies real portable inode identity. Mount metadata is
// explicitly modeled; this does not exercise O_TMPFILE or native bind mounts.
type nativeCapturePreparationFixture struct {
	nativeImagePreparation
	birth    *os.File
	identity nativeLoopIdentity
}

func (p *nativeCapturePreparationFixture) Identity() nativeLoopIdentity { return p.identity }
func (p *nativeCapturePreparationFixture) CreateAnchor(point string, publish func(nativeLoopIdentity) error) (uint64, error) {
	mount, err := p.nativeImagePreparation.CreateAnchor(point, publish)
	if err != nil {
		return mount, err
	}
	if err := os.Remove(point); err != nil {
		return mount, err
	}
	return mount, os.Link(p.birth.Name(), point)
}
func (p *nativeCapturePreparationFixture) Close() error {
	return errors.Join(p.nativeImagePreparation.Close(), os.Remove(p.birth.Name()), p.birth.Close())
}

type nativeCaptureAdapterImagesFixture struct {
	*nativeCaptureFilesFixture
	placementErr error
}

func (b *nativeCaptureAdapterImagesFixture) CheckSnapshotDirectory(ctx context.Context, input *os.File, directory string) error {
	if input == nil || checkNativeEffectDirectory(directory) != nil {
		return errors.New("fixture: missing protected capture directory")
	}
	return errors.Join(ctx.Err(), b.placementErr)
}
func (b *nativeCaptureAdapterImagesFixture) prepareReal(ctx context.Context, owner nativeLaunchRecord, root, directory, name string) (nativeImagePreparation, error) {
	prepared, err := b.nativeSnapshotOutputFixture.PrepareSnapshotOutput(ctx, owner, root, directory, name)
	if err != nil {
		return nil, err
	}
	birth, err := os.CreateTemp(directory, "modeled-capture-")
	if err != nil {
		return nil, errors.Join(err, prepared.Close())
	}
	closeFailed := func(err error) (nativeImagePreparation, error) {
		return nil, errors.Join(err, prepared.Close(), os.Remove(birth.Name()), birth.Close())
	}
	if name == layerImageName {
		if _, err := birth.WriteString("original-drive"); err != nil {
			return closeFailed(err)
		}
	}
	info, err := birth.Stat()
	if err != nil {
		return closeFailed(err)
	}
	id, err := resourceFileID(info)
	if err != nil {
		return closeFailed(err)
	}
	identity := nativeLoopIdentity{Device: id.Device, Inode: id.Inode}
	b.clones[identity] = b.clones[prepared.Identity()]
	delete(b.clones, prepared.Identity())
	return &nativeCapturePreparationFixture{nativeImagePreparation: prepared, birth: birth, identity: identity}, nil
}
func (b *nativeCaptureAdapterImagesFixture) PrepareSnapshotOutput(ctx context.Context, owner nativeLaunchRecord, root, directory, name string) (nativeImagePreparation, error) {
	return b.prepareReal(ctx, owner, root, directory, name)
}

type nativeCaptureAdapterFixture struct {
	nativeCaptureOutputFixture
	images  *nativeCaptureAdapterImagesFixture
	memory  *nativeCaptureMemoryFixture
	store   *nativeCaptureStorageFixture
	machine *nativeMachineFixture
	handle  *nativeMachineHandleFixture
	spec    SnapshotSpec
}

func nativeCaptureAdapter(t *testing.T) nativeCaptureAdapterFixture {
	t.Helper()
	q, frame, ctx := nativeQualificationFixture(t)
	incoming, err := q.claim(ctx, frame)
	if err != nil {
		t.Fatal(err)
	}
	lease := qualificationLease(frame.InstanceID)
	if err := q.owner.prepare(nativeQualificationContext(ctx, incoming), lease); err != nil {
		t.Fatal(err)
	}
	incoming, err = q.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	source := nativeExecutableFixtureFile(t, "capture-selected", []byte("\x7fELF-original-firecracker-fixture"))
	if err := q.owner.captureExecutable(ctx, lease, source); err != nil {
		t.Fatal(err)
	}
	owner, err := q.owner.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	owner.Authorized, owner.PID, owner.StartTime = true, 42, 101
	if err := q.owner.write(owner); err != nil {
		t.Fatal(err)
	}
	capture := nativeQualificationCaptureRecord{Version: 1, InstanceID: lease.Instance, CaptureID: incoming.Generation,
		NativeGeneration: incoming.NativeGeneration, KernelBootID: incoming.KernelBootID, StartedAt: q.clock().UTC()}
	if err := q.writeCapture(incoming, capture); err != nil {
		t.Fatal(err)
	}
	b := &nativeSnapshotOutputFixture{nativeWritableImageFixture: &nativeWritableImageFixture{
		nativeImageBackendFixture: newNativeImageBackendFixture(), clones: make(map[nativeLoopIdentity]*nativeImageBackendFixture)}}
	images := &nativeCaptureAdapterImagesFixture{nativeCaptureFilesFixture: &nativeCaptureFilesFixture{
		nativeCaptureReaderFixture: &nativeCaptureReaderFixture{nativeSnapshotOutputFixture: b}}}
	images.freeze = func(_ context.Context, input *os.File, directory string) (*os.File, error) {
		return nativeFrozenDescriptorFixture(t, input, directory), nil
	}
	memory := &nativeCaptureMemoryFixture{owner: owner}
	q.owner.imageSources = images
	j := &nativeImageSourceJournal{owner: q.owner, backend: images}
	base := filepath.Dir(q.owner.root)
	if err := os.Chmod(base, 0o700); err != nil {
		t.Fatal(err)
	}
	v := NewJailerVMM(base, time.Second)
	proc := t.TempDir()
	dir := writeNativeRecoveryProcess(t, proc, owner.PID, lease.Instance, lease.UID, owner.StartTime)
	if err := os.Symlink(source, filepath.Join(dir, "exe")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cgroup"), []byte("0::/"+ParentCgroupFor(lease.Plan)+"/"+lease.Instance+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	handle := &nativeMachineHandleFixture{}
	r := &nativeProcessRecoveryRuntime{journal: q.owner, imageSources: images, snapshotMemory: memory, snapshotDirectory: t.TempDir(), owned: make(map[string]string),
		retirer: nativeProcessRetirer{probe: nativeProcessProbe{root: proc}, open: func(int) (nativeProcessHandle, error) { return handle, nil }}}
	if err := os.Chmod(r.snapshotDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	v.nativeRecovery = r
	if err := r.acquireDaemonOwnership(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.daemonLock.Close(); err != nil {
			t.Error(err)
		}
	})
	r.remember(owner)
	f := nativeCaptureOutputFixture{v: v, q: q, j: j, owner: owner, incoming: incoming, capture: capture, b: b,
		root: v.chrootRoot(lease.Instance), directory: r.snapshotDirectory, ctx: nativeSnapshotCaptureContext(ctx, incoming, capture, owner)}
	preparedOwner := owner
	preparedOwner.Authorized, preparedOwner.PID, preparedOwner.StartTime = false, 0, 0
	if _, err := j.stageOwned(f.ctx, owner, f.root, layerImageName, false, 0,
		func(nativeLaunchRecord) (nativeLaunchRecord, error) { return preparedOwner, nil },
		func(owner nativeLaunchRecord) (nativeImagePreparation, error) {
			return images.prepareReal(f.ctx, owner, f.root, f.directory, layerImageName)
		}); err != nil {
		t.Fatal(err)
	}
	local, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := &nativeCaptureStorageFixture{StorageBackend: local, put: local.Put}
	v.WithStorage(store)
	machine := &nativeMachineFixture{machine: Machine{VcpuCount: 1, MemSizeMib: lease.MemoryMaxMiB}, id: lease.Instance, state: "Running", raw: make(map[string]string), status: make(map[string]int)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPut && req.URL.Path == "/snapshot/create" {
			var body struct {
				SnapshotType string `json:"snapshot_type"`
				Memory       string `json:"mem_file_path"`
				VMState      string `json:"snapshot_path"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.SnapshotType != "Full" ||
				body.Memory != "capture-"+incoming.Generation+"-mem" || body.VMState != "capture-"+incoming.Generation+"-vmstate" ||
				machine.observedState() != "Paused" || !memory.widened.Load() {
				http.Error(w, "capture lacks original paused machine or memory fence", http.StatusBadRequest)
				return
			}
			for _, name := range []string{body.Memory, body.VMState} {
				record, _, err := j.captureOutput(owner, f.root, name)
				if err != nil {
					http.Error(w, "missing capture output", http.StatusInternalServerError)
					return
				}
				if err := os.WriteFile(j.anchor(record), []byte("captured-state"), 0o600); err != nil {
					t.Error(err)
					return
				}
				if name == body.Memory {
					if err := os.Truncate(j.anchor(record), int64(lease.MemoryMaxMiB)<<20); err != nil {
						t.Error(err)
						return
					}
				}
			}
		}
		machine.ServeHTTP(w, req)
	}))
	t.Cleanup(server.Close)
	r.machineDial = func(ctx context.Context, _ string, _ nativeLaunchRecord) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	}
	keys := qualificationSnapshotProof(incoming, SnapshotInfo{})
	spec := SnapshotSpec{StorageKey: keys.StorageKey, VMStateStorageKey: keys.VMStateStorageKey, ResumeBeforePublish: true, BeforeResume: func(ctx context.Context) error {
		if memory.widened.Load() || images.freezes != 1 {
			return errors.New("fixture: resume preceded ordinary memory or drive freeze")
		}
		memory.events = append(memory.events, "admit")
		return ctx.Err()
	}}
	return nativeCaptureAdapterFixture{nativeCaptureOutputFixture: f, images: images, memory: memory, store: store, machine: machine, handle: handle, spec: spec}
}

func TestNativeCaptureAdapterCompletesPinnedOriginalWarmCapture(t *testing.T) {
	f := nativeCaptureAdapter(t)
	f.store.put = func(ctx context.Context, key string, reader io.Reader) error {
		if f.machine.observedState() != "Running" || f.memory.widened.Load() || f.handle.closed {
			return errors.New("capture published outside original resumed session")
		}
		wait, cancel := context.WithTimeout(ctx, 25*time.Millisecond)
		defer cancel()
		lock, err := f.j.owner.lock(wait, f.owner.Lease.Instance)
		if lock != nil {
			_ = lock.Close()
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			return errors.New("capture released original machine ownership during upload")
		}
		return f.store.StorageBackend.Put(ctx, key, reader)
	}
	info, err := f.v.SnapshotKeepAlive(f.ctx, f.owner.Lease, f.spec)
	if err != nil || info.Inputs == nil || info.Inputs.Validate(f.spec.StorageKey) != nil || len(f.store.keys) != 3 || f.machine.patchCount() != 2 {
		t.Fatalf("capture failed: info=%+v keys=%v patches=%d err=%v", info, f.store.keys, f.machine.patchCount(), err)
	}
	if !reflect.DeepEqual(f.memory.events, []string{"open", "widen", "restore", "admit", "restore", "close"}) || !f.handle.closed || f.memory.widened.Load() {
		t.Fatalf("memory/session cleanup order: %v closed=%v widened=%v", f.memory.events, f.handle.closed, f.memory.widened.Load())
	}
	if capture, err := f.q.readCapture(f.incoming); err != nil || !capture.CompletedAt.IsZero() {
		t.Fatal("VMM borrowed Manager completion", err)
	}
	assertNativeCaptureFilesClosed(t, f.images.nativeCaptureFilesFixture)
}

func TestNativeCaptureAdapterRefusesUnqualifiedOrRedirectedCapture(t *testing.T) {
	for _, change := range []string{"context", "keys", "checkpoint", "park", "admission", "host_path", "placement", "memory_backend", "directory"} {
		t.Run(change, func(t *testing.T) {
			f := nativeCaptureAdapter(t)
			ctx, spec := f.ctx, f.spec
			switch change {
			case "context":
				ctx = t.Context()
			case "keys":
				spec.StorageKey += "-redirected"
			case "checkpoint":
				spec.BeforeCheckpoint = true
			case "park":
				spec.ResumeBeforePublish = false
			case "admission":
				spec.BeforeResume = nil
			case "host_path":
				spec.StageMemPath = "/tmp/borrowed"
			case "placement":
				f.images.placementErr = errors.New("different disk")
			case "memory_backend":
				f.v.nativeRecovery.snapshotMemory = nil
			case "directory":
				f.v.nativeRecovery.snapshotDirectory = "/"
			}
			if info, err := f.v.SnapshotKeepAlive(ctx, f.owner.Lease, spec); err == nil || info != (SnapshotInfo{}) || f.b.prepares != 1 || len(f.machine.requests) != 0 || len(f.memory.events) != 0 || len(f.store.keys) != 0 {
				t.Fatalf("rejected capture reached effects: info=%+v err=%v preparations=%d requests=%v memory=%v", info, err, f.b.prepares, f.machine.requests, f.memory.events)
			}
		})
	}
}

func TestNativeCaptureAdapterFailuresNeverCompleteOrLeakMemoryScope(t *testing.T) {
	for _, failure := range []string{"open", "widen", "create", "freeze", "admission", "restore", "dead_task", "shape", "version", "upload", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			f := nativeCaptureAdapter(t)
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			injected := errors.New("injected native capture failure")
			switch failure {
			case "open":
				f.memory.openErr = injected
			case "widen":
				f.memory.widenErr = injected
			case "create":
				f.machine.status["PUT /snapshot/create"] = http.StatusInternalServerError
			case "freeze":
				f.images.freeze = func(context.Context, *os.File, string) (*os.File, error) { return nil, injected }
			case "admission":
				f.spec.BeforeResume = func(context.Context) error { return injected }
			case "restore":
				f.memory.restoreErr = injected
			case "dead_task", "shape", "version":
				f.machine.hook = func(key string) {
					if key != "PUT /snapshot/create" {
						return
					}
					switch failure {
					case "dead_task":
						f.handle.setLiveError(injected)
					case "shape":
						f.machine.mu.Lock()
						f.machine.machine.MemSizeMib++
						f.machine.mu.Unlock()
					case "version":
						f.machine.mu.Lock()
						f.machine.raw["GET /"] = `{"id":"` + f.owner.Lease.Instance + `","app_name":"Firecracker","vmm_version":"1.8.0","state":"Paused"}`
						f.machine.mu.Unlock()
					}
				}
			case "upload":
				f.store.put = func(context.Context, string, io.Reader) error { return injected }
			case "cancel":
				f.memory.widen = cancel
			}
			if info, err := f.v.SnapshotKeepAlive(ctx, f.owner.Lease, f.spec); err == nil || info != (SnapshotInfo{}) {
				t.Fatal("failed native capture returned evidence", info, err)
			}
			if capture, err := f.q.readCapture(f.incoming); err != nil || !capture.CompletedAt.IsZero() {
				t.Fatal("failed native capture gained completion", err)
			}
			if len(f.memory.events) == 0 || f.memory.events[len(f.memory.events)-1] != "close" || !f.handle.closed {
				t.Fatalf("failed memory/session escaped: %v closed=%v", f.memory.events, f.handle.closed)
			}
			if failure == "restore" {
				owner, err := f.q.owner.read(f.owner.Lease.Instance)
				if err != nil || !owner.Revoked || owner.ExitConfirmed || owner.ResourcesRemoved || f.v.nativeRecovery.generation(f.owner.Lease.Instance) != "" || f.machine.patchCount() != 1 {
					t.Fatal("uncertain memory allowed resume or released resources", err)
				}
				if err := f.v.ResumeVM(f.ctx, f.owner.Lease); err == nil || f.machine.patchCount() != 1 {
					t.Fatal("quarantine allowed error-resume", err)
				}
			} else if f.memory.widened.Load() {
				t.Fatal("failed capture left memory widened")
			}
			assertNativeCaptureFilesClosed(t, f.images.nativeCaptureFilesFixture)
		})
	}
}

func TestNativeCaptureAdapterManagerPublishesBackingAndReplaysReceipt(t *testing.T) {
	f := nativeCaptureAdapter(t)
	path, err := f.q.capturePath(f.owner.Lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	frame := f.incoming.Execution
	// Model completed startup for an original live VM. These capabilities must
	// be present, but entering recovery or resource removal here is a failure.
	r := f.v.nativeRecovery
	r.support = func() error { return errors.New("fixture: unexpected recovery support check") }
	r.resources = func(Lease, netns.Config) error { return errors.New("fixture: unexpected resource removal") }
	r.inventory = func([]Lease) error { return errors.New("fixture: unexpected inventory") }
	m := NewManager(nil, f.v, Paths{}, "test-fc", nil, nil).WithNativeQualificationNodeID(frame.NodeID)
	m.WithStorage(f.store)
	m.live[frame.InstanceID] = &Instance{Lease: f.owner.Lease, Method: WakeColdBoot, nativeGeneration: f.owner.Generation, AppID: frame.AppID, DeploymentID: frame.DeploymentID}
	m.instanceBacking = map[string]BackingIdentity{frame.InstanceID: {Version: backingIdentityVersion, Kernel: "sha256:" + strings.Repeat("a", 64), Base: "sha256:" + strings.Repeat("b", 64), Timer: strings.TrimSpace(guestTimerArgs)}}
	m.nativeRecoveryReady = true
	// Manager creates a fresh durable start and its own original capture permit.
	ctx := nativeQualificationContext(t.Context(), f.incoming)
	proof, err := m.CaptureEnvironmentQualification(ctx, frame)
	if err != nil || proof != qualificationSnapshotProof(f.incoming, SnapshotInfo{MemBytes: int64(f.owner.Lease.MemoryMaxMiB) << 20, VMStateBytes: 14, StoredBytes: (int64(f.owner.Lease.MemoryMaxMiB) << 20) + 14 + 14}) {
		t.Fatalf("Manager capture proof: %+v %v", proof, err)
	}
	capture, err := f.q.readCapture(f.incoming)
	if err != nil || capture.CompletedAt.IsZero() || capture.Info.Inputs == nil || capture.Backing.Inputs == nil || !reflect.DeepEqual(capture.Info.Inputs, capture.Backing.Inputs) || len(f.store.keys) != 4 {
		t.Fatal("Manager omitted coupled provenance", err)
	}
	reader, err := f.store.Get(ctx, proof.BackingStorageKey)
	if err != nil {
		t.Fatal(err)
	}
	var backing BackingIdentity
	decodeErr := json.NewDecoder(reader).Decode(&backing)
	if err := errors.Join(decodeErr, reader.Close()); err != nil || !reflect.DeepEqual(backing, capture.Backing) {
		t.Fatal("backing differs from completion", err)
	}
	patches := f.machine.patchCount()
	repeated, err := m.CaptureEnvironmentQualification(ctx, frame)
	if err != nil || repeated != proof || f.machine.patchCount() != patches || len(f.store.keys) != 4 {
		t.Fatal("completed capture repeated effects", err)
	}
	if m.hasInstanceOperation(frame.InstanceID) {
		t.Fatal("Manager capture flight escaped")
	}
	assertNativeCaptureFilesClosed(t, f.images.nativeCaptureFilesFixture)
}
