package imaged

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/storage"
)

// publishedLayerDigests remembers the digest the layer build computed while
// publishing each app layer, keyed by storage key, so the deploy scan does not
// hash the same bytes again. Production app layers are 1–2 GiB ext4 images and
// hashing one cost 7–8 s of every scan on the fleet's CPUs (2026-10-30).
//
// It is process-local and best effort. An entry is used only while the
// backend's local file for the key is still the file the build published:
// the same file (device and inode) with the same size. Local backends replace
// a file through a new one, never in place, and the node cache touches a
// file's modification time on every lookup, so the inode is what identifies
// the bytes. A restart, an evicted or refreshed cache file, or a backend
// without local files hashes as before.
type publishedLayerDigests struct {
	mu      sync.Mutex
	entries map[string]publishedLayerDigest
}

type publishedLayerDigest struct {
	digest   string
	file     os.FileInfo
	recorded time.Time
}

// record remembers digest for key when be keeps the published bytes in a
// local file. A nil receiver records nothing.
func (m *publishedLayerDigests) record(be storage.StorageBackend, key, digest string) {
	if m == nil || digest == "" {
		return
	}
	file, ok := localStorageFile(be, key)
	if !ok {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries == nil {
		m.entries = map[string]publishedLayerDigest{}
	}
	if _, replacing := m.entries[key]; !replacing && len(m.entries) >= api.ImagedPublishedLayerDigests {
		oldest := ""
		for k, e := range m.entries {
			if oldest == "" || e.recorded.Before(m.entries[oldest].recorded) {
				oldest = k
			}
		}
		delete(m.entries, oldest)
	}
	m.entries[key] = publishedLayerDigest{digest: digest, file: file, recorded: time.Now()}
}

// take returns the digest recorded for key, with be's local file for it, if
// that file is still the one recorded. It forgets the entry either way. Call
// it after staging the scan copy, so a file replaced while staging reads as a
// miss.
func (m *publishedLayerDigests) take(be storage.StorageBackend, key string) (string, os.FileInfo, bool) {
	if m == nil {
		return "", nil, false
	}
	m.mu.Lock()
	entry, ok := m.entries[key]
	delete(m.entries, key)
	m.mu.Unlock()
	if !ok {
		return "", nil, false
	}
	file, ok := localStorageFile(be, key)
	if !ok || !os.SameFile(entry.file, file) || file.Size() != entry.file.Size() {
		return "", nil, false
	}
	return entry.digest, file, true
}

func localStorageFile(be storage.StorageBackend, key string) (os.FileInfo, bool) {
	resolver, ok := be.(storage.LocalPathResolver)
	if !ok {
		return nil, false
	}
	path, local, err := resolver.LocalPath(key)
	if err != nil || !local {
		return nil, false
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	return info, true
}

// buildAppLayer runs the layer build and remembers the digest it computed for
// the published layer (publishedLayerDigests).
func (h *Handler) buildAppLayer(ctx context.Context, in rootfs.BuildInput) (rootfs.BuildResult, error) {
	result, err := h.builder.Build(ctx, in)
	if err == nil && result.ImageKey != "" {
		h.layerDigests.record(in.Storage, result.ImageKey, result.ArtifactDigest)
	}
	return result, err
}

// scanArtifactDigest is digestScanArtifact for the layer stored under key,
// staged at path, without hashing it again when this process just published
// it.
func (h *Handler) scanArtifactDigest(be storage.StorageBackend, key, path string) (string, error) {
	if digest, stored, ok := h.layerDigests.take(be, key); ok {
		// As in stageScanExt4: the local backend's scan reads the stored file
		// in place, and any other backend's scan reads a copy staged from it.
		if _, inPlace := be.(*storage.LocalStorageBackend); !inPlace {
			return digest, nil
		}
		if _, scanned, err := scanArtifactFile(path); err == nil && os.SameFile(scanned, stored) {
			return digest, nil
		}
	}
	return digestScanArtifact(path)
}
