//go:build linux

// adr: 568 — disk intent tests supply no VM capture or artifact deletion proof.
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
	"golang.org/x/sys/unix"
)

func nativePublicationDiskFixture(t *testing.T) (nativeCaptureOutputFixture, *linuxNativeSnapshotPublicationJournal, nativeSnapshotPublicationIntent) {
	t.Helper()
	f := nativeCaptureOutputsFixture(t)
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	var filesystem unix.Statfs_t
	if err := unix.Statfs(root, &filesystem); err != nil {
		t.Fatal(err)
	}
	if !nativeCloneFilesystemSupported(filesystem.Type) {
		t.Skip("persistent intent requires an ext4/XFS/Btrfs disk")
	}
	j := newNativeSnapshotPublicationJournal(root, f.v.chrootBase, "").(*linuxNativeSnapshotPublicationJournal)
	if err := j.Acquire(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if j.owner != nil {
			if err := j.owner.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
				t.Error(err)
			}
		}
	})
	intent := nativeSnapshotPublicationIntent{Version: 1, JailBase: f.v.chrootBase, Incoming: f.incoming, Capture: f.capture, Physical: f.owner, Keys: nativeSnapshotIntentKeys(f.incoming)}
	return f, j, intent
}

func TestNativePublicationIntentSurvivesJournalLossWithoutAdoption(t *testing.T) {
	f, j, intent := nativePublicationDiskFixture(t)
	stored, err := j.Begin(t.Context(), intent)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(j.root, stored.Capture.CaptureID+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(f.q.owner.root); err != nil {
		t.Fatal(err)
	}
	if err := j.Require(t.Context(), stored); err != nil {
		t.Fatal("volatile journal loss removed persistent object intent", err)
	}
	if err := j.owner.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := newNativeSnapshotPublicationJournal(j.root, j.base, "").(*linuxNativeSnapshotPublicationJournal)
	if err := restarted.Acquire(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restarted.owner.Close() }()
	if _, err := restarted.Begin(t.Context(), intent); !errors.Is(err, storage.ErrArtifactExists) {
		t.Fatal("recovery adopted or replaced an uncertain original intent", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatal("inventory changed original intent", err)
	}
	f.v.nativeRecovery.publications = restarted
	if err := f.v.publishNativeSnapshotOutput(f.ctx, f.owner.Lease, "mem"); err == nil || f.b.opens != 0 {
		t.Fatal("inventory manufactured live publication authority", err)
	}
}

func TestNativePublicationIntentRefusesCorruptionAliasesAndReplacement(t *testing.T) {
	for _, change := range []string{"missing_field", "missing_nested", "unknown_field", "duplicate", "null", "trailing", "oversized", "foreign_key", "foreign_capture", "alias", "symlink", "replacement", "fifo", "unknown_entry"} {
		t.Run(change, func(t *testing.T) {
			_, j, intent := nativePublicationDiskFixture(t)
			stored, err := j.Begin(t.Context(), intent)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(j.root, stored.Capture.CaptureID+".json")
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(body, &fields); err != nil {
				t.Fatal(err)
			}
			mutated := false
			switch change {
			case "missing_field":
				delete(fields, "version")
				mutated = true
			case "missing_nested":
				fields["file"] = json.RawMessage(`{"device":1}`)
				mutated = true
			case "unknown_field":
				fields["borrowed"] = json.RawMessage(`false`)
				mutated = true
			case "duplicate":
				body = append([]byte(`{"version":1,`), body[1:]...)
			case "null":
				fields["capture"] = json.RawMessage(`null`)
				mutated = true
			case "trailing":
				body = append(body, []byte(` {}`)...)
			case "oversized":
				body = []byte(strings.Repeat(" ", 2<<20+1))
			case "foreign_key":
				stored.Keys.Memory += "-borrowed"
				body, err = json.Marshal(stored)
			case "foreign_capture":
				stored.Capture.NativeGeneration = "00000000-0000-0000-0000-000000000001"
				body, err = json.Marshal(stored)
			case "alias":
				err = os.Link(path, filepath.Join(t.TempDir(), "alias"))
			case "symlink", "replacement", "fifo":
				moved := filepath.Join(t.TempDir(), "original")
				if err = os.Rename(path, moved); err == nil {
					if change == "symlink" {
						err = os.Symlink(moved, path)
					} else if change == "fifo" {
						err = unix.Mkfifo(path, 0o600)
					} else {
						err = os.WriteFile(path, body, 0o600)
					}
				}
			case "unknown_entry":
				err = os.WriteFile(filepath.Join(j.root, "unowned"), nil, 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if mutated {
				body, err = json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
			}
			if change != "alias" && change != "symlink" && change != "replacement" && change != "fifo" && change != "unknown_entry" {
				if err := os.WriteFile(path, body, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if change != "unknown_entry" {
				// Restore the original expected record even when the fixture
				// deliberately corrupted a field in its serialized form.
				expected := intent
				expected.Directory, expected.File = stored.Directory, stored.File
				if err := j.Require(t.Context(), expected); err == nil {
					t.Fatal("damaged original intent retained publication authority")
				}
			}
			if err := j.owner.Close(); err != nil {
				t.Fatal(err)
			}
			restarted := newNativeSnapshotPublicationJournal(j.root, j.base, "").(*linuxNativeSnapshotPublicationJournal)
			if err := restarted.Acquire(t.Context()); err == nil {
				_ = restarted.owner.Close()
				t.Fatal("corrupt inventory acquired persistent ownership")
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatal("failed inventory deleted uncertain intent", err)
			}
		})
	}
}

func TestNativePublicationDirectoryLifetimeOwnership(t *testing.T) {
	_, j, intent := nativePublicationDiskFixture(t)
	other := newNativeSnapshotPublicationJournal(j.root, j.base, "").(*linuxNativeSnapshotPublicationJournal)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if err := other.Acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("another daemon borrowed publication ownership", err)
	}
	if err := j.owner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := j.Check(); err == nil {
		t.Fatal("closed directory lock retained publication authority")
	}
	if _, err := j.Begin(t.Context(), intent); err == nil {
		t.Fatal("closed original owner created intent")
	}
	if err := other.Acquire(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.owner.Close() }()
}

func TestNativePublicationStartupFailureReleasesUnassignedDaemonLocks(t *testing.T) {
	f, _, _ := nativePublicationDiskFixture(t)
	if err := f.v.nativeRecovery.daemonLock.Close(); err != nil {
		t.Fatal(err)
	}
	images := t.TempDir()
	if err := os.Chmod(images, 0o700); err != nil {
		t.Fatal(err)
	}
	backend := linuxNativeImageSources{base: f.v.chrootBase, diskStagingRoot: images}
	injected := errors.New("persistent publication inventory is corrupt")
	failed := &nativeProcessRecoveryRuntime{journal: f.q.owner, imageSources: backend, publications: &nativePublicationIntentFixture{err: injected}}
	if err := failed.acquireDaemonOwnership(t.Context()); !errors.Is(err, injected) {
		t.Fatal("corrupt publication inventory acquired daemon ownership", err)
	}
	if failed.daemonLock != nil || failed.diskLock != nil {
		t.Fatal("failed startup retained unassigned daemon locks")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	retry := &nativeProcessRecoveryRuntime{journal: f.q.owner, imageSources: backend}
	if err := retry.acquireDaemonOwnership(ctx); err != nil {
		t.Fatal("failed startup leaked a jail or disk ownership lock", err)
	}
	if err := errors.Join(retry.daemonLock.Close(), retry.diskLock.Close()); err != nil {
		t.Fatal(err)
	}
}

func TestNativePublicationRootRejectsUnsafePlacementBeforeIntent(t *testing.T) {
	for _, change := range []string{"missing", "relative", "shared", "symlink", "jail", "image_staging", "replaced_root"} {
		t.Run(change, func(t *testing.T) {
			_, j, intent := nativePublicationDiskFixture(t)
			if change == "replaced_root" {
				if err := os.Rename(j.root, j.root+"-moved"); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.RemoveAll(j.root + "-moved") })
				if err := os.Mkdir(j.root, 0o700); err != nil {
					t.Fatal(err)
				}
				if _, err := j.Begin(t.Context(), intent); err == nil {
					t.Fatal("substituted root created original capture intent")
				}
				return
			}
			if err := j.owner.Close(); err != nil {
				t.Fatal(err)
			}
			root, base, images := j.root, j.base, ""
			switch change {
			case "missing":
				root += "/missing"
			case "relative":
				root = "relative-publication-root"
			case "shared":
				if err := os.Chmod(root, 0o777); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				link := filepath.Join(t.TempDir(), "publication-root")
				if err := os.Symlink(root, link); err != nil {
					t.Fatal(err)
				}
				root = link
			case "jail":
				base = root
			case "image_staging":
				images = root
			}
			rejected := newNativeSnapshotPublicationJournal(root, base, images).(*linuxNativeSnapshotPublicationJournal)
			if err := rejected.Acquire(t.Context()); err == nil {
				_ = rejected.owner.Close()
				t.Fatal("unsafe directory acquired persistent capture ownership")
			}
			entries, err := os.ReadDir(j.root)
			if err != nil || len(entries) != 0 {
				t.Fatal("refused ownership created named intent", err)
			}
		})
	}
}

func TestNativePublicationDiskIntentFencesOutputBeforeAndAfterIO(t *testing.T) {
	for _, when := range []string{"before", "during", "complete"} {
		t.Run(when, func(t *testing.T) {
			_, j, _ := nativePublicationDiskFixture(t)
			f, _ := nativeReadableCaptureFixture(t)
			j.base = f.v.chrootBase
			backend := &nativeExclusivePublicationFixture{memStorage: &memStorage{blobs: make(map[string][]byte)}}
			f.v.storage, f.v.nativeRecovery.publications = backend, j
			ctx, err := f.v.beginNativeSnapshotPublication(f.ctx, f.owner.Lease)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(j.root, f.capture.CaptureID+".json")
			if when == "before" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else if when == "during" {
				backend.consume = func(context.Context, *os.File) error { return os.Remove(path) }
			}
			err = f.v.publishNativeSnapshotOutput(ctx, f.owner.Lease, "mem")
			if when == "complete" {
				if err != nil || backend.puts != 1 {
					t.Fatal("original durable intent could not publish", err)
				}
			} else if err == nil {
				t.Fatal("missing durable intent acknowledged publication")
			}
			if when == "before" && (f.b.opens != 0 || backend.puts != 0) {
				t.Fatal("missing durable intent opened original output")
			}
			if f.v.checkEnvironmentQualificationSnapshotSupport() == nil {
				t.Fatal("persistent intent enabled incomplete native capture")
			}
		})
	}
}

func TestNativePublicationBeginCrashRetainsOnlyIntent(t *testing.T) {
	if root := os.Getenv("GREGALE_NATIVE_PUBLICATION_CRASH_ROOT"); root != "" {
		body, err := os.ReadFile(os.Getenv("GREGALE_NATIVE_PUBLICATION_CRASH_INPUT"))
		if err != nil {
			t.Fatal(err)
		}
		var intent nativeSnapshotPublicationIntent
		if err := json.Unmarshal(body, &intent); err != nil {
			t.Fatal(err)
		}
		j := newNativeSnapshotPublicationJournal(root, intent.JailBase, "")
		if err := j.Acquire(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err := j.Begin(t.Context(), intent); err != nil {
			t.Fatal(err)
		}
		os.Exit(0) // Skip every Go defer; the kernel releases the directory lock.
	}
	f, j, intent := nativePublicationDiskFixture(t)
	if err := j.owner.Close(); err != nil {
		t.Fatal(err)
	}
	// Input is only a fixture, outside the publication inventory.
	input := filepath.Join(f.directory, "original-capture.json")
	body, err := json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, body, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestNativePublicationBeginCrashRetainsOnlyIntent$", "-test.timeout=30s")
	cmd.Env = append(os.Environ(), "GREGALE_NATIVE_PUBLICATION_CRASH_ROOT="+j.root, "GREGALE_NATIVE_PUBLICATION_CRASH_INPUT="+input, "GORACE=atexit_sleep_ms=0")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("original publication child: %v %s", err, output)
	}
	if err := os.RemoveAll(f.q.owner.root); err != nil {
		t.Fatal(err)
	}
	restarted := newNativeSnapshotPublicationJournal(j.root, j.base, "").(*linuxNativeSnapshotPublicationJournal)
	if err := restarted.Acquire(t.Context()); err != nil {
		t.Fatal("original crash intent lost inventory", err)
	}
	defer func() { _ = restarted.owner.Close() }()
	if _, err := restarted.Begin(t.Context(), intent); !errors.Is(err, storage.ErrArtifactExists) {
		t.Fatal("crashed producer could be replayed", err)
	}
	f.v.nativeRecovery.publications = restarted
	if err := f.v.publishNativeSnapshotOutput(f.ctx, f.owner.Lease, "mem"); err == nil {
		t.Fatal("crash inventory granted live publication")
	}
}
