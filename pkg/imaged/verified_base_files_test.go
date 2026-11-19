package imaged

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/storage"
)

// readCountingBackend counts the bytes read from every object it serves,
// keeping the served file's Stat so cache identities still resolve.
type readCountingBackend struct {
	storage.StorageBackend
	read int
}

type readCountingFile struct {
	io.ReadCloser
	backend *readCountingBackend
}

func (f *readCountingFile) Read(p []byte) (int, error) {
	n, err := f.ReadCloser.Read(p)
	f.backend.read += n
	return n, err
}

func (f *readCountingFile) Stat() (os.FileInfo, error) {
	if s, ok := f.ReadCloser.(interface{ Stat() (os.FileInfo, error) }); ok {
		return s.Stat()
	}
	return nil, os.ErrInvalid
}

func (b *readCountingBackend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	rc, err := b.StorageBackend.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	return &readCountingFile{ReadCloser: rc, backend: b}, nil
}

func baseArtifactFor(key string, body []byte) imagechain.BaseArtifact {
	sum := sha256.Sum256(body)
	return imagechain.BaseArtifact{StorageKey: key, Digest: "sha256:" + hex.EncodeToString(sum[:]), Bytes: int64(len(body))}
}

func cachedBaseFixture(t *testing.T) (*storage.LocalCacheBackend, *readCountingBackend) {
	t.Helper()
	parent, err := storage.NewLocalStorageBackend(filepath.Join(t.TempDir(), "parent"))
	if err != nil {
		t.Fatal(err)
	}
	cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(t.TempDir(), "cache"), 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	return cache, &readCountingBackend{StorageBackend: cache}
}

// ADR-595 amendment 2026-11-17: a base this process verified from its node
// cache is not hashed again while the cache holds the same file.
func TestCachedBaseArtifactIsHashedOncePerCacheFile(t *testing.T) {
	ctx := t.Context()
	const key = "base/runner-test-amd64.ext4"
	body := bytes.Repeat([]byte("base ext4 "), 4096)
	cache, counting := cachedBaseFixture(t)
	if err := cache.Put(ctx, key, bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	h := &Handler{verifiedBases: &verifiedBaseFiles{}}
	artifact := baseArtifactFor(key, body)

	if err := h.checkCachedBaseArtifact(ctx, counting, artifact); err != nil {
		t.Fatalf("first check: %v", err)
	}
	if counting.read != len(body) {
		t.Fatalf("first check read %d bytes, want the whole base (%d)", counting.read, len(body))
	}
	if err := h.checkCachedBaseArtifact(ctx, counting, artifact); err != nil {
		t.Fatalf("second check: %v", err)
	}
	if counting.read != len(body) {
		t.Fatalf("second check read %d more bytes, want the remembered verification", counting.read-len(body))
	}

	// A new producer of the key is another artifact: hash again.
	other := artifact
	other.Digest = "sha256:" + hex.EncodeToString(make([]byte, 32))
	if err := h.checkCachedBaseArtifact(ctx, counting, other); err == nil {
		t.Fatal("a different artifact digest verified against the old bytes")
	}

	// The cache replaces a file by renaming another over it: the same
	// artifact must be hashed from the new file, and its bytes refused.
	if err := h.checkCachedBaseArtifact(ctx, counting, artifact); err != nil {
		t.Fatalf("re-verify the original bytes: %v", err)
	}
	if err := cache.Put(ctx, key, bytes.NewReader(bytes.Repeat([]byte("tampered!!"), 4096))); err != nil {
		t.Fatal(err)
	}
	before := counting.read
	if err := h.checkCachedBaseArtifact(ctx, counting, artifact); err == nil {
		t.Fatal("a replaced cache file verified from the remembered record")
	}
	if counting.read == before {
		t.Fatal("a replaced cache file was not hashed")
	}
	if h.verifiedBases.matches(artifact, baseFile{}) || len(h.verifiedBases.entries) != 0 {
		t.Fatalf("a failed verification left a record: %+v", h.verifiedBases.entries)
	}
}

type remoteBaseBackend struct {
	storage.StorageBackend
	body  []byte
	reads int
}

func (b *remoteBaseBackend) Get(context.Context, string) (io.ReadCloser, error) {
	b.reads++
	return io.NopCloser(bytes.NewReader(b.body)), nil
}

func TestRemoteBaseArtifactIsAlwaysHashed(t *testing.T) {
	body := []byte("remote base bytes")
	remote := &remoteBaseBackend{body: body}
	h := &Handler{verifiedBases: &verifiedBaseFiles{}}
	artifact := baseArtifactFor("base/remote-amd64.ext4", body)
	for range 2 {
		if err := h.checkCachedBaseArtifact(t.Context(), remote, artifact); err != nil {
			t.Fatal(err)
		}
	}
	if remote.reads != 2 || len(h.verifiedBases.entries) != 0 {
		t.Fatalf("reads %d, records %d: a stream without a file identity must be hashed every time", remote.reads, len(h.verifiedBases.entries))
	}
	// A handler built without the memo still verifies.
	if err := (&Handler{}).checkCachedBaseArtifact(t.Context(), remote, artifact); err != nil {
		t.Fatal(err)
	}
}
