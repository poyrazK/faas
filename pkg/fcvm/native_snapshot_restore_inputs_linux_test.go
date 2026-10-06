//go:build linux

// adr: 568 — modeled input tests do not grant qualification restore authority.
package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
	"golang.org/x/sys/unix"
)

type nativeRestoreReadTrap struct {
	*storage.LocalStorageBackend
	opened, closed int
	closeErr       error
	afterOpen      func(int)
}

type nativeRestoreSourceCloser struct {
	io.ReadCloser
	backend *nativeRestoreReadTrap
}

func (r nativeRestoreSourceCloser) Close() error {
	r.backend.closed++
	return errors.Join(r.ReadCloser.Close(), r.backend.closeErr)
}
func (b *nativeRestoreReadTrap) GetExclusiveArtifact(ctx context.Context, receipt storage.ExclusiveArtifactReceipt) (io.ReadCloser, error) {
	r, err := b.LocalStorageBackend.GetExclusiveArtifact(ctx, receipt)
	if err != nil {
		return nil, err
	}
	b.opened++
	if b.afterOpen != nil {
		b.afterOpen(b.opened)
	}
	return nativeRestoreSourceCloser{ReadCloser: r, backend: b}, nil
}
func (*nativeRestoreReadTrap) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("ordinary read must never replace receipt verification")
}
func (*nativeRestoreReadTrap) LocalPath(string) (string, bool, error) {
	return "", false, errors.New("path lookup must never replace receipt verification")
}
func (*nativeRestoreReadTrap) Delete(context.Context, string) error {
	return errors.New("restore has no artifact cleanup authority")
}

type nativeRestoreInputFixture struct {
	capture   nativeCaptureOutputFixture
	journal   *linuxNativeSnapshotPublicationJournal
	cohort    nativeSnapshotRestoreCohort
	completed nativeQualificationCaptureRecord
	backend   *nativeRestoreReadTrap
	root      string
	bodies    [4][]byte
}

