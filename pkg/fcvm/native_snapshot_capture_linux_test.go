//go:build linux

// adr: 568 — modeled protocol tests do not supply Firecracker acceptance.
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
)

type nativeCaptureSequenceFixture struct {
	nativeCaptureOutputFixture
	events  []string
	fail    string
	after   func(string) error
	drive   *os.File
	frozen  *os.File
	store   *nativeCaptureSequenceStore
	backing BackingIdentity
	t       *testing.T
}

func (f *nativeCaptureSequenceFixture) step(name string) error {
	f.events = append(f.events, name)
	if f.after != nil {
		if err := f.after(name); err != nil {
			return err
		}
	}
	if f.fail == name {
		return errors.New("modeled lost acknowledgement: " + name)
	}
	return f.ctx.Err()
}

type nativeCaptureSequenceImages struct {
	*nativeSnapshotOutputFixture
	f *nativeCaptureSequenceFixture
}

func (b *nativeCaptureSequenceImages) HandoffSnapshotOutputs(context.Context, *JailerVMM, nativeLaunchRecord) error {
	return b.f.step("handoff") // Modeled ordering only; no namespace acceptance.
}

func (*nativeCaptureSequenceImages) CheckSnapshotOutputHandoff(context.Context, *JailerVMM) error {
	return nil
}

func (b *nativeCaptureSequenceImages) CheckSnapshotCaptureDirectory(directory string) error {
	if directory != b.f.directory || b.f.fail == "directory" {
		return errors.New("modeled original disk directory changed")
	}
	return nil
}

func (*nativeCaptureSequenceImages) CheckSnapshotCaptureRoot(context.Context, nativeLaunchRecord, string) error {
	return nil // Modeled fixture; real PID/chroot acceptance is a separate metal test.
}

// Named fixture creation models a descriptor already owned by a producer. It
// is not evidence for Linux anonymous birth, bind mounts or persistent staging.
type nativeCaptureSequencePreparation struct {
	nativeImagePreparation
	file     *os.File
	identity nativeLoopIdentity
}

func (p *nativeCaptureSequencePreparation) Identity() nativeLoopIdentity { return p.identity }
func (p *nativeCaptureSequencePreparation) CreateAnchor(point string, publish func(nativeLoopIdentity) error) (uint64, error) {
	if err := os.Link(p.file.Name(), point); err != nil {
		return 0, err
	}
	return p.nativeImagePreparation.CreateAnchor(point, publish)
}
func (p *nativeCaptureSequencePreparation) Close() error {
	return errors.Join(p.nativeImagePreparation.Close(), p.file.Close())
}

func (b *nativeCaptureSequenceImages) prepare(ctx context.Context, owner nativeLaunchRecord, root, directory, name string) (nativeImagePreparation, error) {
	p, err := b.nativeWritableImageFixture.PrepareWritable(ctx, owner, root, directory, name)
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(directory, "modeled-producer-")
	if err != nil {
		return nil, errors.Join(err, p.Close())
	}
	info, err := file.Stat()
	if err != nil {
		return nil, errors.Join(err, file.Close(), p.Close())
	}
	identity, err := resourceFileID(info)
	if err != nil {
		return nil, errors.Join(err, file.Close(), p.Close())
	}
	id := nativeLoopIdentity{Device: identity.Device, Inode: identity.Inode}
	b.clones[id] = b.clones[p.Identity()]
	delete(b.clones, p.Identity())
	return &nativeCaptureSequencePreparation{nativeImagePreparation: p, file: file, identity: id}, nil
}

func (b *nativeCaptureSequenceImages) PrepareSnapshotOutput(ctx context.Context, owner nativeLaunchRecord, root, directory, name string) (nativeImagePreparation, error) {
	kind := "mem"
	if strings.HasSuffix(name, "-vmstate") {
		kind = "vmstate"
	}
	if err := b.f.step("stage:" + kind); err != nil {
		return nil, err
	}
	return b.prepare(ctx, owner, root, directory, name)
}

