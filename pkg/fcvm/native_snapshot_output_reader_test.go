//go:build linux || darwin

// adr: 794 — modeled mounts test ownership; they provide no native acceptance.
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

type nativeCaptureReaderFixture struct {
	*nativeSnapshotOutputFixture
	open  func(string) (*os.File, error)
	file  *os.File
	opens int
}

func (b *nativeCaptureReaderFixture) OpenSnapshotOutput(record nativeImageSourceRecord, ref nativeImageReference, point string) (*os.File, error) {
	b.opens++
	if err := errors.Join(b.CheckReference(record, ref), b.CheckAnchor(record, point)); err != nil {
		return nil, err
	}
	var err error
	if b.open != nil {
		b.file, err = b.open(point)
	} else {
		b.file, err = os.Open(point)
	}
	return b.file, err
}

func nativeCaptureReaderJournalFixture(t *testing.T) (nativeCaptureOutputFixture, *nativeCaptureReaderFixture) {
	t.Helper()
	f := nativeCaptureOutputsFixture(t)
	if _, err := f.v.stageNativeSnapshotOutputs(f.ctx, f.owner.Lease, f.directory); err != nil {
		t.Fatal(err)
	}
	records, err := f.j.records()
	if err != nil || len(records) != 2 {
		t.Fatalf("capture outputs: %+v %v", records, err)
	}
	for _, record := range records {
		oldPath, point, oldID := f.j.path(record), f.j.anchor(record), record.Identity
		body := "memory"
		if record.References[0].Name == "capture-"+f.capture.CaptureID+"-vmstate" {
			body = "device-state"
		}
		if err := os.WriteFile(point, []byte(body), 0o600); err != nil {
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
		// Use real descriptor identities with modeled mounts/metadata. This
		// does not claim that a named fixture is a native anonymous anchor.
		record.Identity = nativeLoopIdentity{Device: identity.Device, Inode: identity.Inode}
		f.b.clones[record.Identity] = f.b.clones[oldID]
		delete(f.b.clones, oldID)
		if err := f.j.write(record); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(oldPath); err != nil {
			t.Fatal(err)
		}
	}
	b := &nativeCaptureReaderFixture{nativeSnapshotOutputFixture: f.b}
	f.j.backend, f.v.nativeRecovery.imageSources = b, b
	return f, b
}

func TestNativeSnapshotOutputReaderKeepsOriginalBytesAndLocks(t *testing.T) {
	f, b := nativeCaptureReaderJournalFixture(t)
	for _, test := range []struct{ kind, body string }{{"mem", "memory"}, {"vmstate", "device-state"}} {
		t.Run(test.kind, func(t *testing.T) {
			name, err := nativeSnapshotOutputName(f.capture.CaptureID, test.kind)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(f.root, name)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
				t.Fatal(err)
			}
			record, _, err := f.j.captureOutput(f.owner, f.root, name)
			if err != nil {
				t.Fatal(err)
			}
			err = f.v.withNativeSnapshotOutput(f.ctx, f.owner.Lease, test.kind, func(file *os.File) error {
				body, err := io.ReadAll(file)
				if err != nil || string(body) != test.body {
					t.Fatalf("original output: %q %v", body, err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
				defer cancel()
				lock, err := f.j.owner.lock(ctx, f.owner.Lease.Instance)
				if lock != nil {
					_ = lock.Close()
				}
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("physical lock escaped consumer: %v", err)
				}
				sourceCtx, sourceCancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
				defer sourceCancel()
				sourceLock, err := f.j.lock(sourceCtx, record.Identity)
				if sourceLock != nil {
					_ = sourceLock.Close()
				}
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("source lock escaped consumer: %v", err)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := b.file.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("descriptor escaped: %v", err)
			}
			if body, err := os.ReadFile(path); err != nil || string(body) != "replacement" {
				t.Fatalf("reader changed replacement: %q %v", body, err)
			}
			lock, err := f.j.owner.lock(t.Context(), f.owner.Lease.Instance)
			if err != nil {
				t.Fatal(err)
			}
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
	if b.opens != 2 || f.v.checkEnvironmentQualificationSnapshotSupport() == nil {
		t.Fatal("reader enabled capture or opened unrelated outputs")
	}
}

func TestNativeSnapshotOutputReaderRequiresOriginalCapture(t *testing.T) {
	for _, name := range []string{"missing_capture", "recovered", "closed_daemon", "pid", "generation", "revoked", "completed", "other_capture", "bad_kind", "nil_consumer", "canceled"} {
		t.Run(name, func(t *testing.T) {
			f, b := nativeCaptureReaderJournalFixture(t)
			ctx, kind := f.ctx, "mem"
			consume := func(*os.File) error { t.Error("unowned bytes reached consumer"); return nil }
			switch name {
			case "missing_capture":
				ctx = t.Context()
			case "recovered":
				delete(f.v.nativeRecovery.owned, f.owner.Lease.Instance)
			case "closed_daemon":
				if err := f.v.nativeRecovery.daemonLock.Close(); err != nil {
					t.Fatal(err)
				}
			case "pid", "generation", "revoked":
				owner := f.owner
				if name == "pid" {
					owner.PID++
				} else if name == "generation" {
					owner.Generation = uuid.NewString()
				} else {
					owner.Revoked = true
				}
				if err := f.j.owner.write(owner); err != nil {
					t.Fatal(err)
				}
			case "completed", "other_capture":
				capture := f.capture
				if name == "completed" {
					capture.CompletedAt = f.q.clock().UTC()
				} else {
					capture.CaptureID = uuid.NewString()
				}
				ctx = nativeSnapshotCaptureContext(ctx, f.incoming, capture, f.owner)
			case "bad_kind":
				kind = "drive"
			case "nil_consumer":
				consume = nil
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if err := f.v.withNativeSnapshotOutput(ctx, f.owner.Lease, kind, consume); err == nil || b.opens != 0 {
				t.Fatalf("capture refusal: opens=%d error=%v", b.opens, err)
			}
		})
	}
}

func TestNativeSnapshotOutputReaderRejectsAmbiguousAndRetiredBindings(t *testing.T) {
	for _, name := range []string{"invalid_alias_journal", "duplicate", "unready", "removed", "readonly"} {
		t.Run(name, func(t *testing.T) {
			f, b := nativeCaptureReaderJournalFixture(t)
			record, _, err := f.j.captureOutput(f.owner, f.root, "capture-"+f.capture.CaptureID+"-mem")
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "invalid_alias_journal":
				alias := record.References[0]
				alias.ID, alias.Name = uuid.NewString(), "alias"
				record.References = append(record.References, alias)
			case "duplicate":
				record.Epoch = uuid.NewString()
			case "unready":
				record.References[0].Ready = false
			case "removed":
				record.References[0].TargetRemoved, record.References[0].Removed = true, true
			case "readonly":
				record.References[0].ReadOnly, record.References[0].AddPerms = true, 0o044
			}
			record.Desired, err = desiredNativeImageMetadata(record)
			if name == "invalid_alias_journal" {
				if err == nil {
					t.Fatal("writable alias was accepted by source metadata authority")
				}
				// A malformed journal must also close the reader before any
				// descriptor opens; the normal writer already rejects it.
				body, err := json.Marshal(record)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(f.j.path(record), body, 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				record.Applied = record.Desired
				if err := f.j.write(record); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.v.withNativeSnapshotOutput(f.ctx, f.owner.Lease, "mem", func(*os.File) error {
				t.Error("ambiguous or retired output reached consumer")
				return nil
			}); err == nil || b.opens != 0 {
				t.Fatalf("binding refusal: opens=%d error=%v", b.opens, err)
			}
		})
	}
}

func TestNativeSnapshotOutputReaderClosesRejectedAndFailedDescriptors(t *testing.T) {
	for _, name := range []string{"writable", "empty", "replacement", "inheritable", "reader_error", "nil", "consumer_error"} {
		t.Run(name, func(t *testing.T) {
			f, b := nativeCaptureReaderJournalFixture(t)
			failure := errors.New("publication failed")
			b.open = func(point string) (*os.File, error) {
				if name == "nil" {
					return nil, nil
				}
				if name == "empty" {
					if err := os.Truncate(point, 0); err != nil {
						return nil, err
					}
				}
				if name == "replacement" {
					point = filepath.Join(t.TempDir(), "replacement")
					if err := os.WriteFile(point, []byte("other bytes"), 0o600); err != nil {
						return nil, err
					}
				}
				access := os.O_RDONLY
				if name == "writable" {
					access = os.O_RDWR
				}
				file, err := os.OpenFile(point, access, 0)
				if err != nil {
					return file, err
				}
				if name == "inheritable" {
					_, err = unix.FcntlInt(file.Fd(), unix.F_SETFD, 0)
				}
				if name == "reader_error" {
					err = failure
				}
				return file, err
			}
			consumed := false
			err := f.v.withNativeSnapshotOutput(f.ctx, f.owner.Lease, "mem", func(*os.File) error {
				consumed = true
				return failure
			})
			if err == nil || consumed != (name == "consumer_error") {
				t.Fatalf("invalid descriptor reached consumer: consumed=%t error=%v", consumed, err)
			}
			if name == "reader_error" || name == "consumer_error" {
				if !errors.Is(err, failure) {
					t.Fatalf("original error lost: %v", err)
				}
			}
			if b.file != nil {
				if _, err := b.file.Stat(); !errors.Is(err, os.ErrClosed) {
					t.Fatalf("rejected descriptor leaked: %v", err)
				}
			}
		})
	}
}
