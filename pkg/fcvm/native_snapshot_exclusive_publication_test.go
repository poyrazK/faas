//go:build linux || darwin

// adr: 568 — modeled publication fixtures are not native capture acceptance.
package fcvm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
)

type nativeExclusivePublicationFixture struct {
	*memStorage
	checks      []string
	puts        int
	unsupported string
	consume     func(context.Context, *os.File) error
}

func (b *nativeExclusivePublicationFixture) CheckExclusivePut(ctx context.Context, key string) error {
	b.checks = append(b.checks, key)
	if strings.HasSuffix(key, "/"+b.unsupported) {
		return storage.ErrExclusivePutUnsupported
	}
	return ctx.Err()
}

func (b *nativeExclusivePublicationFixture) PutExclusive(ctx context.Context, key string, reader io.Reader, size int64) error {
	b.puts++
	file, ok := reader.(*os.File)
	if !ok {
		return errors.New("fixture: publication lost its original pinned descriptor")
	}
	if _, err := file.WriteAt([]byte("mutation"), 0); err == nil {
		return errors.New("fixture: publication supplied writable authority")
	}
	if b.consume != nil {
		if err := b.consume(ctx, file); err != nil {
			return err
		}
	}
	if _, present := b.blobs[key]; present {
		return storage.ErrArtifactExists
	}
	body, err := io.ReadAll(file)
	if err != nil || int64(len(body)) != size {
		return errors.Join(err, errors.New("fixture: publication changed its original size"))
	}
	b.blobs[key] = body
	return nil
}

type nativePublicationIntentFixture struct {
	intent         nativeSnapshotPublicationIntent
	err            error
	writeErr       error
	objects        map[string]nativeSnapshotPublicationObjectReceipt
	objectWriteErr error
}

func (j *nativePublicationIntentFixture) Acquire(context.Context) error { return j.err }
func (j *nativePublicationIntentFixture) Check() error                  { return j.err }
func (*nativePublicationIntentFixture) RecoverPendingRetirements(context.Context, storage.StorageBackend) error {
	return nil
}
func (j *nativePublicationIntentFixture) Begin(ctx context.Context, r nativeSnapshotPublicationIntent) (nativeSnapshotPublicationIntent, error) {
	if j.intent.Version != 0 {
		return r, storage.ErrArtifactExists
	}
	r.Directory = nativeLoopIdentity{Device: 1, Inode: 42}
	r.File = nativeLoopIdentity{Device: 1, Inode: 43}
	j.intent = r
	return r, errors.Join(j.writeErr, ctx.Err())
}
func (j *nativePublicationIntentFixture) Require(ctx context.Context, r nativeSnapshotPublicationIntent) error {
	if r != j.intent {
		return errors.New("original durable intent changed")
	}
	return errors.Join(j.err, ctx.Err())
}

