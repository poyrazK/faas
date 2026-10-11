//go:build linux || darwin

// adr: 795 — modeled ownership and real descriptors do not qualify native capture.
package fcvm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/storage"
)

type nativeCaptureFilesFixture struct {
	*nativeCaptureReaderFixture
	opened  []*os.File
	freezes int
	freeze  func(context.Context, *os.File, string) (*os.File, error)
	input   func(string) (*os.File, error)
}

func (b *nativeCaptureFilesFixture) OpenSnapshotOutput(record nativeImageSourceRecord, ref nativeImageReference, point string) (*os.File, error) {
	file, err := b.nativeCaptureReaderFixture.OpenSnapshotOutput(record, ref, point)
	if file != nil {
		b.opened = append(b.opened, file)
	}
	return file, err
}

func (b *nativeCaptureFilesFixture) OpenSnapshotInput(record nativeImageSourceRecord, ref nativeImageReference, point string) (*os.File, error) {
	if err := errors.Join(b.CheckReference(record, ref), b.CheckAnchor(record, point)); err != nil {
		return nil, err
	}
	open := b.input
	if open == nil {
		open = os.Open
	}
	file, err := open(point)
	if file != nil {
		b.opened = append(b.opened, file)
	}
	return file, err
}

func (b *nativeCaptureFilesFixture) FreezeSnapshotDrive(ctx context.Context, input *os.File, directory string) (*os.File, error) {
	b.freezes++
	file, err := b.freeze(ctx, input, directory)
	if file != nil {
		b.opened = append(b.opened, file)
	}
	return file, err
}

type nativeCaptureStorageFixture struct {
	storage.StorageBackend
	keys []string
	put  func(context.Context, string, io.Reader) error
}

func (b *nativeCaptureStorageFixture) Put(ctx context.Context, key string, reader io.Reader) error {
	b.keys = append(b.keys, key)
	return b.put(ctx, key, reader)
}

func nativeCapturePublicationFixture(t *testing.T) (nativeCaptureOutputFixture, *nativeCaptureFilesFixture, *nativeCaptureStorageFixture, Machine) {
	t.Helper()
	f, reader := nativeCaptureReaderJournalFixture(t)
	// Add a third, private source to the modeled mount journal. The journal
	// fixture binds its actual inode to the retained anchor, not the jail path.
	prepared := f.owner
	prepared.Authorized, prepared.PID, prepared.StartTime = false, 0, 0
	if _, err := f.j.stageOwned(f.ctx, f.owner, f.root, layerImageName, false, 0,
		func(nativeLaunchRecord) (nativeLaunchRecord, error) { return prepared, nil },
		func(owner nativeLaunchRecord) (nativeImagePreparation, error) {
			return f.b.PrepareWritable(f.ctx, owner, f.root, f.directory, layerImageName)
		}); err != nil {
		t.Fatal(err)
	}
	records, err := f.j.records()
	if err != nil || len(records) != 3 {
		t.Fatalf("fixture sources: %d %v", len(records), err)
	}
	for _, record := range records {
		point := f.j.anchor(record)
		if record.References[0].Name == layerImageName {
			oldPath, oldID := f.j.path(record), record.Identity
			if err := os.WriteFile(point, []byte("original-drive"), 0o600); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(point)
			if err != nil {
				t.Fatal(err)
			}
			identity, err := resourceFileID(info)
			if err != nil {
				t.Fatal(err)
			}
			record.Identity = nativeLoopIdentity{Device: identity.Device, Inode: identity.Inode}
			f.b.clones[record.Identity] = f.b.clones[oldID]
			delete(f.b.clones, oldID)
			if err := f.j.write(record); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(oldPath); err != nil {
				t.Fatal(err)
			}
		} else if record.References[0].Name == "capture-"+f.capture.CaptureID+"-mem" {
			if err := os.Truncate(point, int64(f.owner.Lease.MemoryMaxMiB)<<20); err != nil {
				t.Fatal(err)
			}
		}
	}
	b := &nativeCaptureFilesFixture{nativeCaptureReaderFixture: reader}
	b.freeze = func(_ context.Context, input *os.File, directory string) (*os.File, error) {
		return nativeFrozenDescriptorFixture(t, input, directory), nil
	}
	f.j.backend, f.v.nativeRecovery.imageSources = b, b
	local, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := &nativeCaptureStorageFixture{StorageBackend: local, put: local.Put}
	f.v.storage = s
	return f, b, s, Machine{VcpuCount: 1, MemSizeMib: f.owner.Lease.MemoryMaxMiB}
}

