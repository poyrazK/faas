//go:build metal && linux

package storage

import (
	"context"
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type repeatedArtifactReader struct{}

func (repeatedArtifactReader) Read(b []byte) (int, error) {
	for i := range b {
		b[i] = byte(i % 251)
	}
	return len(b), nil
}

// Run in a disposable systemd cgroup with MemoryMax=256M and disk-backed
// TMPDIR. This is the OCI staging + cache-fill shape that OOM-killed
// schedd with a 23 MiB heap and 216 MiB of dirty file pages.
func TestMetalLargeArtifactWriteback(t *testing.T) {
	const size = int64(512 << 20)
	src, err := os.Create(filepath.Join(t.TempDir(), "download"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = src.Close() }()
	expected := sha256.New()
	n, err := copyContextHash(context.Background(), src, io.LimitReader(repeatedArtifactReader{}, size), expected)
	if err != nil || n != size {
		t.Fatalf("download: bytes=%d err=%v", n, err)
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	cache, err := os.Create(filepath.Join(t.TempDir(), "cache"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cache.Close() }()
	n, err = copyContext(context.Background(), cache, src)
	if err != nil || n != size {
		t.Fatalf("cache fill: bytes=%d err=%v", n, err)
	}
	if err := cache.Sync(); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	got := sha256.New()
	if _, err := io.Copy(got, cache); err != nil {
		t.Fatal(err)
	}
	if string(got.Sum(nil)) != string(expected.Sum(nil)) {
		t.Fatal("artifact changed during download/cache fill")
	}
	t.Logf("verified %d-byte artifact through download and cache fill", size)
}
