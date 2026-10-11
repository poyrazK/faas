// adr: 005
package fcvm

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
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
			size, _, err := publishLocalSnapshotMemory(t.Context(), source, destination, func(path string) error {
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
	size, _, err := publishLocalSnapshotMemory(t.Context(), filepath.Join(dir, "missing"), filepath.Join(dir, "published"), func(string) error {
		t.Fatal("writeback ran after a failed move")
		return nil
	})
	if !errors.Is(err, os.ErrNotExist) || size != 0 {
		t.Fatalf("missing capture = (%d, %v)", size, err)
	}
}

// The jail is tmpfs and the destination is /srv/fc, so production always takes
// the copy branch. It must keep zero pages as holes and report the content.
func TestCopySparseFileKeepsHoles(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "mem")
	const page = 4096
	data := make([]byte, 256*page)
	data[5*page] = 0x7f
	if err := os.WriteFile(src, data, 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "published")
	size, content, err := copySparseFile(t.Context(), src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if size != int64(len(data)) || content != page {
		t.Fatalf("copy = (%d, %d), want (%d, %d)", size, content, len(data), page)
	}
	got, err := os.ReadFile(dst)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("copied bytes differ: %v", err)
	}
	if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source not removed: %v", err)
	}
	if runtime.GOOS != "linux" {
		return // hole allocation is asserted on the Linux filesystems vmmd runs on
	}
	st, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if allocated := st.Sys().(*syscall.Stat_t).Blocks * 512; allocated >= int64(len(data))/2 {
		t.Fatalf("published memory is dense: %d of %d bytes allocated", allocated, len(data))
	}
}

// On one filesystem the rename wins and nothing is scanned.
func TestMoveOutSparseRenameReportsUnscanned(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "mem")
	if err := os.WriteFile(src, []byte("memory"), 0o600); err != nil {
		t.Fatal(err)
	}
	size, content, err := moveOutSparse(t.Context(), src, filepath.Join(dir, "out", "mem"))
	if err != nil || size != int64(len("memory")) || content != -1 {
		t.Fatalf("moveOutSparse = (%d, %d, %v), want (6, -1, nil)", size, content, err)
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
