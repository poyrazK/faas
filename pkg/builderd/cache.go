package builderd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// CacheEntry points to a cached OCI tarball and its size. The cache can be
// wiped without data loss; deployment state remains in SQL (ADR-005).
type CacheEntry struct {
	Path  string
	Bytes int64
}

// Cache stores produced OCI tarballs. Deployment builds use a versioned
// BuildCacheRecipe key; Lookup/Store retain the low-level legacy key interface.
// The historical artifact filename remains layer.ext4 for disk compatibility:
//
// <CacheDir>/<key>.<framework>.<plan>/layer.ext4
//
// Framework and plan remain visible partitions. Corrupt or missing entries
// produce cache misses; GC sweeps both old and versioned entries.
type Cache struct {
	root string
}

// cacheArtifactMode is the shared publication mode for cache artifacts.
// builderd owns the files, while imaged runs as a sibling daemon in the faas
// group and must be able to read a freshly-published layer and sidecar.
// CreateTemp defaults to 0600, which made a split-box cache hit fail with
// EACCES even though the cache directory itself was correctly shared.
const cacheArtifactMode os.FileMode = 0o640

// NewCache wires a Cache rooted at dir. The dir is created lazily.
func NewCache(dir string) *Cache { return &Cache{root: dir} }

// Lookup returns the cached layer for (sourceHash, fw, plan) if one exists
// and looks intact. (false, nil) is a cache miss, not an error.
//
// B1.3 (issue #195): a cache hit requires BOTH the layer file AND a
// matching sidecar. The sidecar is a sibling file
// `<root>/<sha256>.<fw>.<plan>/layer.sha256` whose contents are the
// sourceHash the layer was built from. A missing sidecar, an empty
// sidecar, or a mismatched sidecar all return a cache miss — the next
// Store re-creates the sidecar (idempotent), so legacy caches written
// before B1.3 self-heal on first Store of the same key.
//
// artifact.sha256 additionally records the output SHA-256. A missing digest
// (including pre-upgrade entries) or a byte mismatch is a cache miss.
func (c *Cache) Lookup(sourceHash string, fw Framework, plan api.Plan) (CacheEntry, bool) {
	return c.lookupKey(sourceHash, fw, plan)
}

// LookupWithBase looks up an archive-root build in the versioned recipe
// namespace. Call LookupBuild when selecting a workspace member.
func (c *Cache) LookupWithBase(sourceHash string, fw Framework, plan api.Plan, runtimeBaseRef string) (CacheEntry, bool) {
	return c.LookupBuild(BuildCacheRecipe{SourceSHA256: sourceHash, Framework: fw, Plan: plan, RuntimeBaseRef: runtimeBaseRef})
}

func (c *Cache) lookupKey(sourceHash string, fw Framework, plan api.Plan) (CacheEntry, bool) {
	if c == nil || c.root == "" {
		return CacheEntry{}, false
	}
	p := c.entryPath(sourceHash, fw, plan)
	st, err := os.Stat(p)
	if err != nil {
		return CacheEntry{}, false
	}
	if !st.Mode().IsRegular() || st.Size() == 0 {
		return CacheEntry{}, false
	}
	// Sidecar check: must exist, must be regular, must contain the
	// sourceHash. Whitespace-tolerant because Store writes
	// "<sourceHash>\n" and operators may inspect the file with `cat`.
	cs := c.checksumPath(sourceHash, fw, plan)
	sc, err := os.Stat(cs)
	if err != nil {
		return CacheEntry{}, false
	}
	if !sc.Mode().IsRegular() || sc.Size() == 0 {
		return CacheEntry{}, false
	}
	content, err := os.ReadFile(cs)
	if err != nil {
		return CacheEntry{}, false
	}
	if strings.TrimSpace(string(content)) != sourceHash {
		return CacheEntry{}, false
	}
	// The source sidecar identifies the key; the output digest authenticates
	// the actual artifact. Legacy entries without it are safe cache misses.
	digest, err := os.ReadFile(filepath.Join(filepath.Dir(p), "artifact.sha256"))
	if err != nil {
		return CacheEntry{}, false
	}
	got, err := hashFile(p)
	if err != nil || got != strings.TrimSpace(string(digest)) {
		return CacheEntry{}, false
	}
	return CacheEntry{Path: p, Bytes: st.Size()}, true
}

