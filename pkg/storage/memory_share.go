package storage

// Snapshot memory page sharing — ADR-956.
//
// A Firecracker memory file holds the guest's page cache: 4 KiB pages whose
// bytes equal 4 KiB blocks of the app layer and runtime base the node already
// caches. Measured on production (2026-10-10) that was 55–60% of every
// snapshot's non-zero memory. FIDEDUPERANGE lets the cached memory file share
// those blocks. The kernel compares the bytes before sharing, so a collision
// in the in-process page index only costs a refused attempt, never a wrong
// page, and a reader of either file always sees its original bytes.
//
// The 2026-10-31 amendment adds a second pass: the remaining pages are shared
// with the other memory files on the node, whose kernel data, runtime heaps
// and page tables repeat across snapshots. Measured on production compute-2
// that was another ~20% of every snapshot's non-zero memory.

import (
	"context"
	"errors"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MemoryShareResult reports one ShareSnapshotMemory call. Skipped is true
// when the memory is not cached on this node or was already processed.
type MemoryShareResult struct {
	// SharedBytes now share blocks of an app layer or runtime base.
	SharedBytes int64
	// PeerSharedBytes now share blocks of another memory file on the node.
	PeerSharedBytes int64
	Skipped         bool
}

// Levels recorded in the memorySharedXattr marker of a processed memory file.
const (
	memorySharedWithImages = 1 // shared with the app layer and runtime bases
	memorySharedWithPeers  = 2 // and with the node's other memory files
)

// baseIndexMaxAge bounds how long a base index is reused without checking
// which runtime bases are cached; a release refresh replaces a base in place.
const baseIndexMaxAge = 10 * time.Minute

// MemoryShareIndex keeps the block index of the node's cached runtime bases
// between snapshots: every snapshot of a runtime shares the same base, so
// indexing it once per base version keeps each pass to the app layer and the
// memory file. It also keeps the peer index of the memory files already
// processed, which later memory files share with. It is safe for concurrent
// use but is meant for one worker.
type MemoryShareIndex struct {
	mu        sync.Mutex
	base      *blockIndex
	baseFiles string
	checked   time.Time
	done      map[string]bool // path:dev:ino of processed memory files
	peers     *peerIndex      // nil when sharing between memory files is off
}

// NewMemoryShareIndex returns an index with sharing between memory files on.
func NewMemoryShareIndex() *MemoryShareIndex {
	return &MemoryShareIndex{done: map[string]bool{}, peers: newPeerIndex()}
}

// SetPeerSharing turns sharing between memory files on or off. Off, a pass
// shares only with app layers and runtime bases, as before the amendment.
func (x *MemoryShareIndex) SetPeerSharing(enabled bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	switch {
	case !enabled:
		x.peers = nil
	case x.peers == nil:
		x.peers = newPeerIndex()
	}
}

// PeerSharing reports whether passes share with other memory files. It is
// always false where blocks cannot be shared (non-Linux builds).
func (x *MemoryShareIndex) PeerSharing() bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.peers != nil
}

