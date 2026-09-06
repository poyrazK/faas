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

// dependencyCacheKeyForApp enables the warm dependency path only for
// developer sessions. The outer key isolates accounts, developer workspaces,
// selected monorepo members, frameworks, and runtime bases. BuildKit still
// validates every imported layer against its exact inputs, including lockfile
// bytes and Dockerfile instructions, so changed build inputs become selective
// cache misses.
func dependencyCacheKeyForApp(app state.App, framework Framework, sourceRoot, runtimeBaseRef string) string {
	if app.ID == "" || app.AccountID == "" || app.PreviewOfSlug == "" || app.PreviewPrNumber != 0 {
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
			return os.MkdirAll(dst, 0o700)
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
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
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		// path is rooted in the platform-owned cache and non-regular entries
		// were rejected from the same WalkDir snapshot immediately above.
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

// publishDependencyCache replaces one tenant-scoped cache atomically. The
// caller serializes staging/publication, so removing the old generation cannot
// race a drive being prepared from it. Cross-filesystem export directories are
// supported because bytes are copied into a sibling temp directory first.
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
	if err := copyDependencyCache(src, staged, maxBytes); err != nil {
		return fmt.Errorf("dependency cache copy: %w", err)
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
