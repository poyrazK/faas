//go:build linux

// adr: 521 — pinned input capability rejects substituted kernel placement.
package fcvm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestNativeSnapshotInputLinuxRejectsSubstitutedPlacementBeforeOpen(t *testing.T) {
	for _, name := range []string{"other_anchor", "other_root", "other_executable", "unknown_reference", "readonly", "hardlink", "unfinished", "removed"} {
		t.Run(name, func(t *testing.T) {
			j, _, _, root, _ := nativeSnapshotInputJournalFixture(t)
			records, err := j.records()
			if err != nil {
				t.Fatal(err)
			}
			record, ref := records[0], records[0].References[0]
			base := filepath.Dir(filepath.Dir(filepath.Dir(root)))
			point := filepath.Join(base, ".native-processes", "image-sources", "points", record.Epoch)
			switch name {
			case "other_anchor":
				point = j.anchor(record)
			case "other_root":
				ref.Root = filepath.Join(t.TempDir(), "firecracker", ref.Owner.Lease.Instance, "root")
			case "other_executable":
				ref.Root = filepath.Join(base, "untrusted", ref.Owner.Lease.Instance, "root")
			case "unknown_reference":
				ref.ID = uuid.NewString()
			case "readonly":
				ref.ReadOnly, ref.AddPerms = true, 0o044
			case "hardlink":
				ref.Link, ref.MountID = true, 0
			case "unfinished":
				ref.Ready = false
			case "removed":
				ref.Removed, ref.TargetRemoved = true, true
			}
			if name != "unknown_reference" {
				record.References[0] = ref
				record.Desired, err = desiredNativeImageMetadata(record)
				if err != nil {
					t.Fatal(err)
				}
				record.Applied = record.Desired
			}
			file, err := (linuxNativeImageSources{base: base}).OpenSnapshotInput(record, ref, point)
			if file != nil {
				_ = file.Close()
				t.Error("substituted placement returned a descriptor")
			}
			if err == nil {
				t.Fatal("substituted placement reached native export")
			}
			if err.Error() != "native snapshot input: original private writable binding is required" {
				t.Fatalf("placement bypassed the input preflight: %v", err)
			}
			if _, err := os.Lstat(point); !os.IsNotExist(err) && name != "other_anchor" {
				t.Fatalf("input proof created an output: %v", err)
			}
		})
	}
}
