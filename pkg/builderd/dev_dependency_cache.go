package builderd

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sourcecontext"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	devDependencyCacheDir            = "dev-dependency-cache"
	devDependencyCacheTTL            = 48 * time.Hour
	dependencyCacheMaxBytes    int64 = api.MaxExportedLayerBytes
	devDependencyCacheMaxBytes       = 16 << 30
)

// dependencyCacheKeyForApp enables the reusable dependency path for production
// and developer-session builds. The outer key isolates accounts, applications,
// selected monorepo members, frameworks, and runtime bases. BuildKit still
// validates every imported layer against its exact inputs, including lockfile
// bytes and Dockerfile instructions, so changed build inputs become selective
// cache misses. Pull-request previews remain cold for now because their
// short-lived app rows would consume the node-local cache budget without
// improving the normal deploy loop.
func dependencyCacheKeyForApp(app state.App, framework Framework, sourceRoot, runtimeBaseRef string) string {
	if app.ID == "" || app.AccountID == "" || app.PreviewPrNumber != 0 {
		return ""
	}
	effectiveRoot, err := sourcecontext.EffectiveRoot(sourceRoot)
	if err != nil {
		return ""
	}
	h := sha256.New()
	for _, value := range []string{app.AccountID, app.ID, effectiveRoot, string(framework), runtimeBaseRef} {
		_, _ = io.WriteString(h, value)
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func dependencyCachePath(driveDir, key string) (string, error) {
	if !validDependencyCacheKey(key) {
		return "", fmt.Errorf("dependency cache key must be %d lowercase hexadecimal characters", sha256.Size*2)
	}
	return filepath.Join(driveDir, devDependencyCacheDir, key), nil
}

func validDependencyCacheKey(key string) bool {
	if len(key) != sha256.Size*2 {
		return false
	}
	for _, ch := range key {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

// copyDependencyCache copies a BuildKit local cache without following links
// and with an aggregate byte ceiling. A cache is disposable: malformed or
// oversized input is a cold-build signal, never a reason to weaken path safety.
func copyDependencyCache(src, dst string, maxBytes int64) error {
	return walkDependencyCache(src, maxBytes, func(path, rel string, entry fs.DirEntry) error {
		if rel == "." {
			return os.MkdirAll(dst, 0o700)
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		// path is rooted in the platform-owned cache and non-regular entries
		// were rejected by walkDependencyCache immediately before this call.
		in, err := os.Open(path) //nolint:forbidigo,gosec
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // target is below a fresh platform-owned staging directory.
		if err != nil {
			_ = in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeOutErr := out.Close()
		closeInErr := in.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeOutErr != nil {
			return closeOutErr
		}
		return closeInErr
	})
}

// stageDependencyCacheForDrive snapshots an immutable dependency-cache tree
// into a per-build staging directory without copying its payload when both
// paths share a filesystem. The staging directory owns independent directory
// entries, while hard-linked regular files keep their inodes alive if cache
// publication or GC rotates the source after this function returns.
//
// Filesystems that do not support hard links retain the bounded-copy path.
// Cache validation runs in both paths, so a malformed tree remains a cold-build
// signal rather than weakening the staging boundary.
func stageDependencyCacheForDrive(src, dst string, maxBytes int64, link func(string, string) error) error {
	if link == nil {
		link = os.Link
	}
	linkErr := walkDependencyCache(src, maxBytes, func(path, rel string, entry fs.DirEntry) error {
		if rel == "." {
			return os.MkdirAll(dst, 0o700)
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		return link(path, target)
	})
	if linkErr == nil {
		return nil
	}
	if err := os.RemoveAll(dst); err != nil {
		return errors.Join(fmt.Errorf("dependency cache link: %w", linkErr), fmt.Errorf("dependency cache fallback cleanup: %w", err))
	}
	if err := copyDependencyCache(src, dst, maxBytes); err != nil {
		return errors.Join(fmt.Errorf("dependency cache link: %w", linkErr), fmt.Errorf("dependency cache copy fallback: %w", err))
	}
	return nil
}

// walkDependencyCache validates a BuildKit local cache and optionally visits
// each entry after it has passed the path-shape and aggregate-size checks.
// Keeping validation in the same walk as copying preserves the cold fallback's
// I/O cost while allowing same-filesystem publication to validate without
// copying the complete cache tree a second time.
func walkDependencyCache(src string, maxBytes int64, visit func(string, string, fs.DirEntry) error) error {
	index, err := os.Lstat(filepath.Join(src, "index.json"))
	if err != nil {
		return err
	}
	if !index.Mode().IsRegular() {
		return errors.New("dependency cache index is not a regular file")
	}
	var copied int64
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			if visit != nil {
				return visit(path, rel, entry)
			}
			return nil
		}
		if entry.IsDir() {
			if visit != nil {
				return visit(path, rel, entry)
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("dependency cache contains unsupported entry %q", rel)
		}
		if maxBytes > 0 && info.Size() > maxBytes-copied {
			return fmt.Errorf("dependency cache exceeds %d-byte ceiling", maxBytes)
		}
		copied += info.Size()
		if visit != nil {
			return visit(path, rel, entry)
		}
		return nil
	})
}

// publishDependencyCache replaces one tenant-scoped cache atomically. The
// caller serializes staging/publication, so removing the old generation cannot
// race a drive being prepared from it. Production keeps the vmmd export and
// dependency-cache roots below /srv/fc/builder, so a validated rename avoids
// copying the complete cache tree twice. Cross-filesystem development layouts
// retain the existing bounded copy path.
func publishDependencyCache(src, dst string, maxBytes int64) error {
	parent := filepath.Dir(dst)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("dependency cache mkdir: %w", err)
	}
	tmp, err := os.MkdirTemp(parent, ".publish-*")
	if err != nil {
		return fmt.Errorf("dependency cache temp: %w", err)
	}
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.RemoveAll(tmp)
		}
	}()
	staged := filepath.Join(tmp, "cache")
	if err := stageDependencyCache(src, staged, maxBytes, os.Rename); err != nil {
		return err
	}
	backup := dst + ".previous"
	_ = os.RemoveAll(backup)
	if err := os.Rename(dst, backup); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("dependency cache rotate: %w", err)
	}
	if err := os.Rename(staged, dst); err != nil {
		_ = os.Rename(backup, dst)
		return fmt.Errorf("dependency cache publish: %w", err)
	}
	now := time.Now()
	_ = os.Chtimes(dst, now, now)
	cleanupTmp = false
	_ = os.RemoveAll(tmp)
	_ = os.RemoveAll(backup)
	return nil
}

func stageDependencyCache(src, staged string, maxBytes int64, rename func(string, string) error) error {
	if err := walkDependencyCache(src, maxBytes, nil); err != nil {
		return fmt.Errorf("dependency cache validate: %w", err)
	}
	if err := rename(src, staged); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return fmt.Errorf("dependency cache move: %w", err)
	}
	if err := copyDependencyCache(src, staged, maxBytes); err != nil {
		return fmt.Errorf("dependency cache copy: %w", err)
	}
	return nil
}