func (b *nativeCaptureSequenceImages) OpenSnapshotInput(record nativeImageSourceRecord, ref nativeImageReference, point string) (*os.File, error) {
	if err := errors.Join(b.CheckReference(record, ref), b.CheckAnchor(record, point)); err != nil {
		return nil, err
	}
	var err error
	b.f.drive, err = os.Open(point)
	return b.f.drive, err
}

func (b *nativeCaptureSequenceImages) FreezeSnapshotDrive(_ context.Context, input *os.File, directory string) (*os.File, error) {
	b.f.frozen = nativeFrozenDescriptorFixture(b.f.t, input, directory)
	return b.f.frozen, b.f.step("freeze")
}

type nativeCaptureSequenceControl struct{ f *nativeCaptureSequenceFixture }

func (b nativeCaptureSequenceControl) Request(_ context.Context, socket string, owner nativeLaunchRecord, method, path string, body any) error {
	f := b.f
	if !sameNativeSnapshotProcess(owner, f.owner) || socket != f.v.socketPath(owner.Lease.Instance) {
		return errors.New("modeled control lost original physical process")
	}
	action := "create"
	if path == "/vm" {
		action = "pause"
		if body.(map[string]any)["state"] == "Resumed" {
			action = "resume"
		}
		if method != http.MethodPatch {
			return errors.New("modeled VM control changed method")
		}
	} else {
		memory, _ := nativeSnapshotOutputName(f.capture.CaptureID, "mem")
		vmstate, _ := nativeSnapshotOutputName(f.capture.CaptureID, "vmstate")
		want := map[string]any{"snapshot_type": "Full", "snapshot_path": vmstate, "mem_file_path": memory}
		if method != http.MethodPut || path != "/snapshot/create" || !reflect.DeepEqual(body, want) {
			return errors.New("modeled snapshot used caller paths or another capture")
		}
		for _, kind := range []string{"mem", "vmstate"} {
			name, _ := nativeSnapshotOutputName(f.capture.CaptureID, kind)
			record, _, err := f.j.captureOutput(owner, f.root, name)
			if err != nil {
				return err
			}
			if err := os.WriteFile(f.j.anchor(record), []byte("original-"+kind), 0o600); err != nil {
				return err
			}
		}
	}
	return f.step(action)
}

type nativeCaptureSequenceIntent struct {
	*nativePublicationIntentFixture
	f *nativeCaptureSequenceFixture
}

func (j *nativeCaptureSequenceIntent) Begin(ctx context.Context, intent nativeSnapshotPublicationIntent) (nativeSnapshotPublicationIntent, error) {
	r, err := j.nativePublicationIntentFixture.Begin(ctx, intent)
	if err != nil {
		return r, err
	}
	return r, errors.Join(err, j.f.step("begin"))
}

type nativeCaptureSequenceStore struct {
	*memStorage
	f           *nativeCaptureSequenceFixture
	unsupported string
	ordinary    int
	deletes     int
}

func (b *nativeCaptureSequenceStore) CheckExclusivePut(ctx context.Context, key string) error {
	if strings.HasSuffix(key, "/"+b.unsupported) {
		return storage.ErrExclusivePutUnsupported
	}
	return ctx.Err()
}

func (b *nativeCaptureSequenceStore) PutExclusive(ctx context.Context, key string, reader io.Reader, size int64) error {
	if _, found := b.blobs[key]; found {
		return storage.ErrArtifactExists
	}
	if filepath.Base(key) != "backing" {
		file, ok := reader.(*os.File)
		if !ok {
			return errors.New("modeled publication lost pinned descriptor")
		}
		if _, err := file.WriteAt([]byte("mutation"), 0); err == nil {
			return errors.New("modeled publication acquired a writable descriptor")
		}
	}
	body, err := io.ReadAll(reader)
	if err != nil || int64(len(body)) != size {
		return errors.Join(err, errors.New("modeled publication changed original size"))
	}
	b.blobs[key] = body // Lost acknowledgement deliberately retains the object.
	return errors.Join(b.f.step("put:"+filepath.Base(key)), ctx.Err())
}

