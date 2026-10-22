//go:build linux

package storage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

const localCohortCaptureID = "660e8400-e29b-41d4-a716-446655440001"

func localCaptureCohort(t *testing.T, backend StorageBackend, prefix string) [4]ExclusiveArtifactReceipt {
	t.Helper()
	var objects [4]ExclusiveArtifactReceipt
	for i, kind := range [...]string{"mem", "vmstate", "drive", "backing"} {
		body := "original-" + kind
		var err error
		objects[i], err = PutExclusiveArtifact(t.Context(), backend, prefix+exclusiveCapturePrefix+kind, strings.NewReader(body), int64(len(body)))
		if err != nil {
			t.Fatal(err)
		}
	}
	return objects
}

func TestLocalCaptureCohortRetirementAndReplay(t *testing.T) {
	b := exclusiveLocalFixture(t)
	objects := localCaptureCohort(t, b, "")
	if err := CheckExclusiveArtifactCohortRetirement(t.Context(), b, localCohortCaptureID, objects); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := RetireExclusiveArtifactCohort(t.Context(), b, localCohortCaptureID, objects); err != nil {
			t.Fatal(err)
		}
	}
	for _, object := range objects {
		if _, err := os.Stat(filepath.Join(b.root, object.ObjectKey)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("retired capture remains publicly readable", object.Key, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(b.root, localRetirementNamespace, localCohortCaptureID))
	if err != nil || len(entries) != 1 || entries[0].Name() != localRetirementSeal {
		t.Fatal("retirement has no durable sealed replay marker", entries, err)
	}
	// A fresh public directory with the same key cannot be consumed by replay.
	if err := os.MkdirAll(filepath.Join(b.root, filepath.Dir(objects[0].ObjectKey)), 0o700); err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(b.root, objects[0].ObjectKey)
	if err := os.WriteFile(newPath, []byte("new-capture"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RetireExclusiveArtifactCohort(t.Context(), b, localCohortCaptureID, objects); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(newPath); err != nil || string(body) != "new-capture" {
		t.Fatal("replay consumed a new public capture", err)
	}
}

func TestLocalCaptureCohortRetirementRejectsReplacement(t *testing.T) {
	b := exclusiveLocalFixture(t)
	objects := localCaptureCohort(t, b, "")
	original := filepath.Join(b.root, objects[0].ObjectKey)
	if err := os.Rename(original, original+"-retained"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(original, []byte("replacement"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RetireExclusiveArtifactCohort(t.Context(), b, localCohortCaptureID, objects); !errors.Is(err, ErrArtifactReceiptMismatch) {
		t.Fatal("replacement was not rejected before cohort move", err)
	}
	if body, err := os.ReadFile(original); err != nil || string(body) != "replacement" {
		t.Fatal("replacement was removed", err)
	}
}

func TestLocalCaptureCohortRetirementRejectsMixedOrUnexpectedObjects(t *testing.T) {
	for _, change := range []string{"mixed_key", "mixed_parent", "extra_file", "symlink", "alias"} {
		t.Run(change, func(t *testing.T) {
			b := exclusiveLocalFixture(t)
			objects := localCaptureCohort(t, b, "")
			public := filepath.Join(b.root, objects[0].ObjectKey)
			var err error
			switch change {
			case "mixed_key":
				objects[1].ObjectKey = objects[0].ObjectKey
			case "mixed_parent":
				objects[1].Local.ParentInode++
			case "extra_file":
				err = os.WriteFile(filepath.Join(filepath.Dir(public), "unreviewed"), []byte("extra"), 0o644)
			case "symlink":
				err = os.Rename(public, public+"-retained")
				if err == nil {
					err = os.Symlink(public+"-retained", public)
				}
			case "alias":
				err = os.Link(public, filepath.Join(b.root, "alias"))
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := RetireExclusiveArtifactCohort(t.Context(), b, localCohortCaptureID, objects); err == nil {
				t.Fatal("unsafe capture cohort was retired")
			}
			if _, err := os.Stat(filepath.Join(b.root, objects[2].ObjectKey)); err != nil {
				t.Fatal("rejected cohort removed an original object", err)
			}
		})
	}
}

func TestLocalCaptureCohortRetirementResumesAfterMoveAndPartialDelete(t *testing.T) {
	b := exclusiveLocalFixture(t)
	objects := localCaptureCohort(t, b, "")
	root, private, err := b.retirementRoot(t.Context(), true, objects)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := b.exclusiveParent(t.Context(), filepath.Dir(objects[0].ObjectKey), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Renameat2(int(parent.Fd()), "v2", int(private.Fd()), localCohortCaptureID, unix.RENAME_NOREPLACE); err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(parent.Sync(), private.Sync(), parent.Close(), private.Close(), root.Close()); err != nil {
		t.Fatal(err)
	}
	quarantined := filepath.Join(b.root, localRetirementNamespace, localCohortCaptureID)
	if err := os.Remove(filepath.Join(quarantined, "mem")); err != nil {
		t.Fatal(err)
	}
	if err := RetireExclusiveArtifactCohort(t.Context(), b, localCohortCaptureID, objects); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(quarantined)
	if err != nil || len(entries) != 1 || entries[0].Name() != localRetirementSeal {
		t.Fatal("partial retirement did not seal on replay", entries, err)
	}
}

func TestLocalCaptureCohortRetirementRoutesOneOriginalBackend(t *testing.T) {
	b := exclusiveLocalFixture(t)
	router, err := NewPrefixRouter(map[string]StorageBackend{"canonical/": b}, nil)
	if err != nil {
		t.Fatal(err)
	}
	objects := localCaptureCohort(t, router, "canonical/")
	if err := RetireExclusiveArtifactCohort(t.Context(), router, localCohortCaptureID, objects); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(b.root, objects[0].ObjectKey)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("routed original was not retired", err)
	}
}