func nativeExclusivePublicationStore(t *testing.T, f *nativeCaptureOutputFixture) *nativeExclusivePublicationFixture {
	t.Helper()
	b := &nativeExclusivePublicationFixture{memStorage: &memStorage{blobs: make(map[string][]byte)}}
	f.v.storage = b
	f.v.nativeRecovery.publications = &nativePublicationIntentFixture{}
	var err error
	f.ctx, err = f.v.beginNativeSnapshotPublication(f.ctx, f.owner.Lease)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestNativeExclusivePublicationRetainsOriginalKeysAndReaderOwnership(t *testing.T) {
	f, _ := nativeReadableCaptureFixture(t)
	b := nativeExclusivePublicationStore(t, &f)
	keys := qualificationSnapshotProof(f.incoming, SnapshotInfo{})
	for _, kind := range []string{"mem", "vmstate"} {
		if err := f.v.publishNativeSnapshotOutput(f.ctx, f.owner.Lease, kind); err != nil {
			t.Fatal(err)
		}
		if _, err := f.b.output.Stat(); !errors.Is(err, os.ErrClosed) {
			t.Fatal("publication reader escaped its original ownership boundary", err)
		}
		if err := f.v.publishNativeSnapshotOutput(f.ctx, f.owner.Lease, kind); !errors.Is(err, storage.ErrArtifactExists) {
			t.Fatal("publication replay replaced an original capture", err)
		}
	}
	if string(b.blobs[keys.StorageKey]) != "capture-"+f.capture.CaptureID+"-mem" ||
		string(b.blobs[keys.VMStateStorageKey]) != "capture-"+f.capture.CaptureID+"-vmstate" || len(b.blobs) != 2 {
		t.Fatal("publication substituted another capture namespace")
	}
	want := map[string]bool{keys.StorageKey: true, keys.VMStateStorageKey: true, keys.DriveStorageKey: true, keys.BackingStorageKey: true}
	seen := make(map[string]bool)
	for _, key := range b.checks {
		if !want[key] {
			t.Fatal("publication preflight borrowed another object namespace", key)
		}
		seen[key] = true
	}
	// Repeated preflights intentionally check the same four original keys.
	if len(seen) != 4 {
		t.Fatal("publication omitted cohort object preflight")
	}
	capture, err := f.q.readCapture(f.incoming)
	if err != nil || capture != f.capture || f.v.checkEnvironmentQualificationSnapshotSupport() == nil {
		t.Fatal("read/publication primitive completed or enabled native capture", err)
	}
}

func TestNativeExclusivePublicationRefusesUnsupportedOrStaleAuthorityBeforeOpening(t *testing.T) {
	for _, change := range []string{"no_persistent_begin", "drive_unsupported", "backing_unsupported", "no_capture", "kind", "revoked", "daemon_generation"} {
		t.Run(change, func(t *testing.T) {
			f, _ := nativeReadableCaptureFixture(t)
			b := nativeExclusivePublicationStore(t, &f)
			ctx, kind := f.ctx, "mem"
			switch change {
			case "no_persistent_begin":
				ctx = nativeSnapshotCaptureContext(t.Context(), f.incoming, f.capture, f.owner)
			case "drive_unsupported":
				b.unsupported = "drive"
			case "backing_unsupported":
				b.unsupported = "backing"
			case "no_capture":
				ctx = t.Context()
			case "kind":
				kind = "../mem"
			case "revoked":
				owner := f.owner
				owner.Revoked = true
				if err := f.q.owner.write(owner); err != nil {
					t.Fatal(err)
				}
			case "daemon_generation":
				delete(f.v.nativeRecovery.owned, f.owner.Lease.Instance)
			}
			if err := f.v.publishNativeSnapshotOutput(ctx, f.owner.Lease, kind); err == nil || f.b.opens != 0 || b.puts != 0 {
				t.Fatal("unsupported or stale publication opened a producer", err, f.b.opens, b.puts)
			}
		})
	}
}

func TestNativeExclusivePublicationJoinsFailuresAndRechecksOriginalAuthority(t *testing.T) {
	for _, outcome := range []string{"failed", "cancelled", "generation_changed", "incoming_changed", "daemon_lock_closed"} {
		t.Run(outcome, func(t *testing.T) {
			f, _ := nativeReadableCaptureFixture(t)
			b := nativeExclusivePublicationStore(t, &f)
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			b.consume = func(_ context.Context, _ *os.File) error {
				switch outcome {
				case "failed":
					return errors.New("original publication failed")
				case "cancelled":
					cancel()
				case "generation_changed":
					delete(f.v.nativeRecovery.owned, f.owner.Lease.Instance)
				case "incoming_changed":
					incoming := f.incoming
					incoming.Revoked = true
					return f.q.write(incoming)
				case "daemon_lock_closed":
					return f.v.nativeRecovery.daemonLock.Close()
				}
				return nil
			}
			if err := f.v.publishNativeSnapshotOutput(ctx, f.owner.Lease, "mem"); err == nil {
				t.Fatal("failed or changed publication supplied success")
			}
			if _, err := f.b.output.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Fatal("failed publication retained its reader", err)
			}
		})
	}
}

