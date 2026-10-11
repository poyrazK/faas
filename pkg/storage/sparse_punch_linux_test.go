//go:build linux

package storage

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A memory-shaped stream (one data page in every sixteen) must not leave the
// filesystem's speculative preallocation inside the copy: allocation stays at
// the data written. On XFS this failed by tens of MiB before punchHoles.
func TestCopySparseReleasesSpeculativeAllocation(t *testing.T) {
	const page = 4096
	const pages = 256 << 20 / page
	src := make([]byte, pages*page)
	for i := 0; i < pages; i += 16 {
		src[i*page] = byte(i%251 + 1)
	}
	f, err := os.Create(filepath.Join(t.TempDir(), "mem"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	written, data, err := CopySparse(t.Context(), f, bytes.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if written != int64(len(src)) || data != pages/16*page {
		t.Fatalf("written=%d data=%d", written, data)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	allocated := info.Sys().(*syscall.Stat_t).Blocks * 512
	if allocated > data+1<<20 {
		t.Fatalf("copy allocates %d bytes for %d bytes of data (%s)", allocated, data, os.TempDir())
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(f)
	if err != nil || !bytes.Equal(got, src) {
		t.Fatalf("copied bytes differ: %v", err)
	}
}
