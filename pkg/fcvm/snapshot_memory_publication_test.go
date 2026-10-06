// adr: 005
package fcvm

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A capture is usable only after the destination memory file has completed
// writeback. A failed durability barrier must propagate to capture cleanup.
func TestLocalSnapshotMemoryPublicationWaitsForWriteback(t *testing.T) {
	wantErr := errors.New("memory writeback failed")
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "sync failure"}[fail], func(t *testing.T) {
			dir := t.TempDir()
			source, destination := filepath.Join(dir, "captured"), filepath.Join(dir, "published")
			const contents = "complete memory capture"
			if err := os.WriteFile(source, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			size, err := publishLocalSnapshotMemory(source, destination, func(path string) error {
				calls++
				if path != destination {
					t.Fatal("writeback did not target the published destination")
				}
				got, err := os.ReadFile(path)
				if err != nil || string(got) != contents {
					t.Fatalf("writeback ran before publication: %q %v", got, err)
				}
				if fail {
					return wantErr
				}
				return syncLocalSnapshotMemory(path)
			})
			if calls != 1 {
				t.Fatalf("writeback calls = %d, want 1", calls)
			}
			if fail {
				if !errors.Is(err, wantErr) || size != 0 {
					t.Fatalf("failed publication = (%d, %v), want 0 and writeback error", size, err)
				}
			} else if err != nil || size != int64(len(contents)) {
				t.Fatalf("publication = (%d, %v)", size, err)
			}
		})
	}
}

func TestLocalSnapshotMemoryMoveFailureDoesNotSync(t *testing.T) {
	dir := t.TempDir()
	size, err := publishLocalSnapshotMemory(filepath.Join(dir, "missing"), filepath.Join(dir, "published"), func(string) error {
		t.Fatal("writeback ran after a failed move")
		return nil
	})
	if !errors.Is(err, os.ErrNotExist) || size != 0 {
		t.Fatalf("missing capture = (%d, %v)", size, err)
	}
}

func TestLocalSnapshotMemorySyncRejectsNonRegularInputs(t *testing.T) {
	dir := t.TempDir()
	file, link := filepath.Join(dir, "memory"), filepath.Join(dir, "alias")
	if err := os.WriteFile(file, []byte("memory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, dir, filepath.Join(dir, "missing")} {
		if err := syncLocalSnapshotMemory(path); err == nil {
			t.Fatalf("accepted non-regular memory input %s", path)
		}
	}
}