func sweepDependencyCaches(driveDir string, now time.Time) error {
	return sweepDependencyCachesWithLimit(driveDir, now, devDependencyCacheMaxBytes)
}

func sweepDependencyCachesWithLimit(driveDir string, now time.Time, maxBytes int64) error {
	root := filepath.Join(driveDir, devDependencyCacheDir)
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	type candidate struct {
		path    string
		modTime time.Time
		size    int64
	}
	var fresh []candidate
	var total int64
	for _, entry := range entries {
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		name := entry.Name()
		generated := strings.HasPrefix(name, ".publish-") || validDependencyCacheKey(name)
		if strings.HasSuffix(name, ".previous") {
			generated = validDependencyCacheKey(strings.TrimSuffix(name, ".previous"))
		}
		if !entry.IsDir() || !generated {
			continue
		}
		path := filepath.Join(root, name)
		if now.Sub(info.ModTime()) > devDependencyCacheTTL {
			_ = os.RemoveAll(path)
			continue
		}
		size, sizeErr := dependencyCacheSize(path)
		if sizeErr != nil {
			continue
		}
		fresh = append(fresh, candidate{path: path, modTime: info.ModTime(), size: size})
		total += size
	}
	if maxBytes <= 0 || total <= maxBytes {
		return nil
	}
	sort.SliceStable(fresh, func(i, j int) bool {
		if fresh[i].modTime.Equal(fresh[j].modTime) {
			return fresh[i].path < fresh[j].path
		}
		return fresh[i].modTime.Before(fresh[j].modTime)
	})
	for _, entry := range fresh {
		if total <= maxBytes {
			break
		}
		if err := os.RemoveAll(entry.path); err == nil {
			total -= entry.size
		}
	}
	return nil
}

func dependencyCacheSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}
