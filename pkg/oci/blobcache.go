package oci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// BlobCache is the registry-blob cache seam used by RegistryClient. The
// fetch function is called only on a cache miss and must return a body that
// the cache owns and closes. The returned reader is always an independent
// reader owned by the caller.
//
// The digest is the cache key. OCI blobs are content-addressed, so a digest
// is safe to share between repositories and private-registry credentials do
// not become part of the cache key.
type BlobCache interface {
	Open(ctx context.Context, digest string, fetch func(context.Context) (io.ReadCloser, error)) (io.ReadCloser, error)
}

// BlobCacheObserver receives bounded cache lifecycle events. Implementations
// must be fast and non-blocking; callbacks run outside the cache mutex.
type BlobCacheObserver interface {
	OnHit()
	OnMiss()
	OnEviction()
}

// BlobCacheObserverFuncs adapts callbacks from a metrics or logging package
// without making pkg/oci depend on that package.
type BlobCacheObserverFuncs struct {
	Hit      func()
	Miss     func()
	Eviction func()
}

func (f BlobCacheObserverFuncs) OnHit() {
	if f.Hit != nil {
		f.Hit()
	}
}

func (f BlobCacheObserverFuncs) OnMiss() {
	if f.Miss != nil {
		f.Miss()
	}
}

func (f BlobCacheObserverFuncs) OnEviction() {
	if f.Eviction != nil {
		f.Eviction()
	}
}

// DefaultBlobCacheMaxBytes is the per-node OCI blob cache budget. It is
// intentionally separate from storage.DefaultCacheMaxBytes so operators can
// size registry layers independently from snapshots and ext4 artifacts.
const DefaultBlobCacheMaxBytes int64 = 8 << 30

var errBlobCacheOversized = errors.New("oci: blob exceeds cache budget")

// DiskBlobCache stores verified OCI blobs on local disk. A miss is materialised
// through a temporary file and atomically renamed into the content-addressed
// path, so a daemon crash cannot leave a partial blob looking like a hit.
// Concurrent misses for the same digest are coalesced with singleflight.
type DiskBlobCache struct {
	root     string
	maxBytes int64
	mu       sync.Mutex
	flights  singleflight.Group
	observer BlobCacheObserver
}

// NewDiskBlobCache creates a disk-backed OCI blob cache rooted at root.
// maxBytes <= 0 uses DefaultBlobCacheMaxBytes. The cache contains no
// credentials; only verified content-addressed blob bytes are written.
func NewDiskBlobCache(root string, maxBytes int64) (*DiskBlobCache, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("oci: blob cache: empty root dir")
	}
	if maxBytes <= 0 {
		maxBytes = DefaultBlobCacheMaxBytes
	}
	if err := os.MkdirAll(root, 0o770); err != nil {
		return nil, fmt.Errorf("oci: blob cache: mkdir %q: %w", root, err)
	}
	_ = os.Chmod(root, 0o770)
	return &DiskBlobCache{root: root, maxBytes: maxBytes}, nil
}

// Root reports the cache root for startup/readiness logging.
func (c *DiskBlobCache) Root() string { return c.root }

// SetObserver attaches the optional bounded event sink. Replacing the
// observer is safe while pulls are in flight.
func (c *DiskBlobCache) SetObserver(observer BlobCacheObserver) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.observer = observer
	c.mu.Unlock()
}

// Open returns a verified cached blob or fetches and persists it before
// returning a reader. A cache miss never exposes a partially written file.
// If a single blob is larger than the configured budget, the materialisation
// is discarded and the fetch is repeated once as an uncached stream. This
// keeps the cache bounded without making large images undeployable.
func (c *DiskBlobCache) Open(ctx context.Context, digest string, fetch func(context.Context) (io.ReadCloser, error)) (io.ReadCloser, error) {
	if fetch == nil {
		return nil, errors.New("oci: blob cache: nil fetcher")
	}
	if c == nil {
		return fetch(ctx)
	}
	if err := validateDigest(digest); err != nil {
		return nil, err
	}

	path := c.blobPath(digest)
	if f, ok := c.openCached(path); ok {
		c.notifyHit()
		return f, nil
	}

	result := c.flights.DoChan(digest, func() (any, error) {
		// Another caller may have installed the entry between the initial
		// check and acquiring the singleflight slot.
		if c.hasCached(path) {
			c.notifyHit()
			return path, nil
		}
		c.notifyMiss()
		rc, err := fetch(ctx)
		if err != nil {
			return nil, err
		}
		installed, err := c.materialize(ctx, digest, path, rc)
		if err != nil {
			if errors.Is(err, errBlobCacheOversized) {
				return nil, errBlobCacheOversized
			}
			return nil, err
		}
		return installed, nil
	})

	var shared singleflight.Result
	select {
	case shared = <-result:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if shared.Err != nil {
		if errors.Is(shared.Err, errBlobCacheOversized) {
			// The leader consumed the first body while measuring it. Fetch a
			// fresh body for this caller rather than retaining an oversized
			// temporary file or failing an otherwise valid deployment.
			return fetch(ctx)
		}
		return nil, shared.Err
	}
	installed, ok := shared.Val.(string)
	if !ok || installed == "" {
		return nil, errors.New("oci: blob cache: invalid singleflight result")
	}
	f, err := os.Open(installed) //nolint:forbidigo // path is derived from a validated digest under the configured cache root.
	if err != nil {
		// A concurrent budget sweep may have evicted the entry after the
		// singleflight completed. Preserve pull availability by fetching a
		// fresh body rather than surfacing a cache-maintenance race.
		return fetch(ctx)
	}
	c.touch(installed)
	return f, nil
}

func (c *DiskBlobCache) blobPath(digest string) string {
	hexDigest := strings.TrimPrefix(digest, digestAlgo)
	return filepath.Join(c.root, hexDigest[:2], hexDigest[2:])
}

func (c *DiskBlobCache) openCached(path string) (io.ReadCloser, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f, err := os.Open(path) //nolint:forbidigo // path is derived from a validated digest under the configured cache root.
	if err != nil {
		return nil, false
	}
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		_ = f.Close()
		return nil, false
	}
	c.touchLocked(path)
	return f, true
}

