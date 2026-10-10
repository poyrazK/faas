//go:build linux && metal

package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The ownership/receipt is modeled and the empty cgroup is real. This tests
// retirement identity and crash acknowledgement, not VM lifecycle acceptance.
func TestMetalNativeSnapshotMemoryRetirement(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root and the managed unified hierarchy")
	}
	withCgroupRootAt(t, "/sys/fs/cgroup")
	for _, scenario := range []string{"removed", "retained_inode", "replacement", "lost_disposal_ack"} {
		t.Run(scenario, func(t *testing.T) {
			g, _ := nativeSnapshotMemoryFixture(t)
			owner := g.owner
			owner.Revoked, owner.ExitConfirmed = true, true
			if err := g.j.owner.write(owner); err != nil {
				t.Fatal(err)
			}
			path := nativeCgroupScope(owner.Lease)
			if _, err := os.Stat(filepath.Dir(path)); err != nil {
				t.Skip("requires an existing managed tenant parent:", err)
			}
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				for _, name := range []string{path} {
					if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
						t.Error("private empty cgroup cleanup:", err)
					}
				}
			})
			file, group, err := openNativeSnapshotMemoryCgroup(owner)
			if err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			record := g.record
			record.SnapshotOutput.Memory.Group, record.SnapshotOutput.Memory.Phase = group, nativeSnapshotMemoryRestored
			if err := g.j.write(owner, record); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "removed":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "replacement":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
			case "lost_disposal_ack":
				g.j.writeValue = func(string, nativeHostHelperRecord) error {
					return errors.New("lost original disposal journal acknowledgement")
				}
			}
			ctx, stop := context.WithTimeout(t.Context(), 10*time.Second)
			defer stop()
			if scenario == "retained_inode" {
				if err := requireNativeSnapshotMemoryInodeGone(ctx, group); err == nil {
					t.Fatal("existing original inode supplied absence proof")
				}
				if _, err := os.Stat(path); err != nil {
					t.Fatal("inode inventory changed the original scope", err)
				}
				if g.j.requireSnapshotMemoryDisposed(owner) == nil {
					t.Fatal("retained scope supplied disposal")
				}
				return
			}
			handled, err := g.j.removeSnapshotMemoryCgroup(ctx, owner)
			refuses := scenario != "removed"
			if !handled || refuses != (err != nil) {
				t.Fatal("original cgroup retirement outcome:", handled, err)
			}
			if scenario == "replacement" {
				if _, err := os.Stat(path); err != nil {
					t.Fatal("replacement scope was changed", err)
				}
				if g.j.requireSnapshotMemoryDisposed(owner) == nil {
					t.Fatal("replacement scope supplied disposal")
				}
				return
			}
			if scenario == "lost_disposal_ack" {
				if g.j.requireSnapshotMemoryDisposed(owner) == nil {
					t.Fatal("uncertain disposal supplied acknowledgement")
				}
				g.j.writeValue = nil
				if handled, err := g.j.removeSnapshotMemoryCgroup(ctx, owner); err != nil || !handled {
					t.Fatal("original inode absence did not repair disposal receipt", handled, err)
				}
			}
			if err := g.j.requireSnapshotMemoryDisposed(owner); err != nil {
				t.Fatal(err)
			}
		})
	}
}
