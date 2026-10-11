package imaged

// Runtime base convergence — ADR-567; cached bases ADR-632.
//
// A runtime base is published under one logical key (base/runner-*.ext4) and
// every compute node attaches its cached copy as drive0. ADR-510 refuses to
// restore a snapshot onto bytes other than the ones it was captured with, so
// a fleet only reuses snapshots across nodes when every node holds the
// published bytes. ext4 builds are not byte-deterministic, and each node's
// imaged reads the publication sidecars through its own cache, so after a
// release each node rebuilt and republished its own copy; on production-us
// the two compute nodes then held different node22 and python312 bases and
// every cross-node wake cold-booted instead of restoring.
//
// The publisher therefore records the SHA-256 of the exact bytes it published
// next to the base (<key>.content), and every node periodically re-reads the
// publication from the canonical parent and replaces its cached copy when the
// bytes differ. A node only adopts a publication built from the same source
// ref with the same guest-init it would build itself, so a rolling rollout
// never hands an old daemon a newer PID 1.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
)

// BaseConvergenceInterval is how often imaged re-checks every staged base
// against its shared publication. Each check reads two small sidecars from
// the parent; the base itself is only downloaded when the bytes differ.
const BaseConvergenceInterval = time.Minute

// baseContentSuffix names the content-identity sidecar of a published base.
const baseContentSuffix = ".content"

// baseContentVersion versions the sidecar document.
const baseContentVersion = 1