// checksumPath returns the canonical sidecar path for (sourceHash, fw, plan).
// The sidecar is a SIBLING of the layer.ext4 file inside the layer's
// own directory:
//
//	<root>/<sha256>.<fw>.<plan>/layer.ext4   ← the layer
//	<root>/<sha256>.<fw>.<plan>/layer.sha256 ← the sidecar
//
// The two filenames are siblings inside the same directory, so neither
// can collide with the other and a future "manifest.json" or "meta.json"
// added to the same dir won't rename-conflict.
func (c *Cache) checksumPath(sourceHash string, fw Framework, plan api.Plan) string {
	return filepath.Join(c.root, sourceHash+"."+string(fw)+"."+string(plan), "layer.sha256")
}

// Store moves the produced layer into the cache under the (sourceHash, fw,
// plan) key. The publish is atomic: write to a unique temp file in the
// destination directory, fsync, close, then os.Rename onto the canonical
// name. A crash mid-write leaves the temp file behind; the canonical name
// is never observable in a half-written state.
//
// CRITICAL invariants:
//
//  1. The source layerPath is NEVER renamed — pkg/builderd/builderd.go:432
//     uses out.OCIImage (the original path) immediately after Store
//     returns. Renaming the source would silently break the subsequent
//     SetDeploymentRootfs call. Always copy-then-rename-to-dst.
//
//  2. Concurrent Store calls for DIFFERENT (sourceHash, fw, plan) keys MUST NOT
//     share a temp path. Two writers with a literal dst.tmp would race —
//     one writer's os.Rename would publish the other's data. os.CreateTemp
//     with a "cache-*.tmp" wildcard gives each call a unique suffix
//     (mirrors pkg/storage/local.go::Put's atomic-publish idiom).
//
//  3. Cross-device writes fail loud — the old copyFile fallback was the
//     bug that allowed partial writes to be observable on EXDEV. A
//     cross-filesystem cache root is a config error; refuse it.
//
//  4. First-writer wins: if the canonical entry already exists, return
//     nil without rewriting. Content-addressed storage means later
//     writers should produce identical bytes; the existing copy is fine.
func (c *Cache) Store(sourceHash string, fw Framework, plan api.Plan, layerPath string, bytes int64) error {
	return c.storeKey(sourceHash, fw, plan, layerPath, bytes)
}

// StoreWithBase publishes an archive-root build in the versioned recipe
// namespace. Call StoreBuild when selecting a workspace member.
func (c *Cache) StoreWithBase(sourceHash string, fw Framework, plan api.Plan, runtimeBaseRef, layerPath string, bytes int64) error {
	return c.StoreBuild(BuildCacheRecipe{SourceSHA256: sourceHash, Framework: fw, Plan: plan, RuntimeBaseRef: runtimeBaseRef}, layerPath, bytes)
}