func TestNativeExclusivePublicationPinsPhysicalAndOutputLocksDuringStorageIO(t *testing.T) {
	f, memory := nativeReadableCaptureFixture(t)
	b := nativeExclusivePublicationStore(t, &f)
	b.consume = func(ctx context.Context, _ *os.File) error {
		for _, acquire := range []func(context.Context) (*os.File, error){
			func(ctx context.Context) (*os.File, error) { return f.q.owner.lock(ctx, f.owner.Lease.Instance) },
			func(ctx context.Context) (*os.File, error) { return f.j.lock(ctx, memory.Identity) },
		} {
			bounded, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
			lock, err := acquire(bounded)
			cancel()
			if err == nil {
				_ = lock.Close()
				t.Fatal("original native authority retired during storage IO")
			}
		}
		return nil
	}
	if err := f.v.publishNativeSnapshotOutput(f.ctx, f.owner.Lease, "mem"); err != nil {
		t.Fatal(err)
	}
}

func TestNativeExclusivePublicationPinsCanonicalBackendThroughWholeCohort(t *testing.T) {
	f, _ := nativeReadableCaptureFixture(t)
	original := nativeExclusivePublicationStore(t, &f)
	replacement := &nativeExclusivePublicationFixture{memStorage: &memStorage{blobs: make(map[string][]byte)}}
	f.v.storage = replacement
	for _, kind := range []string{"mem", "vmstate"} {
		if err := f.v.publishNativeSnapshotOutput(f.ctx, f.owner.Lease, kind); err != nil {
			t.Fatal(err)
		}
	}
	if original.puts != 2 || replacement.puts != 0 || len(replacement.checks) != 0 {
		t.Fatal("publication cohort borrowed a replacement backend", original.puts, replacement.puts)
	}
}

func TestNativeExclusivePublicationRequiresOriginalDurableBegin(t *testing.T) {
	for _, outcome := range []string{"missing_adapter", "unsupported_storage", "write_ack_lost", "intent_changed", "adapter_changed"} {
		t.Run(outcome, func(t *testing.T) {
			f, _ := nativeReadableCaptureFixture(t)
			backend := &nativeExclusivePublicationFixture{memStorage: &memStorage{blobs: make(map[string][]byte)}}
			f.v.storage = backend
			journal := &nativePublicationIntentFixture{}
			f.v.nativeRecovery.publications = journal
			switch outcome {
			case "missing_adapter":
				f.v.nativeRecovery.publications = nil
			case "unsupported_storage":
				f.v.storage = backend.memStorage
			case "write_ack_lost":
				journal.writeErr = errors.New("durable begin acknowledgement lost")
			}
			ctx, err := f.v.beginNativeSnapshotPublication(f.ctx, f.owner.Lease)
			if outcome == "intent_changed" || outcome == "adapter_changed" {
				if err != nil {
					t.Fatal(err)
				}
				if outcome == "intent_changed" {
					journal.intent.Physical.StartTime++
				} else {
					f.v.nativeRecovery.publications = &nativePublicationIntentFixture{intent: journal.intent}
				}
			} else if err == nil {
				t.Fatal("failed durable begin supplied publication capability")
			}
			if outcome == "write_ack_lost" && journal.intent.Version != 1 {
				t.Fatal("fixture did not preserve the uncertain durable intent")
			}
			if err := f.v.publishNativeSnapshotOutput(ctx, f.owner.Lease, "mem"); err == nil || f.b.opens != 0 || backend.puts != 0 {
				t.Fatal("unowned publication opened its source", err, f.b.opens, backend.puts)
			}
		})
	}
}

