package imaged

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/storage"
)

// baseFile is the node-cache file a base artifact was hashed from. The cache
// touches a file's times on every read to drive eviction, so only the device,
// inode and size identify it.
type baseFile struct {
	device, inode uint64
	size          int64
}

// verifiedBaseFile records that the cache file named by file held the bytes
// of a producer's base artifact when this process hashed it.
type verifiedBaseFile struct {
	digest string
	bytes  int64
	file   baseFile
}

// verifiedBaseFiles remembers the base artifacts this imaged process has
// verified from its node cache (ADR-595 amendment 2026-11-17). Every deploy
// re-checks the runtime base, and a runtime with a parent re-checks both, so
// without it each deploy hashed one or two 0.5 GiB files it had already
// hashed. The node cache writes a file once and replaces it only by renaming
// another over it, so the same file holds the same bytes; a replaced,
// evicted or re-fetched file is another file and is hashed again. The
// records live in memory, so a restarted imaged hashes each base once more.
type verifiedBaseFiles struct {
	mu      sync.Mutex
	entries map[string]verifiedBaseFile
}

func (v *verifiedBaseFiles) matches(expected imagechain.BaseArtifact, file baseFile) bool {
	if v == nil {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	got, ok := v.entries[expected.StorageKey]
	return ok && got.digest == expected.Digest && got.bytes == expected.Bytes && got.file == file
}

func (v *verifiedBaseFiles) record(expected imagechain.BaseArtifact, file baseFile) {
	if v == nil {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.entries == nil {
		v.entries = make(map[string]verifiedBaseFile)
	}
	if _, ok := v.entries[expected.StorageKey]; !ok && len(v.entries) >= api.ImagedVerifiedBaseFiles {
		// The keys are the runtime matrix's bases; a full table means
		// something unexpected, so start over rather than pick a victim.
		clear(v.entries)
	}
	v.entries[expected.StorageKey] = verifiedBaseFile{digest: expected.Digest, bytes: expected.Bytes, file: file}
}

func (v *verifiedBaseFiles) forget(key string) {
	if v == nil {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.entries, key)
}

// cachedBaseFile identifies the regular file behind a cache read. A reader
// that is not a local file, such as a remote stream, has no identity, and
// its bytes are always hashed.
func cachedBaseFile(rc io.Reader) (baseFile, bool) {
	stater, ok := rc.(interface{ Stat() (os.FileInfo, error) })
	if !ok {
		return baseFile{}, false
	}
	fi, err := stater.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return baseFile{}, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return baseFile{}, false
	}
	return baseFile{device: uint64(st.Dev), inode: st.Ino, size: fi.Size()}, true
}

// checkCachedBaseArtifact is checkStoredBaseArtifact for this process: it
// hashes the stored bytes unless it already hashed this very cache file for
// the same artifact. The identity comes from the descriptor it reads, so the
// bytes it hashes and the file it records are the same.
func (h *Handler) checkCachedBaseArtifact(ctx context.Context, be storage.StorageBackend, expected imagechain.BaseArtifact) error {
	if !expected.Valid() {
		return fmt.Errorf("imaged: invalid complete base identity")
	}
	rc, err := be.Get(ctx, expected.StorageKey)
	if err != nil {
		return err
	}
	file, identified := cachedBaseFile(rc)
	if identified && h.verifiedBases.matches(expected, file) {
		return rc.Close()
	}
	if err := verifyBaseArtifactBytes(ctx, rc, expected); err != nil {
		h.verifiedBases.forget(expected.StorageKey)
		return err
	}
	if identified {
		h.verifiedBases.record(expected, file)
	}
	return nil
}

// verifyBaseArtifactBytes hashes rc, closes it, and compares the result with
// the producer's artifact.
func verifyBaseArtifactBytes(ctx context.Context, rc io.ReadCloser, expected imagechain.BaseArtifact) error {
	identity, readErr := rootfs.ReadArtifactIdentity(ctx, io.LimitReader(rc, expected.Bytes+1))
	closeErr := rc.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	if identity.Digest != expected.Digest || identity.Bytes != expected.Bytes {
		return fmt.Errorf("imaged: stored base differs from produced artifact")
	}
	return nil
}