// ShareSnapshotMemory makes the cached copy of memKey share every page whose
// bytes equal a block of a cached layerKeys entry or runtime base, then every
// remaining run of pages found in a memory file already processed on this
// node. A memory file is processed once: the outcome is remembered on the
// file (an xattr, lost with the file when the cache replaces it) and in
// memory. A file processed before this daemon started is only added to the
// peer index, so later memory files can share with it.
func (x *MemoryShareIndex) ShareSnapshotMemory(ctx context.Context, backend StorageBackend, memKey string, layerKeys []string) (MemoryShareResult, error) {
	memPath, ok, err := cachedPath(backend, memKey)
	if err != nil || !ok {
		return MemoryShareResult{Skipped: true}, err
	}
	id, err := fileIdentity(memPath)
	if errors.Is(err, os.ErrNotExist) {
		return MemoryShareResult{Skipped: true}, nil // evicted, or a local parent path with no file
	}
	if err != nil {
		return MemoryShareResult{Skipped: true}, err
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.done[id] {
		return MemoryShareResult{Skipped: true}, nil
	}
	peers := x.peers
	peers.prune()
	level := memorySharedLevel(memPath)
	if level >= memorySharedWithPeers || (level > 0 && peers == nil) {
		// Processed before this daemon started: nothing is left to share,
		// but later memory files may share with it.
		if err := peers.addFile(ctx, memPath); err != nil {
			return MemoryShareResult{}, err
		}
		x.done[id] = true
		return MemoryShareResult{Skipped: true}, nil
	}
	var result MemoryShareResult
	if level < memorySharedWithImages {
		shared, err := x.shareWithImagesLocked(ctx, backend, memPath, layerKeys)
		if errors.Is(err, errDedupeUnsupported) {
			// ext4 hosts and local development: nothing can be shared, now or later.
			x.done[id] = true
			return MemoryShareResult{Skipped: true}, nil
		}
		result.SharedBytes = shared
		if err != nil {
			return result, err
		}
		setMemorySharedMarker(memPath, memorySharedWithImages)
	}
	if peers != nil {
		// Pages now shared with an image are left as they are; the rest are
		// matched against the memory files processed before this one.
		shared, err := peers.share(ctx, memPath)
		result.PeerSharedBytes = shared
		if errors.Is(err, errDedupeUnsupported) {
			// As for the image pass: nothing can be shared here.
			x.done[id] = true
			return MemoryShareResult{Skipped: true}, nil
		}
		if err != nil {
			return result, err
		}
		setMemorySharedMarker(memPath, memorySharedWithPeers)
	}
	x.done[id] = true
	return result, nil
}

// shareWithImagesLocked is the original ADR-956 pass: memPath's pages are
// shared with blocks of the cached layerKeys and runtime bases.
func (x *MemoryShareIndex) shareWithImagesLocked(ctx context.Context, backend StorageBackend, memPath string, layerKeys []string) (int64, error) {
	base, err := x.baseIndexLocked(ctx, backend)
	if err != nil {
		return 0, err
	}
	var layerPaths []string
	for _, key := range layerKeys {
		if path, ok, err := cachedPath(backend, key); err == nil && ok {
			layerPaths = append(layerPaths, path)
		}
	}
	layers, err := indexImageFiles(ctx, layerPaths)
	if err != nil {
		return 0, err
	}
	// The app layer first: its blocks are the deployment's own files, and a
	// block present in both is equally valid from either.
	return shareMemoryPages(ctx, memPath, layers, base)
}

// ErrBlockSharingUnsupported reports a filesystem that cannot share blocks
// (ext4, tmpfs); ADR-956 sharing is then a no-op.
var ErrBlockSharingUnsupported = errDedupeUnsupported

// ShareMemoryWithImages shares memPath's pages with blocks of the given image
// files directly, without cache resolution or the processed-once marker.
// Metal tests use it to restore from a memory file that already shares.
func ShareMemoryWithImages(ctx context.Context, memPath string, imagePaths []string) (int64, error) {
	idx, err := indexImageFiles(ctx, imagePaths)
	if err != nil {
		return 0, err
	}
	return shareMemoryPages(ctx, memPath, idx)
}

// baseIndexLocked returns the index of every runtime base cached on this
// node, rebuilding it when the set of cached base files changed.
func (x *MemoryShareIndex) baseIndexLocked(ctx context.Context, backend StorageBackend) (*blockIndex, error) {
	if x.base != nil && time.Since(x.checked) < baseIndexMaxAge {
		return x.base, nil
	}
	paths := cachedBaseImages(ctx, backend)
	ids := make([]string, 0, len(paths))
	for _, path := range paths {
		if id, err := fileIdentity(path); err == nil {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	files := strings.Join(ids, ",")
	if x.base != nil && files == x.baseFiles {
		x.checked = time.Now()
		return x.base, nil
	}
	base, err := indexImageFiles(ctx, paths)
	if err != nil {
		return nil, err
	}
	x.base, x.baseFiles, x.checked = base, files, time.Now()
	return base, nil
}

// cachedBaseImages lists the runtime base filesystems cached on this node.
// The listing comes from the parent; only bases already local are indexed,
// because a base that is not cached cannot back a local restore either.
func cachedBaseImages(ctx context.Context, backend StorageBackend) []string {
	lister, ok := backend.(LocalArtifactLister)
	if !ok {
		return nil
	}
	keys, err := lister.List(ctx, "base/")
	if err != nil {
		return nil
	}
	var paths []string
	for _, key := range keys {
		if !strings.HasSuffix(key, ".ext4") {
			continue
		}
		if path, ok, err := cachedPath(backend, key); err == nil && ok {
			paths = append(paths, path)
		}
	}
	return paths
}

func identityString(path string, dev, ino uint64) string {
	return path + ":" + strconv.FormatUint(dev, 10) + ":" + strconv.FormatUint(ino, 10)
}
