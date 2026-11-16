package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Native source review excludes installed dependencies. Execution separately
// pins the owner's installed tree so an agent cannot replace a local driver or
// loader after authorization while retaining the same source fingerprint.
func mcpTaskExecutionDependencies(plan string) (string, error) {
	p, err := readMCPNativePlan(plan)
	if err != nil {
		return "", err
	}
	root := filepath.Join(p.WorkerPath, "node_modules")
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() {
		return "", errors.New("task execution requires an owner-installed local Node dependency tree")
	}
	h := sha256.New()
	var bytes int64
	files := 0
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		files++
		bytes += info.Size()
		if files > mcpTaskReviewSourceMaxFiles || bytes > mcpTaskReviewSourceMaxBytes {
			return errors.New("task dependency tree exceeds review bounds")
		}
		rel, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		_, _ = fmt.Fprintf(h, "%d:%s:%d:%d:", len(rel), rel, info.Mode(), info.Size())
		if entry.Type()&os.ModeSymlink != 0 {
			target, e := filepath.EvalSymlinks(path)
			if e != nil {
				return e
			}
			relTarget, e := filepath.Rel(root, target)
			if e != nil || relTarget == ".." || strings.HasPrefix(relTarget, ".."+string(os.PathSeparator)) {
				return errors.New("task dependencies must not reference external files")
			}
			_, _ = fmt.Fprintf(h, "link:%d:%s", len(relTarget), relTarget)
			return nil
		}
		if !info.Mode().IsRegular() {
			return errors.New("task dependencies require regular files")
		}
		f, e := openCustomerFile(path)
		if e != nil {
			return e
		}
		_, e = io.Copy(h, f)
		_ = f.Close()
		return e
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