func (b *nativeCaptureSequenceStore) Put(context.Context, string, io.Reader) error {
	b.ordinary++
	return errors.New("native capture borrowed ordinary publication")
}
func (b *nativeCaptureSequenceStore) Delete(context.Context, string) error {
	b.deletes++
	return errors.New("native capture borrowed unowned cleanup")
}

func nativeCaptureSequence(t *testing.T) *nativeCaptureSequenceFixture {
	t.Helper()
	f := &nativeCaptureSequenceFixture{nativeCaptureOutputFixture: nativeCaptureOutputsFixture(t), t: t,
		backing: BackingIdentity{Version: 1, Kernel: "sha256:" + strings.Repeat("a", 64), Base: "sha256:" + strings.Repeat("b", 64)}}
	b := &nativeCaptureSequenceImages{nativeSnapshotOutputFixture: f.b, f: f}
	f.v.nativeImageStagingRoot = f.directory
	f.v.nativeRecovery.imageSources, f.q.owner.imageSources, f.j.backend = b, b, b
	f.v.nativeRecovery.snapshotControl = nativeCaptureSequenceControl{f: f}
	f.v.nativeRecovery.snapshotMemory = nativeModeledSnapshotMemoryBackend{effect: f.step}
	f.v.nativeRecovery.publications = &nativeCaptureSequenceIntent{nativePublicationIntentFixture: &nativePublicationIntentFixture{}, f: f}
	f.store = &nativeCaptureSequenceStore{memStorage: &memStorage{blobs: make(map[string][]byte)}, f: f}
	f.v.storage = f.store
	prepared := f.owner
	prepared.Authorized, prepared.PID, prepared.StartTime = false, 0, 0
	if err := f.q.owner.write(prepared); err != nil {
		t.Fatal(err)
	}
	if _, err := f.j.stageOwned(f.ctx, prepared, f.root, layerImageName, false, 0,
		func(owner nativeLaunchRecord) (nativeLaunchRecord, error) { return owner, nil },
		func(owner nativeLaunchRecord) (nativeImagePreparation, error) {
			return b.prepare(f.ctx, owner, f.root, f.directory, layerImageName)
		}); err != nil {
		t.Fatal(err)
	}
	if err := f.q.owner.write(f.owner); err != nil {
		t.Fatal(err)
	}
	drive, _, err := f.j.snapshotDrive(f.owner, f.root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.j.anchor(drive), []byte("original-private-drive"), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestNativeCaptureSequencePublishesOnlyAfterFreezeAndResume(t *testing.T) {
	f := nativeCaptureSequence(t)
	info, err := f.v.captureEnvironmentQualificationSnapshot(f.ctx, f.owner.Lease, f.backing)
	want := []string{"begin", "stage:mem", "stage:vmstate", "handoff", "memory:prepare", "pause", "memory:raise", "create", "freeze", "memory:restore", "resume", "put:mem", "put:vmstate", "put:drive", "put:backing"}
	if err != nil || !reflect.DeepEqual(f.events, want) || info != (SnapshotInfo{MemBytes: 12, VMStateBytes: 16, StoredBytes: 50}) {
		t.Fatalf("capture order/info: %v %+v %v", f.events, info, err)
	}
	keys := qualificationSnapshotProof(f.incoming, SnapshotInfo{})
	if string(f.store.blobs[keys.DriveStorageKey]) != "original-private-drive" || len(f.store.blobs) != 4 {
		t.Fatal("capture omitted or substituted an original artifact")
	}
	var backing BackingIdentity
	if err := json.Unmarshal(f.store.blobs[keys.BackingStorageKey], &backing); err != nil || backing != f.backing {
		t.Fatal("capture sidecar did not retain boot backing identity", err)
	}
	assertNativeCaptureSequenceClosed(t, f)
}

func assertNativeCaptureSequenceClosed(t *testing.T, f *nativeCaptureSequenceFixture) {
	t.Helper()
	for _, file := range []*os.File{f.drive, f.frozen, f.b.output} {
		if file != nil {
			if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Fatal("capture retained a producer descriptor", err)
			}
		}
	}
	capture, err := f.q.readCapture(f.incoming)
	if err != nil || capture != f.capture || f.store.ordinary != 0 || f.store.deletes != 0 || f.v.checkEnvironmentQualificationSnapshotSupport() == nil {
		t.Fatal("internal producer supplied completion, legacy effects or an enabled native gate", err)
	}
}

func TestNativeCaptureSequenceUncertainStepNeverReplaysOrPromotes(t *testing.T) {
	for _, step := range []string{"begin", "stage:mem", "stage:vmstate", "handoff", "memory:prepare", "pause", "memory:raise", "create", "freeze", "memory:restore", "resume", "put:mem", "put:vmstate", "put:drive", "put:backing"} {
		t.Run(step, func(t *testing.T) {
			f := nativeCaptureSequence(t)
			f.fail = step
			info, err := f.v.captureEnvironmentQualificationSnapshot(f.ctx, f.owner.Lease, f.backing)
			if err == nil || info != (SnapshotInfo{}) || !nativeCaptureStoppedAt(f.events, step) {
				t.Fatal("uncertain step continued effects or returned capture evidence", f.events, info, err)
			}
			assertNativeCaptureSequenceClosed(t, f)
			before := append([]string(nil), f.events...)
			f.fail = ""
			if info, err := f.v.captureEnvironmentQualificationSnapshot(f.ctx, f.owner.Lease, f.backing); err == nil || info != (SnapshotInfo{}) || !reflect.DeepEqual(f.events, before) {
				t.Fatal("retry replayed an uncertain capture effect", f.events, info, err)
			}
		})
	}
}

func TestNativeCaptureSequenceRejectsIncompletePreflightBeforeIntentOrEffects(t *testing.T) {
	for _, change := range []string{"backing", "control", "images", "directory", "memory_allowance", "memory_backend", "drive_backend", "backing_backend", "capture", "generation", "revoked", "cancelled"} {
		t.Run(change, func(t *testing.T) {
			f := nativeCaptureSequence(t)
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			switch change {
			case "backing":
				f.backing = BackingIdentity{}
			case "control":
				f.v.nativeRecovery.snapshotControl = nil
			case "images":
				f.v.nativeRecovery.imageSources = f.b
			case "directory":
				f.fail = "directory"
			case "memory_allowance":
				f.v.nativeRecovery.snapshotMemory = nil
			case "memory_backend":
				f.store.unsupported = "mem"
			case "drive_backend":
				f.store.unsupported = "drive"
			case "backing_backend":
				f.store.unsupported = "backing"
			case "capture":
				ctx = t.Context()
			case "generation":
				delete(f.v.nativeRecovery.owned, f.owner.Lease.Instance)
			case "revoked":
				owner := f.owner
				owner.Revoked = true
				if err := f.q.owner.write(owner); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				cancel()
			}
			if info, err := f.v.captureEnvironmentQualificationSnapshot(ctx, f.owner.Lease, f.backing); err == nil || info != (SnapshotInfo{}) || len(f.events) != 0 || len(f.store.blobs) != 0 {
				t.Fatal("incomplete native preflight started a capture", f.events, info, err)
			}
		})
	}
}

func TestNativeCaptureSequenceRetainsPhysicalAndDriveLocksThroughPublication(t *testing.T) {
	f := nativeCaptureSequence(t)
	drive, _, err := f.j.snapshotDrive(f.owner, f.root)
	if err != nil {
		t.Fatal(err)
	}
	f.after = func(step string) error {
		if !strings.HasPrefix(step, "put:") && step != "freeze" && step != "resume" {
			return nil
		}
		for _, acquire := range []func(context.Context) (*os.File, error){
			func(ctx context.Context) (*os.File, error) { return f.q.owner.lock(ctx, f.owner.Lease.Instance) },
			func(ctx context.Context) (*os.File, error) { return f.j.lock(ctx, drive.Identity) },
		} {
			ctx, cancel := context.WithTimeout(f.ctx, 15*time.Millisecond)
			lock, err := acquire(ctx)
			cancel()
			if lock != nil {
				_ = lock.Close()
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				return errors.New("capture released original physical/drive authority before IO joined")
			}
		}
		return nil
	}
	if _, err := f.v.captureEnvironmentQualificationSnapshot(f.ctx, f.owner.Lease, f.backing); err != nil {
		t.Fatal(err)
	}
	assertNativeCaptureSequenceClosed(t, f)
}

func TestNativeCaptureSequenceCancellationAndAuthorityLossPreventCompletion(t *testing.T) {
	for _, step := range []string{"stage:mem", "stage:vmstate", "handoff", "memory:prepare", "pause", "memory:raise", "create", "freeze", "memory:restore", "resume", "put:mem", "put:backing"} {
		for _, change := range []string{"cancelled", "intent", "physical", "generation"} {
			t.Run(step+"/"+change, func(t *testing.T) {
				f := nativeCaptureSequence(t)
				ctx, cancel := context.WithCancel(f.ctx)
				defer cancel()
				f.after = func(name string) error {
					if name != step {
						return nil
					}
					switch change {
					case "cancelled":
						cancel()
					case "intent":
						f.v.nativeRecovery.publications.(*nativeCaptureSequenceIntent).intent.Physical.StartTime++
					case "physical":
						owner := f.owner
						owner.StartTime++
						return f.q.owner.write(owner)
					case "generation":
						delete(f.v.nativeRecovery.owned, f.owner.Lease.Instance)
					}
					return nil
				}
				info, err := f.v.captureEnvironmentQualificationSnapshot(ctx, f.owner.Lease, f.backing)
				if err == nil || info != (SnapshotInfo{}) || !nativeCaptureStoppedAt(f.events, step) {
					t.Fatal("authority loss continued effects or supplied evidence", f.events, info, err)
				}
				assertNativeCaptureSequenceClosed(t, f)
			})
		}
	}
}

func TestNativeCaptureSequencePinsCanonicalBackendForEntireCohort(t *testing.T) {
	f := nativeCaptureSequence(t)
	replacement := &nativeCaptureSequenceStore{memStorage: &memStorage{blobs: make(map[string][]byte)}, f: f}
	f.after = func(step string) error {
		if step == "stage:mem" {
			f.v.storage = replacement
		}
		return nil
	}
	if _, err := f.v.captureEnvironmentQualificationSnapshot(f.ctx, f.owner.Lease, f.backing); err != nil || len(f.store.blobs) != 4 || len(replacement.blobs) != 0 {
		t.Fatal("capture cohort borrowed a replacement canonical backend", err)
	}
	assertNativeCaptureSequenceClosed(t, f)
}

func TestNativeCaptureSequenceDescriptorCloseFailureRetainsIncompleteCohort(t *testing.T) {
	for _, kind := range []string{"drive", "frozen"} {
		t.Run(kind, func(t *testing.T) {
			f := nativeCaptureSequence(t)
			f.after = func(step string) error {
				if step != "put:backing" {
					return nil
				}
				if kind == "drive" {
					return f.drive.Close()
				}
				return f.frozen.Close()
			}
			info, err := f.v.captureEnvironmentQualificationSnapshot(f.ctx, f.owner.Lease, f.backing)
			if err == nil || info != (SnapshotInfo{}) || len(f.store.blobs) != 4 {
				t.Fatal("descriptor close failure promoted or deleted uncertain objects", info, err)
			}
			assertNativeCaptureSequenceClosed(t, f)
		})
	}
}

func nativeCaptureStoppedAt(events []string, step string) bool {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i] == step {
			return true
		}
		if events[i] != "memory:restore" {
			return false
		}
	}
	return false
}