// baseContent is the identity of the bytes published under a base key.
type baseContent struct {
	Version int    `json:"version"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}

func (c baseContent) valid() bool {
	return c.Version == baseContentVersion && len(c.SHA256) == len("sha256:")+64 && c.Size > 0
}

type stagedBase struct {
	ref       string
	digestKey string
}

type contentFileKey struct {
	dev, ino uint64
	size     int64
}

type baseConvergenceState struct {
	mu sync.Mutex
	// staged maps a base key to the publication this daemon staged for it.
	staged map[string]stagedBase
	// digests memoizes local file digests per immutable file version; cache
	// refreshes always rename a new file, so a replacement changes the inode.
	digests map[contentFileKey]string
	// declined remembers a publication whose bytes still differed from its
	// sidecar after a refresh (a publisher mid-write), so the loop does not
	// download the same base every tick until the sidecar changes.
	declined map[string]string
}

func baseContentKey(baseKey string) string { return baseKey + baseContentSuffix }

// rememberStagedBase records a base this daemon staged (built or reused) so
// the convergence loop keeps it aligned with its publication.
func (h *Handler) rememberStagedBase(ref, baseKey, digestKey string) {
	s := &h.baseConvergence
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.staged == nil {
		s.staged = make(map[string]stagedBase)
	}
	s.staged[baseKey] = stagedBase{ref: ref, digestKey: digestKey}
}

// writeBaseContentSidecar records the identity of the bytes now published
// under baseKey. It hashes the cache's local copy of the publication when one
// exists, else streams the published object.
func (h *Handler) writeBaseContentSidecar(ctx context.Context, be storage.StorageBackend, baseKey string) error {
	content, err := h.publishedBytesIdentity(ctx, be, baseKey)
	if err != nil {
		return err
	}
	return putBaseContent(ctx, be, baseKey, content)
}

func putBaseContent(ctx context.Context, be storage.StorageBackend, baseKey string, content baseContent) error {
	body, err := json.Marshal(content)
	if err != nil {
		return err
	}
	if err := be.Put(ctx, baseContentKey(baseKey), bytes.NewReader(body)); err != nil {
		return fmt.Errorf("imaged: write base content sidecar %q: %w", baseContentKey(baseKey), err)
	}
	return nil
}

func (h *Handler) publishedBytesIdentity(ctx context.Context, be storage.StorageBackend, baseKey string) (baseContent, error) {
	if resolver, ok := be.(storage.LocalPathResolver); ok {
		if path, local, err := resolver.LocalPath(baseKey); err == nil && local {
			return h.fileContent(path)
		}
	}
	rc, err := be.Get(ctx, baseKey)
	if err != nil {
		return baseContent{}, fmt.Errorf("imaged: read published base %q: %w", baseKey, err)
	}
	defer func() { _ = rc.Close() }()
	return hashContent(rc)
}

func hashContent(r io.Reader) (baseContent, error) {
	h := sha256.New()
	n, err := io.Copy(h, r)
	if err != nil {
		return baseContent{}, fmt.Errorf("imaged: hash base: %w", err)
	}
	return baseContent{Version: baseContentVersion, SHA256: "sha256:" + hex.EncodeToString(h.Sum(nil)), Size: n}, nil
}

// fileContent hashes path once per file version.
func (h *Handler) fileContent(path string) (baseContent, error) {
	info, err := os.Stat(path)
	if err != nil {
		return baseContent{}, fmt.Errorf("imaged: stat base %s: %w", path, err)
	}
	var key contentFileKey
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		key = contentFileKey{dev: uint64(st.Dev), ino: st.Ino, size: info.Size()} //nolint:unconvert // Dev is int32 on darwin
		s := &h.baseConvergence
		s.mu.Lock()
		digest, hit := s.digests[key]
		s.mu.Unlock()
		if hit {
			return baseContent{Version: baseContentVersion, SHA256: digest, Size: info.Size()}, nil
		}
	}
	f, err := os.OpenFile(path, os.O_RDONLY, 0) // #nosec G304 -- path is this daemon's own storage-cache entry for a platform base key.
	if err != nil {
		return baseContent{}, fmt.Errorf("imaged: open base %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	content, err := hashContent(f)
	if err != nil {
		return baseContent{}, err
	}
	if key != (contentFileKey{}) {
		s := &h.baseConvergence
		s.mu.Lock()
		if s.digests == nil {
			s.digests = make(map[contentFileKey]string)
		}
		s.digests[key] = content.SHA256
		s.mu.Unlock()
	}
	return content, nil
}

// readParent reads key from the canonical parent rather than this node's
// cache: a cached sidecar describes whatever was published when this node
// last fetched it, which is exactly the staleness convergence repairs.
func readParent(ctx context.Context, be storage.StorageBackend, key string) ([]byte, error) {
	var rc io.ReadCloser
	cache, rel, err := storage.CacheBackendForKey(be, key)
	if err != nil {
		return nil, err
	}
	if cache != nil {
		rc, err = cache.Refresh(ctx, rel)
	} else {
		rc, err = be.Get(ctx, key)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(io.LimitReader(rc, 64<<10))
}

// convergeBase makes this node's cached copy of baseKey byte-identical to the
// shared publication. It reports whether it replaced the local copy.
func (h *Handler) convergeBase(ctx context.Context, be storage.StorageBackend, baseKey string, staged stagedBase, guestInitDigest string) (bool, error) {
	cache, rel, err := storage.CacheBackendForKey(be, baseKey)
	if err != nil || cache == nil {
		// An uncached route reads the canonical object directly; there is no
		// local copy to drift.
		return false, err
	}
	// Adopt only a publication of the same recipe: the same immutable source
	// ref built with this daemon's guest-init.
	sidecar, err := readParent(ctx, be, staged.digestKey)
	if err != nil {
		return false, fmt.Errorf("read base digest sidecar %q: %w", staged.digestKey, err)
	}
	if _, sourceRef, current := parseBaseDigestSidecar(string(sidecar), guestInitDigest); !current || sourceRef != staged.ref {
		return false, nil
	}
	path, local, err := cache.LocalPath(rel)
	if err != nil || !local {
		// Not cached here: the next read fetches the publication itself.
		return false, err
	}
	raw, err := readParent(ctx, be, baseContentKey(baseKey))
	var published baseContent
	switch {
	case errors.Is(err, storage.ErrNotFound) || (err == nil && (json.Unmarshal(raw, &published) != nil || !published.valid())):
		// A publication from before ADR-567 (or a torn sidecar): adopt the
		// published bytes and record their identity for every other node.
		return h.adoptPublication(ctx, be, cache, rel, baseKey, path, baseContent{})
	case err != nil:
		return false, fmt.Errorf("read base content sidecar %q: %w", baseContentKey(baseKey), err)
	}
	have, err := h.fileContent(path)
	if err != nil {
		return false, err
	}
	if have.SHA256 == published.SHA256 {
		return false, nil
	}
	s := &h.baseConvergence
	s.mu.Lock()
	declined := s.declined[baseKey] == published.SHA256
	s.mu.Unlock()
	if declined {
		return false, nil
	}
	return h.adoptPublication(ctx, be, cache, rel, baseKey, path, published)
}

// adoptPublication replaces the local copy with the published object. With
// a zero want it records the adopted bytes as the publication's identity.
func (h *Handler) adoptPublication(ctx context.Context, be storage.StorageBackend, cache *storage.LocalCacheBackend, rel, baseKey, oldPath string, want baseContent) (bool, error) {
	before, _ := h.fileContent(oldPath)
	rc, err := cache.Refresh(ctx, rel)
	if err != nil {
		return false, fmt.Errorf("refresh base %q: %w", baseKey, err)
	}
	got, hashErr := hashContent(rc)
	closeErr := rc.Close()
	if err := errors.Join(hashErr, closeErr); err != nil {
		return true, err
	}
	if want == (baseContent{}) {
		if err := putBaseContent(ctx, be, baseKey, got); err != nil {
			return true, err
		}
	} else if got.SHA256 != want.SHA256 {
		s := &h.baseConvergence
		s.mu.Lock()
		if s.declined == nil {
			s.declined = make(map[string]string)
		}
		s.declined[baseKey] = want.SHA256
		s.mu.Unlock()
		h.log.Warn("imaged: published base differs from its content sidecar; will retry when the sidecar changes",
			"key", baseKey, "sidecar", want.SHA256, "published", got.SHA256)
		return true, nil
	}
	if before.SHA256 != got.SHA256 {
		h.log.Info("imaged: runtime base converged to its shared publication",
			"key", baseKey, "previous", before.SHA256, "published", got.SHA256)
	}
	return true, nil
}

// ConvergeBases runs one convergence pass over every staged base and every
// platform base cached on this node (ADR-632).
func (h *Handler) ConvergeBases(ctx context.Context) {
	h.convergeBases(ctx, BuilderArch(), os.Getenv)
}

func (h *Handler) convergeBases(ctx context.Context, arch string, envLookup func(string) string) {
	be, err := h.storageFor()
	if err != nil {
		h.log.Warn("imaged: base convergence: storage", "err", err)
		return
	}
	guestInitDigest, err := guestInitBinaryDigest(h.guestInitPath)
	if err != nil {
		h.log.Warn("imaged: base convergence: hash guest-init", "path", h.guestInitPath, "err", err)
		return
	}
	h.rememberCachedBases(ctx, be, arch, envLookup)
	s := &h.baseConvergence
	s.mu.Lock()
	keys := make([]string, 0, len(s.staged))
	staged := make(map[string]stagedBase, len(s.staged))
	for key, base := range s.staged {
		keys = append(keys, key)
		staged[key] = base
	}
	s.mu.Unlock()
	sort.Strings(keys)
	for _, key := range keys {
		if ctx.Err() != nil {
			return
		}
		// Serialize with on-demand staging so a convergence download never
		// races this daemon's own rebuild of the same key.
		h.runtimeBaseMu.Lock()
		_, err := h.convergeBase(ctx, be, key, staged[key], guestInitDigest)
		h.runtimeBaseMu.Unlock()
		if err != nil {
			h.log.Warn("imaged: base convergence", "key", key, "err", err)
		}
	}
}

// RunBaseConvergence converges every staged base now and then every interval
// until ctx ends.
func (h *Handler) RunBaseConvergence(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = BaseConvergenceInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		h.ConvergeBases(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