func (c *DiskBlobCache) hasCached(path string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

func (c *DiskBlobCache) materialize(ctx context.Context, digest, path string, src io.ReadCloser) (string, error) {
	if src == nil {
		return "", errors.New("oci: blob cache: fetcher returned nil body")
	}
	defer func() { _ = src.Close() }()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o770); err != nil {
		return "", fmt.Errorf("oci: blob cache: mkdir %q: %w", dir, err)
	}
	_ = os.Chmod(dir, 0o770)
	tmp, err := os.CreateTemp(dir, ".faas-oci-blob-*")
	if err != nil {
		return "", fmt.Errorf("oci: blob cache: temp file: %w", err)
	}
	tmpPath := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	h := sha256.New()
	n, err := copyWithContext(ctx, io.MultiWriter(tmp, h), src)
	if err != nil {
		return "", fmt.Errorf("oci: blob cache: copy %s: %w", digest, err)
	}
	if got := digestAlgo + hex.EncodeToString(h.Sum(nil)); got != digest {
		return "", fmt.Errorf("oci: blob cache: digest mismatch: requested %s, got %s", digest, got)
	}
	if err := tmp.Sync(); err != nil {
		return "", fmt.Errorf("oci: blob cache: sync %s: %w", digest, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("oci: blob cache: close %s: %w", digest, err)
	}
	if n > c.maxBytes {
		return "", errBlobCacheOversized
	}

	c.mu.Lock()
	if err := os.Rename(tmpPath, path); err != nil {
		c.mu.Unlock()
		return "", fmt.Errorf("oci: blob cache: install %s: %w", digest, err)
	}
	keep = true
	_ = os.Chmod(path, 0o664)
	c.touchLocked(path)
	evicted, err := c.enforceBudgetLocked(path)
	c.mu.Unlock()
	if err != nil {
		// Eviction is a cache-maintenance concern. The verified blob remains
		// usable even if a directory walk or remove fails.
		return path, nil
	}
	c.notifyEvictions(evicted)
	return path, nil
}

func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, 32*1024)
	var n int64
	for {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		read, err := src.Read(buf)
		if read > 0 {
			written, werr := dst.Write(buf[:read])
			n += int64(written)
			if werr != nil {
				return n, werr
			}
			if written != read {
				return n, io.ErrShortWrite
			}
		}
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return n, err
		}
	}
}

type blobCacheEntry struct {
	path    string
	size    int64
	modTime int64
}

func (c *DiskBlobCache) enforceBudgetLocked(installedPath string) (int, error) {
	entries := make([]blobCacheEntry, 0)
	var total int64
	err := filepath.WalkDir(c.root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".faas-oci-blob-") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		total += info.Size()
		// Admission is the newest access, regardless of filesystem timestamp
		// resolution or clock skew. Count this blob toward the budget, but
		// do not evict it during its own admission sweep. materialize rejects
		// oversized blobs, so evicting older entries can still meet the limit.
		if path == installedPath {
			return nil
		}
		entries = append(entries, blobCacheEntry{path: path, size: info.Size(), modTime: info.ModTime().UnixNano()})
		return nil
	})
	if err != nil || total <= c.maxBytes {
		return 0, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].modTime < entries[j].modTime })
	removed := 0
	for _, entry := range entries {
		if total <= c.maxBytes {
			break
		}
		if err := os.Remove(entry.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			continue
		}
		total -= entry.size
		removed++
	}
	return removed, nil
}

func (c *DiskBlobCache) touch(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.touchLocked(path)
}

func (c *DiskBlobCache) touchLocked(path string) {
	now := time.Now()
	_ = os.Chtimes(path, now, now)
}

func (c *DiskBlobCache) notifyHit() {
	c.mu.Lock()
	observer := c.observer
	c.mu.Unlock()
	if observer != nil {
		observer.OnHit()
	}
}

func (c *DiskBlobCache) notifyMiss() {
	c.mu.Lock()
	observer := c.observer
	c.mu.Unlock()
	if observer != nil {
		observer.OnMiss()
	}
}

func (c *DiskBlobCache) notifyEvictions(count int) {
	if count == 0 {
		return
	}
	c.mu.Lock()
	observer := c.observer
	c.mu.Unlock()
	if observer != nil {
		for i := 0; i < count; i++ {
			observer.OnEviction()
		}
	}
}