func (c *Cache) storeKey(sourceHash string, fw Framework, plan api.Plan, layerPath string, bytes int64) error {
	if c == nil || c.root == "" {
		return errors.New("cache: not configured")
	}
	dst := c.entryPath(sourceHash, fw, plan)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("cache: mkdir: %w", err)
	}
	// Keep only an intact entry. A corrupt or legacy entry is rebuilt from
	// the newly produced artifact, including all its integrity metadata.
	if _, ok := c.lookupKey(sourceHash, fw, plan); ok {
		for _, path := range []string{dst, c.checksumPath(sourceHash, fw, plan), filepath.Join(filepath.Dir(dst), "artifact.sha256")} {
			if err := os.Chmod(path, cacheArtifactMode); err != nil {
				return err
			}
		}
		return nil
	}
	// Step 1: open source for reading (never write to it).
	//
	//nolint:forbidigo // layerPath is the builderd-produced OCI image from
	// pkg/builderd/builderd.go::processClaimedBuild; the path is built by
	// builderd itself under its vetted cache/spool dir, never reaches a
	// customer-supplied path. The shape validator (validateTarballShape in
	// cmd/apid/deploy_inputs.go) ran on the source tarball before
	// builderd saw it.
	in, err := os.Open(layerPath)
	if err != nil {
		return fmt.Errorf("cache: store %s: open source: %w", dst, err)
	}
	// Step 2: create a UNIQUE temp file on the destination filesystem.
	// os.CreateTemp gives a random suffix and atomic create semantics
	// (O_EXCL). Two concurrent Store calls for distinct keys cannot
	// collide on this temp.
	tmp, err := os.CreateTemp(filepath.Dir(dst), "cache-*.tmp")
	if err != nil {
		_ = in.Close()
		return fmt.Errorf("cache: store %s: open tmp: %w", dst, err)
	}
	tmpPath := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()
	// Step 3: copy source bytes into temp.
	digest := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, digest), in); err != nil {
		_ = in.Close()
		return fmt.Errorf("cache: store %s: copy: %w", dst, err)
	}
	if err := in.Close(); err != nil {
		return fmt.Errorf("cache: store %s: close source: %w", dst, err)
	}
	// Step 4: fsync so the rename publishes durable bytes.
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("cache: store %s: fsync tmp: %w", dst, err)
	}
	if err := os.Chmod(tmpPath, cacheArtifactMode); err != nil {
		return fmt.Errorf("cache: store %s: chmod tmp: %w", dst, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("cache: store %s: close tmp: %w", dst, err)
	}
	closed = true
	// Step 5: atomic rename. The temp file is in filepath.Dir(dst), so
	// the rename stays on the same filesystem (atomic).
	if err := os.Rename(tmpPath, dst); err != nil {
		// EXDEV: temp file is on a different filesystem than dst.
		// Old code silently fell back to copyFile — the bug B1.2
		// closes. New code refuses: a cross-device cache root is a
		// configuration error that the operator must fix (the cache
		// must be on the same filesystem as /srv/fc).
		_ = os.Remove(tmpPath)
		if errors.Is(err, syscall.EXDEV) {
			return fmt.Errorf("cache: store %s: cross-device rename (cache_dir must be on the same filesystem as /srv/fc — %w)", dst, err)
		}
		return fmt.Errorf("cache: store %s: rename tmp: %w", dst, err)
	}
	// Layer published. Now write the sidecar (B1.3). Publish order
	// is layer-first-then-sidecar: a concurrent Lookup arriving
	// after the layer rename but before the sidecar rename sees a
	// layer-without-sidecar and returns a cache miss — the
	// conservative choice. Every successful Lookup after both
	// publishes have landed sees both files.
	if err := writeCacheSidecar(filepath.Join(filepath.Dir(dst), "artifact.sha256"), hex.EncodeToString(digest.Sum(nil))); err != nil {
		return err
	}
	return c.writeSidecar(sourceHash, fw, plan)
}

// writeSidecar atomically publishes the sidecar file at
// checksumPath(sourceHash, fw, plan) with content "<sourceHash>\n". Uses
// the same temp+rename idiom as the layer publish: unique temp,
// copy, sync, close, rename. Idempotent on existing sidecar.
func (c *Cache) writeSidecar(sourceHash string, fw Framework, plan api.Plan) error {
	if c == nil || c.root == "" {
		return errors.New("cache: not configured")
	}
	return writeCacheSidecar(c.checksumPath(sourceHash, fw, plan), sourceHash)
}

