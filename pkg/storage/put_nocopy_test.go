package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTempBlob returns a file positioned at EOF, which is the state a
// caller's file is in after it has been written — the fast path has to
// rewind it itself rather than assuming offset 0.
func writeTempBlob(t *testing.T, data string) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "blob-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	if _, err := f.WriteString(data); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// TestHashInPlace_NoCopyAndRewinds pins the fast path: hashing a blob
// the caller already has on disk must not write a second copy, must
// return the caller's own path, and must leave the file rewound so the
// uploader can read it straight through.
func TestHashInPlace_NoCopyAndRewinds(t *testing.T) {
	const payload = "snapshot-bytes-that-must-not-be-copied"
	f := writeTempBlob(t, payload)
	dir := filepath.Dir(f.Name())
	before, _ := os.ReadDir(dir)

	path, digest, ok := hashInPlace(context.Background(), f)
	if !ok {
		t.Fatal("hashInPlace reported not-ok for a plain *os.File")
	}
	if path != f.Name() {
		t.Errorf("path = %q, want the caller's own file %q (a different path means a copy was made)", path, f.Name())
	}
	if digest != sha256Hex(payload) {
		t.Errorf("digest = %q, want %q", digest, sha256Hex(payload))
	}
	if after, _ := os.ReadDir(dir); len(after) != len(before) {
		t.Errorf("directory entry count changed %d -> %d; the fast path must not create a spool file", len(before), len(after))
	}
	// Rewound: the uploader reads from here.
	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll after hashInPlace: %v", err)
	}
	if string(got) != payload {
		t.Errorf("file was left at offset %d bytes in; the uploader would send a truncated blob", len(payload)-len(got))
	}
}

// TestBufferAndHash_FileInputIsNotOwned is the safety pin. When the
// blob was hashed in place, the returned path belongs to the CALLER —
// the read-through cache's spool, or a snapshot blob on the capture
// path. OCI Put deletes the path it is given when it owns it, so a
// wrong `owned` here silently destroys the caller's data.
func TestBufferAndHash_FileInputIsNotOwned(t *testing.T) {
	const payload = "caller-owned-blob"
	f := writeTempBlob(t, payload)
	o := &OCIRegistryStorageBackend{}

	path, digest, owned, err := o.bufferAndHash(context.Background(), "snap/x/mem", f)
	if err != nil {
		t.Fatalf("bufferAndHash: %v", err)
	}
	if owned {
		t.Error("owned = true for a caller-supplied file; Put would delete the caller's blob")
	}
	if path != f.Name() {
		t.Errorf("path = %q, want %q", path, f.Name())
	}
	if digest != sha256Hex(payload) {
		t.Errorf("digest = %q, want %q", digest, sha256Hex(payload))
	}
	// Simulate Put's ownership-gated cleanup.
	if owned {
		_ = removeTmp(path)
	}
	if _, err := os.Stat(f.Name()); err != nil {
		t.Errorf("caller's file was removed: %v", err)
	}
}

// TestBufferAndHash_NonFileStillSpools keeps the fallback honest: a
// plain stream has no path to hash in place, so it must spool to a temp
// the backend owns and is responsible for deleting.
func TestBufferAndHash_NonFileStillSpools(t *testing.T) {
	const payload = "streamed-blob-without-a-file"
	o := &OCIRegistryStorageBackend{}

	path, digest, owned, err := o.bufferAndHash(context.Background(), "snap/x/mem", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("bufferAndHash: %v", err)
	}
	t.Cleanup(func() { _ = removeTmp(path) })
	if !owned {
		t.Error("owned = false for a spooled stream; the temp file would leak")
	}
	if digest != sha256Hex(payload) {
		t.Errorf("digest = %q, want %q", digest, sha256Hex(payload))
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read spool: %v", err)
	}
	if string(got) != payload {
		t.Errorf("spooled %q, want %q", got, payload)
	}
}

// fileSpyBackend records whether the parent was handed a file-backed
// reader. That is the whole point of the change: only a file lets the
// OCI parent hash in place instead of writing the blob to disk twice.
type fileSpyBackend struct {
	gotFile bool
	data    string
}

func (f *fileSpyBackend) Put(_ context.Context, _ string, r io.Reader) error {
	_, f.gotFile = r.(interface {
		io.Reader
		io.Seeker
		Name() string
	})
	b, err := io.ReadAll(r)
	f.data = string(b)
	return err
}

func (f *fileSpyBackend) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, ErrNotFound
}
func (f *fileSpyBackend) Delete(context.Context, string) error { return nil }

// TestCachePut_HandsParentAFileNotAStream pins the half of the fix that
// lives in the cache. Teeing the caller's stream through to the parent
// forced the parent to spool its own copy, because it cannot upload
// until it knows the digest. Measured cost on a production node: ~52 s
// of a ~100 s 1 GiB capture, against a 75 s budget.
//
// The parent must receive the spool FILE, already rewound and complete.
func TestCachePut_HandsParentAFileNotAStream(t *testing.T) {
	parent := &fileSpyBackend{}
	cache, err := NewLocalCacheBackend(parent, filepath.Join(t.TempDir(), "cache"), 0)
	if err != nil {
		t.Fatalf("NewLocalCacheBackend: %v", err)
	}
	payload := strings.Repeat("mem-page-", 4096)
	if err := cache.Put(context.Background(), "snap/dep/mem", strings.NewReader(payload)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if !parent.gotFile {
		t.Error("parent received a non-file reader; it will spool its own copy and write the blob to disk twice")
	}
	if parent.data != payload {
		t.Errorf("parent received %d bytes, want %d (a non-rewound file would truncate the upload)", len(parent.data), len(payload))
	}
}