func assertNativeCaptureFilesClosed(t *testing.T, b *nativeCaptureFilesFixture) {
	t.Helper()
	for _, file := range b.opened {
		if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
			t.Errorf("capture descriptor escaped: %v", err)
		}
	}
}

func TestNativeCapturePublicationUsesOriginalThreePartSet(t *testing.T) {
	f, b, s, machine := nativeCapturePublicationFixture(t)
	records, err := f.j.records()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		path := filepath.Join(f.root, record.References[0].Name)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s.put = func(ctx context.Context, key string, reader io.Reader) error {
		if len(s.keys) == 1 {
			// A guest may write drive1 after a warm capture resumes. The
			// exported drive must retain the contents frozen at the pause.
			record, _, err := f.j.snapshotDrive(f.owner, f.root)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.j.anchor(record), []byte("live-drive-changed"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		// All source locks, including the private drive, remain held for each
		// upload; separate single-file scopes would fail this assertion.
		for _, identity := range append([]nativeLoopIdentity{{}}, []nativeLoopIdentity{records[0].Identity, records[1].Identity, records[2].Identity}...) {
			wait, cancel := context.WithTimeout(ctx, 25*time.Millisecond)
			var lock *os.File
			var err error
			if identity == (nativeLoopIdentity{}) {
				lock, err = f.j.owner.lock(wait, f.owner.Lease.Instance)
			} else {
				lock, err = f.j.lock(wait, identity)
			}
			cancel()
			if lock != nil {
				_ = lock.Close()
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("capture ownership escaped upload: %v", err)
			}
		}
		return s.StorageBackend.Put(ctx, key, reader)
	}
	info, err := f.v.publishNativeSnapshotCapture(f.ctx, f.owner.Lease, f.directory, machine, "1.10.1")
	if err != nil || info.Inputs == nil || info.Inputs.Validate(info.Inputs.Memory.Key) != nil || len(s.keys) != 3 || b.freezes != 1 {
		t.Fatalf("capture publication: info=%+v keys=%v freezes=%d error=%v", info, s.keys, b.freezes, err)
	}
	proof := qualificationSnapshotProof(f.incoming, SnapshotInfo{})
	if info.Inputs.Memory.Key != proof.StorageKey || info.Inputs.VMState.Key != proof.VMStateStorageKey || info.Inputs.Drive.Key != proof.DriveStorageKey {
		t.Fatal("publication escaped original capture namespace")
	}
	for i, part := range []SnapshotArtifactIdentity{info.Inputs.Memory, info.Inputs.VMState, info.Inputs.Drive} {
		reader, err := s.Get(t.Context(), part.Key)
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.New()
		size, copyErr := io.Copy(h, reader)
		if err := errors.Join(copyErr, reader.Close()); err != nil || size != part.Bytes || hex.EncodeToString(h.Sum(nil)) != part.SHA256 || s.keys[i] != part.Key {
			t.Fatalf("published provenance: %+v size=%d error=%v", part, size, err)
		}
	}
	reader, err := s.Get(t.Context(), info.Inputs.Drive.Key)
	if err != nil {
		t.Fatal(err)
	}
	drive, readErr := io.ReadAll(reader)
	if err := errors.Join(readErr, reader.Close()); err != nil || string(drive) != "original-drive" {
		t.Fatalf("frozen drive followed resumed input: %q %v", drive, err)
	}
	if info.StoredBytes != info.Inputs.Memory.Bytes+info.Inputs.VMState.Bytes+info.Inputs.Drive.Bytes {
		t.Fatal("unknown destination footprint was understated")
	}
	capture, err := f.q.readCapture(f.incoming)
	if err != nil || capture != f.capture || f.v.checkEnvironmentQualificationSnapshotSupport() == nil {
		t.Fatal("publication granted capture completion or serving authority", err)
	}
	assertNativeCaptureFilesClosed(t, b)
}

func TestNativeCaptureFilesClosePartialAndConsumedDescriptors(t *testing.T) {
	for _, name := range []string{"second_output_error", "input_error", "consumer_error", "consumer_canceled"} {
		t.Run(name, func(t *testing.T) {
			f, b, _, _ := nativeCapturePublicationFixture(t)
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			injected := errors.New("injected capture consumer error")
			if name == "second_output_error" {
				b.open = func(point string) (*os.File, error) {
					file, err := os.Open(point)
					if b.opens == 2 {
						err = errors.Join(err, injected)
					}
					return file, err
				}
			} else if name == "input_error" {
				b.input = func(point string) (*os.File, error) {
					file, err := os.Open(point)
					return file, errors.Join(err, injected)
				}
			}
			err := f.v.withNativeSnapshotCaptureFiles(ctx, f.owner.Lease, f.directory, func(nativeSnapshotCaptureFiles) error {
				if name == "consumer_canceled" {
					cancel()
					return nil
				}
				if name != "consumer_error" {
					t.Error("partial descriptors reached consumer")
				}
				return injected
			})
			if name == "consumer_canceled" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			} else if !errors.Is(err, injected) {
				t.Fatal(err)
			}
			assertNativeCaptureFilesClosed(t, b)
			lock, err := f.j.owner.lock(t.Context(), f.owner.Lease.Instance)
			if err != nil {
				t.Fatal(err)
			}
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNativeCaptureFilesRejectEpochChangedWhileWaitingForSource(t *testing.T) {
	f, b, _, _ := nativeCapturePublicationFixture(t)
	records, err := f.j.records()
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(records, func(a, b int) bool {
		return nativeImageSourceKey(records[a].Identity) < nativeImageSourceKey(records[b].Identity)
	})
	record := records[len(records)-1]
	sourceLock, err := f.j.lock(f.ctx, record.Identity)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sourceLock.Close() })
	ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- f.v.withNativeSnapshotCaptureFiles(ctx, f.owner.Lease, f.directory, func(nativeSnapshotCaptureFiles) error {
			t.Error("changed source reached consumer")
			return nil
		})
	}()
	// Observe the first canonical source lock held while the last is blocked.
	// Discovery has then completed; replacing its epoch cannot precede it.
	for {
		wait, stop := context.WithTimeout(ctx, 25*time.Millisecond)
		lock, lockErr := f.j.lock(wait, records[0].Identity)
		stop()
		if lock != nil {
			_ = lock.Close()
		}
		if errors.Is(lockErr, context.DeadlineExceeded) {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("capture did not acquire its first source lock")
		}
		time.Sleep(time.Millisecond)
	}
	oldPath, oldAnchor := f.j.path(record), f.j.anchor(record)
	record.Epoch = uuid.NewString()
	if err := os.Rename(oldAnchor, f.j.anchor(record)); err != nil {
		t.Fatal(err)
	}
	if err := f.j.write(record); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(oldPath); err != nil {
		t.Fatal(err)
	}
	if err := sourceLock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil || len(b.opened) != 0 || b.freezes != 0 {
		t.Fatalf("later source epoch substituted: files=%d freezes=%d error=%v", len(b.opened), b.freezes, err)
	}
}

func TestNativeCapturePublicationFailureReturnsNoReceipt(t *testing.T) {
	for _, name := range []string{"storage_error", "short_read", "changed_memory", "changed_frozen_drive", "canceled", "freeze_error_with_descriptor"} {
		t.Run(name, func(t *testing.T) {
			f, b, s, machine := nativeCapturePublicationFixture(t)
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			injected := errors.New("injected upload error")
			if name == "freeze_error_with_descriptor" {
				original := b.freeze
				b.freeze = func(ctx context.Context, input *os.File, directory string) (*os.File, error) {
					file, _ := original(ctx, input, directory)
					return file, injected
				}
			}
			s.put = func(ctx context.Context, key string, reader io.Reader) error {
				if name == "short_read" {
					_, err := io.CopyN(io.Discard, reader, 1)
					return err
				}
				if name == "storage_error" {
					return injected
				}
				if name == "canceled" {
					cancel()
					return nil
				}
				if name == "changed_memory" {
					record, _, err := f.j.captureOutput(f.owner, f.root, "capture-"+f.capture.CaptureID+"-mem")
					if err != nil {
						t.Fatal(err)
					}
					file, err := os.OpenFile(f.j.anchor(record), os.O_WRONLY, 0)
					if err != nil {
						t.Fatal(err)
					}
					_, writeErr := file.WriteAt([]byte("changed"), 0)
					if err := errors.Join(writeErr, file.Close()); err != nil {
						t.Fatal(err)
					}
				} else if name == "changed_frozen_drive" {
					if err := b.opened[len(b.opened)-1].Chmod(0o600); err != nil {
						t.Fatal(err)
					}
				}
				return s.StorageBackend.Put(ctx, key, reader)
			}
			info, err := f.v.publishNativeSnapshotCapture(ctx, f.owner.Lease, f.directory, machine, "1.10.1")
			if err == nil || info != (SnapshotInfo{}) || len(s.keys) > 1 {
				t.Fatalf("failed capture produced receipt or continued: info=%+v keys=%v error=%v", info, s.keys, err)
			}
			assertNativeCaptureFilesClosed(t, b)
		})
	}
}

func TestNativeCapturePublicationRefusesBeforeProducingFiles(t *testing.T) {
	for _, name := range []string{"no_capture", "recovered", "completed", "wrong_ram", "missing_version", "missing_storage", "unsupported_backend", "nil_consumer"} {
		t.Run(name, func(t *testing.T) {
			f, b, s, machine := nativeCapturePublicationFixture(t)
			ctx, version := f.ctx, "1.10.1"
			switch name {
			case "no_capture":
				ctx = t.Context()
			case "recovered":
				delete(f.v.nativeRecovery.owned, f.owner.Lease.Instance)
			case "completed":
				capture := f.capture
				capture.CompletedAt = f.q.clock().UTC()
				ctx = nativeSnapshotCaptureContext(ctx, f.incoming, capture, f.owner)
			case "wrong_ram":
				machine.MemSizeMib++
			case "missing_version":
				version = ""
			case "missing_storage":
				f.v.storage = nil
			case "unsupported_backend":
				f.v.nativeRecovery.imageSources = b.nativeCaptureReaderFixture
			case "nil_consumer":
				if err := f.v.withNativeSnapshotCaptureFiles(ctx, f.owner.Lease, f.directory, nil); err == nil {
					t.Fatal("nil consumer accepted")
				}
				return
			}
			if info, err := f.v.publishNativeSnapshotCapture(ctx, f.owner.Lease, f.directory, machine, version); err == nil || info != (SnapshotInfo{}) || b.freezes != 0 || len(b.opened) != 0 || len(s.keys) != 0 {
				t.Fatalf("refusal performed capture effects: files=%d freezes=%d keys=%v info=%+v error=%v", len(b.opened), b.freezes, s.keys, info, err)
			}
		})
	}
}