func nativeRestoreInputsFixture(t *testing.T, sidecar []byte) nativeRestoreInputFixture {
	t.Helper()
	original, j, intent := nativePublicationDiskFixture(t)
	intent, err := j.Begin(t.Context(), intent)
	if err != nil {
		t.Fatal(err)
	}
	backing := BackingIdentity{Version: 1, Kernel: "sha256:modeled-kernel", Base: "sha256:modeled-base"}
	if sidecar == nil {
		sidecar, err = json.Marshal(backing)
		if err != nil {
			t.Fatal(err)
		}
	}
	canonical, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(canonical.Root(), 0o700); err != nil {
		t.Fatal(err)
	}
	f := nativeRestoreInputFixture{capture: original, journal: j, backend: &nativeRestoreReadTrap{LocalStorageBackend: canonical}, root: t.TempDir(),
		bodies: [4][]byte{append([]byte("original-memory"), make([]byte, 32<<10)...), []byte("original-vmstate"), []byte("original-drive"), sidecar}}
	if err := os.Chmod(f.root, 0o700); err != nil {
		t.Fatal(err)
	}
	completed := intent.Capture
	completed.CompletedAt, completed.Backing = completed.StartedAt.Add(time.Millisecond), backing
	for i, kind := range [...]string{"mem", "vmstate", "drive", "backing"} {
		object, err := storage.PutExclusiveArtifact(t.Context(), canonical, nativePublicationObjectKey(intent, kind), bytes.NewReader(f.bodies[i]), int64(len(f.bodies[i])))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := j.RecordObject(t.Context(), intent, kind, object); err != nil {
			t.Fatal(err)
		}
		if i < 3 {
			completed.Info.StoredBytes += object.StoredBytes
		}
	}
	completed.Info.MemBytes, completed.Info.VMStateBytes = int64(len(f.bodies[0])), int64(len(f.bodies[1]))
	f.completed = completed
	f.cohort, err = j.ReadRestoreCohort(t.Context(), completed.CaptureID)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestNativeRestoreInputsRequireFullVerifiedCohortAndJoinDescriptors(t *testing.T) {
	f := nativeRestoreInputsFixture(t, nil)
	var received nativeSnapshotRestoreInputs
	err := withNativeSnapshotRestoreInputs(t.Context(), f.journal, f.backend, f.completed, f.root, func(inputs nativeSnapshotRestoreInputs) error {
		received = inputs
		if f.backend.opened != 4 || f.backend.closed != 4 || inputs.Backing != f.completed.Backing {
			t.Fatal("consumer ran before all source proofs joined")
		}
		for i, file := range inputs.Files {
			body, err := io.ReadAll(file)
			if err != nil || !bytes.Equal(body, f.bodies[i]) {
				t.Fatal("restore input differs", i, err)
			}
			if _, err := file.WriteAt([]byte("mutation"), 0); err == nil {
				t.Fatal("consumer received a writable descriptor")
			}
			var stat unix.Stat_t
			if err := unix.Fstat(int(file.Fd()), &stat); err != nil || stat.Nlink != 0 || stat.Mode&0o7777 != 0o400 {
				t.Fatal("input lost private anonymous identity", err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range received.Files {
		if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
			t.Fatal("descriptor escaped consumer boundary", err)
		}
	}
	entries, err := os.ReadDir(f.root)
	if err != nil || len(entries) != 0 {
		t.Fatal("restore materialization created named files", entries, err)
	}
}

func TestNativeRestoreInputsRefuseIncompleteOrUncertainEvidenceBeforeConsumer(t *testing.T) {
	for _, failure := range []string{"missing", "incomplete", "foreign_completion", "accounting", "source_digest", "replacement", "source_close", "receipt_changed", "root_changed", "cancel", "backing_mismatch", "backing_duplicate", "backing_null", "backing_oversized"} {
		t.Run(failure, func(t *testing.T) {
			var sidecar []byte
			switch failure {
			case "backing_duplicate":
				sidecar = []byte(`{"version":1,"version":1,"kernel":"sha256:modeled-kernel","base":"sha256:modeled-base"}`)
			case "backing_null":
				sidecar = []byte(`{"version":1,"kernel":null,"base":"sha256:modeled-base"}`)
			case "backing_oversized":
				sidecar = bytes.Repeat([]byte("x"), api.NativeSnapshotBackingRecordMaxBytes+1)
			}
			f := nativeRestoreInputsFixture(t, sidecar)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			r := f.cohort.Objects[0]
			path := filepath.Join(r.Object.Location, filepath.FromSlash(r.Object.ObjectKey))
			switch failure {
			case "missing":
				if err := os.Remove(filepath.Join(f.journal.root, nativePublicationReceiptName(r.CaptureID, "drive"))); err != nil {
					t.Fatal(err)
				}
			case "incomplete":
				f.completed.CompletedAt, f.completed.Info, f.completed.Backing = time.Time{}, SnapshotInfo{}, BackingIdentity{}
			case "foreign_completion":
				f.completed.StartedAt = f.completed.StartedAt.Add(time.Microsecond)
			case "accounting":
				f.completed.Info.StoredBytes++
			case "source_digest":
				if err := os.WriteFile(path, bytes.Repeat([]byte("x"), len(f.bodies[0])), 0o644); err != nil {
					t.Fatal(err)
				}
			case "replacement":
				if err := os.Rename(path, path+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, f.bodies[0], 0o644); err != nil {
					t.Fatal(err)
				}
			case "source_close":
				f.backend.closeErr = errors.New("original source close acknowledgement lost")
			case "receipt_changed":
				f.backend.afterOpen = func(n int) {
					if n == 4 {
						if err := os.Remove(filepath.Join(f.journal.root, nativePublicationReceiptName(r.CaptureID, "mem"))); err != nil {
							t.Fatal(err)
						}
					}
				}
			case "root_changed":
				f.backend.afterOpen = func(n int) {
					if n == 1 {
						if err := os.Rename(f.root, f.root+".original"); err != nil {
							t.Fatal(err)
						}
						if err := os.Mkdir(f.root, 0o700); err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { _ = os.RemoveAll(f.root + ".original") })
					}
				}
			case "cancel":
				f.backend.afterOpen = func(int) { cancel() }
			case "backing_mismatch":
				f.completed.Backing.Base += "-foreign"
			}
			consumed := false
			err := withNativeSnapshotRestoreInputs(ctx, f.journal, f.backend, f.completed, f.root, func(nativeSnapshotRestoreInputs) error { consumed = true; return nil })
			if err == nil || consumed {
				t.Fatal("incomplete evidence reached consumer", err)
			}
			if f.backend.opened != f.backend.closed {
				t.Fatal("failed source was not joined", f.backend.opened, f.backend.closed)
			}
			entries, err := os.ReadDir(f.root)
			if err != nil || len(entries) != 0 {
				t.Fatal("failure left named materialization", entries, err)
			}
			if failure == "missing" || failure == "incomplete" || failure == "foreign_completion" || failure == "accounting" || failure == "backing_oversized" {
				if f.backend.opened != 0 {
					t.Fatal("invalid cohort started storage IO", f.backend.opened)
				}
			}
		})
	}
}

func TestNativeRestoreCohortRefusesAliasedObjectsAndOverflow(t *testing.T) {
	f := nativeRestoreInputsFixture(t, nil)
	for _, change := range []string{"file", "object", "inode", "inode_other_location", "overflow", "order"} {
		t.Run(change, func(t *testing.T) {
			c := f.cohort
			local := *c.Objects[1].Object.Local
			c.Objects[1].Object.Local = &local
			switch change {
			case "file":
				c.Objects[1].File = c.Objects[0].File
			case "object":
				c.Objects[1].Object.ObjectKey = c.Objects[0].Object.ObjectKey
			case "inode":
				c.Objects[1].Object.Local.Inode = c.Objects[0].Object.Local.Inode
			case "inode_other_location":
				c.Objects[1].Object.Local.Inode = c.Objects[0].Object.Local.Inode
				c.Objects[1].Object.Location += "-bind-alias"
			case "overflow":
				c.Objects[0].Object.StoredBytes = int64(^uint64(0) >> 1)
			case "order":
				c.Objects[0], c.Objects[1] = c.Objects[1], c.Objects[0]
			}
			if err := c.validate(f.completed); err == nil {
				t.Fatal("aliased or ambiguous cohort retained evidence")
			}
		})
	}
	if err := f.cohort.validate(f.completed); err != nil {
		t.Fatal("test corrupted original fixture", err)
	}
}

func TestNativeRestoreCohortMissingReceiptReturnsNoPartialEvidence(t *testing.T) {
	f := nativeRestoreInputsFixture(t, nil)
	if err := os.Remove(filepath.Join(f.journal.root, nativePublicationReceiptName(f.completed.CaptureID, "backing"))); err != nil {
		t.Fatal(err)
	}
	c, err := f.journal.ReadRestoreCohort(t.Context(), f.completed.CaptureID)
	if err == nil || !reflect.DeepEqual(c, nativeSnapshotRestoreCohort{}) {
		t.Fatal("partial cohort escaped missing receipt", c, err)
	}
}

func TestNativeRestoreInputsJoinAfterConsumerFailureOrRevokedEvidence(t *testing.T) {
	for _, outcome := range []string{"consumer_error", "consumer_cancel", "consumer_removes_receipt"} {
		t.Run(outcome, func(t *testing.T) {
			f := nativeRestoreInputsFixture(t, nil)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var received nativeSnapshotRestoreInputs
			injected := errors.New("consumer failed")
			err := withNativeSnapshotRestoreInputs(ctx, f.journal, f.backend, f.completed, f.root, func(inputs nativeSnapshotRestoreInputs) error {
				received = inputs
				switch outcome {
				case "consumer_error":
					return injected
				case "consumer_cancel":
					cancel()
				case "consumer_removes_receipt":
					return os.Remove(filepath.Join(f.journal.root, nativePublicationReceiptName(f.completed.CaptureID, "drive")))
				}
				return nil
			})
			if err == nil || received.Files[0] == nil {
				t.Fatal("consumer failure acquired success or skipped the consumer", err)
			}
			if outcome == "consumer_error" && !errors.Is(err, injected) {
				t.Fatal("original consumer failure was hidden", err)
			}
			for _, file := range received.Files {
				if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
					t.Fatal("consumer failure retained a descriptor", err)
				}
			}
		})
	}
}
