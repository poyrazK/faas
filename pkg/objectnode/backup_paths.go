package objectnode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

// Resolve only the existing parent. The final component must never be a symlink,
// and publication must never replace an existing file or directory.
func backupDestination(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	abs = filepath.Join(parent, filepath.Base(abs))
	if _, err = os.Lstat(abs); !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("object backup: destination must not exist: %w", os.ErrExist)
	}
	return abs, nil
}

func backupDirectory(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("object backup: directory must be real and private to its owner")
	}
	return filepath.EvalSymlinks(abs)
}

func backupPathsDisjoint(a, b string) bool {
	return a != b && !strings.HasPrefix(a, b+string(filepath.Separator)) && !strings.HasPrefix(b, a+string(filepath.Separator))
}

// A bounded-depth, batched walk rejects symlinks, devices, FIFOs and hard-link
// aliases before any recovery code can follow a catalog path. The owner must
// keep other programs from editing these private directories during an operation.
func walkBackupTree(ctx context.Context, root, relative string, depth int, visit func(string, os.FileInfo) error) error {
	if depth > api.ObjectNodeInventoryMaxDepth {
		return ErrCorrupt
	}
	return visitDirectory(filepath.Join(root, relative), func(e os.DirEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(relative, e.Name())
		info, err := e.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("object backup: unsupported file type at %s: %w", path, ErrCorrupt)
		}
		if stat, ok := info.Sys().(*syscall.Stat_t); !ok || !info.IsDir() && stat.Nlink != 1 {
			return fmt.Errorf("object backup: aliased file at %s: %w", path, ErrCorrupt)
		}
		if err = visit(filepath.ToSlash(path), info); err != nil {
			return err
		}
		if info.IsDir() {
			return walkBackupTree(ctx, root, path, depth+1, visit)
		}
		return nil
	})
}

func validBackupPath(path string, directory bool) bool {
	if !filepath.IsLocal(path) || filepath.ToSlash(filepath.Clean(path)) != path || strings.Contains(path, "\\") {
		return false
	}
	if validReplicationBackupPath(path, directory) {
		return true
	}
	p := strings.Split(path, "/")
	if len(p) == 1 {
		if !directory {
			return path == "format.json" || path == "recovery.json"
		}
		return path == "buckets" || path == "receipts" || path == "multipart" || path == "scrub" || path == "staging" || path == "trash"
	}
	switch p[0] {
	case "receipts":
		return len(p) == 2 && !directory && validReceiptID(strings.TrimSuffix(p[1], ".json")) && strings.HasSuffix(p[1], ".json")
	case "scrub":
		if len(p) == 2 {
			if directory {
				return p[1] == "findings"
			}
			return p[1] == "progress.json" || strings.HasSuffix(p[1], ".jsonl") && validReceiptID(strings.TrimSuffix(p[1], ".jsonl"))
		}
		return len(p) == 3 && !directory && p[1] == "findings" && strings.HasSuffix(p[2], ".json") && validScrubFindingID(strings.TrimSuffix(p[2], ".json"))
	case "buckets", "multipart":
		bucket := p[0] == "buckets"
		if bucket && !validScrubFindingID(p[1]) || !bucket && !validReceiptID(p[1]) {
			return false
		}
		if len(p) == 2 {
			return directory
		}
		catalog, children := "session.json", "parts"
		if bucket {
			catalog, children = "bucket.json", "objects"
		}
		if len(p) == 3 {
			return directory && p[2] == children || !directory && p[2] == catalog
		}
		if p[2] != children || len(p) > 5 {
			return false
		}
		if bucket {
			if !validScrubFindingID(p[3]) {
				return false
			}
		} else {
			n, err := strconv.ParseInt(p[3], 10, 32)
			if err != nil || n < 1 || n > api.MaxMultipartParts || strconv.FormatInt(n, 10) != p[3] {
				return false
			}
		}
		if len(p) == 4 {
			return directory
		}
		return !directory && (p[4] == "current.json" || strings.HasSuffix(p[4], ".data") && validReceiptID(strings.TrimSuffix(p[4], ".data")))
	}
	return false
}

func backupSpace(path string, bytes int64) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return err
	}
	if bytes < 0 || uint64(bytes)+uint64(api.ObjectUploadSpoolMinFreeBytes) > stat.Bavail*uint64(stat.Bsize) {
		return ErrCapacity
	}
	return nil
}

func publishBackupDirectory(ctx context.Context, stage, destination string, checkpoint func(string) error) error {
	if err := backupCheckpoint(ctx, checkpoint, "ready"); err != nil {
		return err
	}
	if err := unix.Renameat2(unix.AT_FDCWD, stage, unix.AT_FDCWD, destination, unix.RENAME_NOREPLACE); err != nil {
		return fmt.Errorf("object backup: publish directory: %w", err)
	}
	if err := syncDirectory(filepath.Dir(destination)); err != nil {
		return fmt.Errorf("object backup: published at %s but parent fsync failed; durability uncertain: %w", destination, err)
	}
	return backupCheckpoint(ctx, checkpoint, "published")
}

func backupCheckpoint(ctx context.Context, checkpoint func(string) error, phase string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if checkpoint != nil {
		return checkpoint(phase)
	}
	return nil
}

func syncBackupTree(ctx context.Context, path string) error {
	if err := visitDirectory(path, func(e os.DirEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.IsDir() {
			return syncBackupTree(ctx, filepath.Join(path, e.Name()))
		}
		return nil
	}); err != nil {
		return err
	}
	return syncDirectory(path)
}