func (b *nativeExclusivePublicationFixture) CheckExclusiveArtifact(ctx context.Context, key string) error {
	return b.CheckExclusivePut(ctx, key)
}
func (b *nativeExclusivePublicationFixture) PutExclusiveArtifact(ctx context.Context, key string, reader io.Reader, size int64) (storage.ExclusiveArtifactReceipt, error) {
	if err := b.PutExclusive(ctx, key, reader, size); err != nil {
		return storage.ExclusiveArtifactReceipt{}, err
	}
	return nativeModeledArtifactReceipt(key, b.blobs[key]), nil
}
func (b *nativeExclusivePublicationFixture) GetExclusiveArtifact(context.Context, storage.ExclusiveArtifactReceipt) (io.ReadCloser, error) {
	return nil, errors.New("modeled fixture has no original generation reader")
}
func nativeModeledArtifactReceipt(key string, body []byte) storage.ExclusiveArtifactReceipt {
	digest := sha256.Sum256(body)
	return storage.ExclusiveArtifactReceipt{Version: 1, Key: key, ObjectKey: key, Backend: "gcs", Location: "modeled-native-artifacts", LogicalBytes: int64(len(body)), StoredBytes: int64(len(body)), SHA256: hex.EncodeToString(digest[:]), Generation: 1}
}
func (j *nativePublicationIntentFixture) RecordObject(ctx context.Context, intent nativeSnapshotPublicationIntent, kind string, object storage.ExclusiveArtifactReceipt) (nativeSnapshotPublicationObjectReceipt, error) {
	if err := j.Require(ctx, intent); err != nil {
		return nativeSnapshotPublicationObjectReceipt{}, err
	}
	if j.objects == nil {
		j.objects = make(map[string]nativeSnapshotPublicationObjectReceipt)
	}
	if _, present := j.objects[kind]; present {
		return nativeSnapshotPublicationObjectReceipt{}, storage.ErrArtifactExists
	}
	r := nativeSnapshotPublicationObjectReceipt{Version: 1, Directory: intent.Directory, File: nativeLoopIdentity{Device: 1, Inode: uint64(50 + len(j.objects))}, IntentFile: intent.File, CaptureID: intent.Capture.CaptureID, Kind: kind, Object: object}
	if err := r.validate(intent); err != nil {
		return nativeSnapshotPublicationObjectReceipt{}, err
	}
	j.objects[kind] = r
	if j.objectWriteErr != nil {
		return nativeSnapshotPublicationObjectReceipt{}, j.objectWriteErr
	}
	return r, ctx.Err()
}
func (j *nativePublicationIntentFixture) RequireObject(ctx context.Context, intent nativeSnapshotPublicationIntent, r nativeSnapshotPublicationObjectReceipt) error {
	if err := j.Require(ctx, intent); err != nil {
		return err
	}
	current, ok := j.objects[r.Kind]
	if !ok || current.File != r.File || current.Object.SHA256 != r.Object.SHA256 || current.Object.Key != r.Object.Key {
		return errors.New("modeled original object receipt changed")
	}
	return ctx.Err()
}

func TestNativeExclusivePublicationLostReceiptAcknowledgementCannotReplay(t *testing.T) {
	f, _ := nativeReadableCaptureFixture(t)
	b := nativeExclusivePublicationStore(t, &f)
	j := f.v.nativeRecovery.publications.(*nativePublicationIntentFixture)
	lost := errors.New("object receipt acknowledgement lost")
	j.objectWriteErr = lost
	if err := f.v.publishNativeSnapshotOutput(f.ctx, f.owner.Lease, "mem"); !errors.Is(err, lost) {
		t.Fatal("uncertain receipt supplied publication success", err)
	}
	if len(j.objects) != 1 || len(b.blobs) != 1 {
		t.Fatal("fixture did not retain uncertain receipt and object")
	}
	j.objectWriteErr = nil
	if err := f.v.publishNativeSnapshotOutput(f.ctx, f.owner.Lease, "mem"); !errors.Is(err, storage.ErrArtifactExists) {
		t.Fatal("uncertain receipt was adopted/replayed", err)
	}
	if len(j.objects) != 1 || len(b.blobs) != 1 {
		t.Fatal("replay changed original cohort")
	}
}
