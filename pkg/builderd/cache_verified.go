package builderd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/api"
)

// verifiedArtifactMinAge is how far in the past a cache artifact's
// modification time must lie before builderd trusts a digest it remembers for
// the file. A write sets the modification time to the current time, but the
// file system keeps that time at a coarse resolution (a few milliseconds on
// ext4), so a write just after builderd read the time could leave it
// unchanged. storeKey backdates the artifacts it publishes beyond this age.
const verifiedArtifactMinAge = 2 * time.Second

// verifiedArtifacts remembers the SHA-256 builderd computed for each cache
// artifact, with the file it computed it from, so a cache hit does not read
// the whole artifact again to check it against its artifact.sha256 sidecar.
// On the production compute nodes (2026-11-16) that read made up most of a
// cache hit's time before "build started": an artifact is 160 MB at the
// median and up to 370 MB, the cache sits on a boot disk that reads about
// 45 MB/s cold, and a hit hashed it twice (ClassifyBuild and LeaseBuild), so
// a hit spent 5–9 s on a 182 MB artifact and 19.6 s on a 368 MB one.
//
// It is process-local and best effort. A remembered digest is used only while
// the path still names the same regular file (device and inode) with the same
// size and modification time. The cache publishes an artifact through a new
// file and a rename, never in place, and ages entries by their directory's
// modification time, not the artifact's, so any write to the artifact or any
// replacement of it reads as a miss and is hashed again. A restart hashes
// each artifact once, on its first lookup.
type verifiedArtifacts struct {
	mu      sync.Mutex
	entries map[string]verifiedArtifact
}

type verifiedArtifact struct {
	digest   string
	file     os.FileInfo
	recorded time.Time
}

// record remembers digest for the artifact at path, computed from file. It
// remembers nothing for a file modified within verifiedArtifactMinAge of now.
func (m *verifiedArtifacts) record(path, digest string, file os.FileInfo, now time.Time) {
	if digest == "" || file == nil || !file.Mode().IsRegular() || now.Sub(file.ModTime()) < verifiedArtifactMinAge {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries == nil {
		m.entries = map[string]verifiedArtifact{}
	}
	if _, replacing := m.entries[path]; !replacing && len(m.entries) >= api.BuilderdVerifiedCacheArtifacts {
		oldest := ""
		for p, e := range m.entries {
			if oldest == "" || e.recorded.Before(m.entries[oldest].recorded) {
				oldest = p
			}
		}
		delete(m.entries, oldest)
	}
	m.entries[path] = verifiedArtifact{digest: digest, file: file, recorded: now}
}

// digest returns the digest remembered for path when file, the path's current
// Lstat, is still the file it was computed from. It forgets a stale entry.
func (m *verifiedArtifacts) digest(path string, file os.FileInfo) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[path]
	if !ok {
		return "", false
	}
	if !file.Mode().IsRegular() || !os.SameFile(e.file, file) || e.file.Size() != file.Size() || !e.file.ModTime().Equal(file.ModTime()) {
		delete(m.entries, path)
		return "", false
	}
	return e.digest, true
}

// artifactDigest returns the SHA-256 of the cache artifact at path, reading
// the file only when builderd remembers no digest for this exact file.
func (c *Cache) artifactDigest(path string) (string, bool) {
	if info, err := os.Lstat(path); err == nil {
		if digest, ok := c.verified.digest(path, info); ok {
			return digest, true
		}
	}
	hash := c.hashArtifact
	if hash == nil {
		hash = hashFileInfo
	}
	digest, info, err := hash(path)
	if err != nil {
		return "", false
	}
	c.verified.record(path, digest, info, time.Now())
	return digest, true
}

// hashFileInfo hashes the regular file at path without following a final
// symlink and returns the file's status from before the read, so a write
// during the read leaves a status that no longer matches the file.
func hashFileInfo(path string) (string, os.FileInfo, error) {
	f, err := openNoFollow(path, unix.O_RDONLY, 0)
	if err != nil {
		return "", nil, fmt.Errorf("hash: open: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return "", nil, fmt.Errorf("hash: stat: %w", err)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", nil, fmt.Errorf("hash: read: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), info, nil
}