func writeCacheSidecar(cs, value string) error {
	if content, err := os.ReadFile(cs); err == nil && strings.TrimSpace(string(content)) == value {
		return os.Chmod(cs, cacheArtifactMode)
	}

	if err := os.MkdirAll(filepath.Dir(cs), 0o755); err != nil {
		return fmt.Errorf("cache: sidecar mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(cs), "sidecar-*.tmp")
	if err != nil {
		return fmt.Errorf("cache: sidecar open tmp: %w", err)
	}
	tmpPath := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.WriteString(value + "\n"); err != nil {
		return fmt.Errorf("cache: sidecar write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("cache: sidecar fsync: %w", err)
	}
	if err := os.Chmod(tmpPath, cacheArtifactMode); err != nil {
		return fmt.Errorf("cache: sidecar chmod tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("cache: sidecar close: %w", err)
	}
	closed = true
	if err := os.Rename(tmpPath, cs); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("cache: sidecar rename: %w", err)
	}
	return nil
}

func (c *Cache) entryPath(sourceHash string, fw Framework, plan api.Plan) string {
	return filepath.Join(c.root, sourceHash+"."+string(fw)+"."+string(plan), "layer.ext4")
}

// CacheGCSweepLoop is the free-function goroutine that calls
// Cache.Sweep on a fixed cadence (issue #196 B2.1). Runs alongside
// workerLoop and ReaperLoop in cmd/builderd/main.go.
//
// Why this is its own loop (not folded into the reaper): the reaper
// hits Postgres on every tick (cheap-ish but DB-bound); the cache GC
// hits the filesystem (no DB). Pairing them would force an operator
// who wants to tune one cadence to also tune the other.
//
// Cadence defaults to 24h in cmd/builderd/config.go (configurable
// via cache_gc_sweep_interval). MaxBytes/MaxAge/Now come from the
// caller so tests can inject small values via the runDeps seam.
func CacheGCSweepLoop(ctx context.Context, c *Cache, interval time.Duration, maxBytes int64, maxAge time.Duration, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		n, err := c.Sweep(maxBytes, maxAge, time.Now(), log)
		if err != nil {
			log.Warn("cache: gc sweep", "err", err)
			continue
		}
		_ = n // swept count is logged inside Sweep when > 0
	}
}

// Sweep deletes cache entries that are older than maxAge OR pushes the
// total cache size below maxBytes by deleting oldest-first.
//
// Two policies, applied in order:
//
//  1. TTL eviction: every entry whose ModTime is before now.Add(-maxAge)
//     is removed. Stable-sorted by (ModTime, path) so the same inputs
//     produce the same deletion order on every tick.
//  2. Size cap: if dirSize still exceeds maxBytes after TTL eviction,
//     delete oldest-first by (ModTime, path) until under cap.
//
// Errors are best-effort: per-entry os.RemoveAll failures are logged
// via the cache's slog logger and the sweep continues. Returns the
// total count of entries swept (TTL + size-cap combined). A missing
// or empty cache root returns (0, nil) without error — the GC is a
// no-op when there's nothing to do.
//
// Concurrency: a concurrent Cache.Store for a NEW (sourceHash, fw)
// key racing with Sweep may see its destination directory deleted
// mid-rename. Store returns an error and the next build retries —
// acceptable, the next tick will re-create the entry.
//
// This is B2.1 (issue #196). The defaults in cmd/builderd/config.go
// are 30-day TTL + 50 GB size cap; both configurable via TOML.
func (c *Cache) Sweep(maxBytes int64, maxAge time.Duration, now time.Time, log *slog.Logger) (int, error) {
	if c == nil || c.root == "" {
		return 0, nil
	}
	if log == nil {
		log = slog.Default()
	}

	// Stat the root; a missing or non-directory cache root is a no-op
	// (the operator hasn't created the cache yet, or wiped it).
	info, err := os.Stat(c.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("cache: sweep: stat %s: %w", c.root, err)
	}
	if !info.IsDir() {
		return 0, fmt.Errorf("cache: sweep: %s is not a directory", c.root)
	}

	cutoff := now.Add(-maxAge)

	// Collect every cache entry with its mod time + size. The size is
	// the sum of all files in the entry directory (layer.ext4 +
	// layer.sha256 sidecar from B1.3).
	entries, err := c.collectEntries(cutoff)
	if err != nil {
		return 0, fmt.Errorf("cache: sweep: collect: %w", err)
	}

	swept := 0

	// Policy 1: TTL. Everything older than cutoff. Sort oldest-first
	// so the deletion order is stable across ticks (helps debugging
	// log output).
	ttlVictims := make([]cacheEntry, 0)
	fresh := make([]cacheEntry, 0)
	for _, e := range entries {
		if e.modTime.Before(cutoff) {
			ttlVictims = append(ttlVictims, e)
		} else {
			fresh = append(fresh, e)
		}
	}
	sort.SliceStable(ttlVictims, func(i, j int) bool {
		if ttlVictims[i].modTime.Equal(ttlVictims[j].modTime) {
			return ttlVictims[i].path < ttlVictims[j].path
		}
		return ttlVictims[i].modTime.Before(ttlVictims[j].modTime)
	})
	for _, e := range ttlVictims {
		if err := os.RemoveAll(e.path); err != nil {
			log.Warn("cache: sweep: ttl remove", "path", e.path, "err", err)
			continue
		}
		swept++
	}

	// Policy 2: size cap. Only if maxBytes > 0 (zero means "no cap").
	// Recompute total after TTL — the deleted entries no longer count.
	if maxBytes > 0 {
		var total int64
		for _, e := range fresh {
			total += e.size
		}
		if total > maxBytes {
			// Sort fresh oldest-first; delete until under cap.
			sort.SliceStable(fresh, func(i, j int) bool {
				if fresh[i].modTime.Equal(fresh[j].modTime) {
					return fresh[i].path < fresh[j].path
				}
				return fresh[i].modTime.Before(fresh[j].modTime)
			})
			for _, e := range fresh {
				if total <= maxBytes {
					break
				}
				if err := os.RemoveAll(e.path); err != nil {
					log.Warn("cache: sweep: size-cap remove", "path", e.path, "err", err)
					continue
				}
				total -= e.size
				swept++
			}
		}
	}

	if swept > 0 {
		log.Info("cache: swept", "count", swept, "max_age", maxAge, "max_bytes", maxBytes)
	}
	return swept, nil
}

// cacheEntry is one <sha256>.<fw>/ directory with its mod time + size.
// path is the entry directory (not the layer.ext4 inside it); size is
// the recursive sum of all files in that directory.
type cacheEntry struct {
	path    string
	modTime time.Time
	size    int64
}

// collectEntries walks c.root one level deep (each child is an entry
// directory named <sha256>.<fw>). The mod time is the entry dir's
// own ModTime — fresh writes touch the dir, so the dir mtime reflects
// the most recent Store for that key. Size is recursive (layer + sidecar).
func (c *Cache) collectEntries(cutoff time.Time) ([]cacheEntry, error) {
	dirs, err := os.ReadDir(c.root)
	if err != nil {
		return nil, fmt.Errorf("readdir %s: %w", c.root, err)
	}
	var out []cacheEntry
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		full := filepath.Join(c.root, d.Name())
		info, err := d.Info()
		if err != nil {
			// Skip unreadable entries — the sweep will retry next tick.
			continue
		}
		size, err := dirSize(full)
		if err != nil {
			// Per-entry dirSize failures are non-fatal; we still
			// record the entry with size=0 so it can be evicted.
			size = 0
		}
		out = append(out, cacheEntry{
			path:    full,
			modTime: info.ModTime(),
			size:    size,
		})
	}
	return out, nil
}

// dirSize returns the total byte size of all files under root,
// recursively. filepath.WalkDir is the precedent for recursive size
// accumulation (pkg/rootfs/size.go:30, pkg/storage/local.go:245).
//
// Per-file access errors are intentionally swallowed: the sweep is
// best-effort and a single permission glitch on one file shouldn't
// fail the whole sweep (the next tick will retry). Top-level walk
// errors (e.g. the root dir is gone) propagate.
func dirSize(root string) (int64, error) {
	var total int64
	walkErr := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			//nolint:nilerr // best-effort: skip unreadable files; next sweep tick retries
			return nil
		}
		_ = info
		total += info.Size()
		return nil
	})
	return total, walkErr
}

// hashFile streams the file at path through sha256 and returns the hex digest.
// The whole file is read; builderd source tarballs are bounded by the plan's
// SourceTarballMaxMB (100 MB Hobby, 250 MB Pro+) so this is safe in memory.
//
//nolint:forbidigo // path is a vetted-id cache file under c.root joined from sourceHash + framework — no customer input reaches the open. Symlink-attack impossible because c.root is apid-owned and populated only by builderd.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("hash: open: %w", err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash: read: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
