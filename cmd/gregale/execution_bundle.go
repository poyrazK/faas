package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// executionBundle reads a local directory into the same regular-file-only
// contract enforced by API admission and the guest. It never follows a
// customer symlink and bounds each read before allocating its content.
func executionBundle(root, entrypoint string) ([]api.ExecutionFile, error) {
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return nil, fmt.Errorf("bundle root must be a real directory")
	}

	var paths []string
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path != root && entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if path == root || entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink %q", path)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing non-regular file %q", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		if len(paths) > api.ExecutionBundleMaxFiles {
			return fmt.Errorf("bundle contains more than %d files", api.ExecutionBundleMaxFiles)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	files := make([]api.ExecutionFile, 0, len(paths))
	total := 0
	for _, rel := range paths {
		f, err := openCustomerFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", rel, err)
		}
		data, readErr := io.ReadAll(io.LimitReader(f, int64(api.ExecutionPlaintextFieldMaxBytes-total)+1))
		_ = f.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read %q: %w", rel, readErr)
		}
		if len(data) > api.ExecutionPlaintextFieldMaxBytes-total {
			return nil, fmt.Errorf("bundle exceeds %d source bytes", api.ExecutionPlaintextFieldMaxBytes)
		}
		total += len(data)
		files = append(files, api.ExecutionFile{Path: rel, Content: data})
	}
	if err := api.ValidateExecutionBundle(entrypoint, files, api.ExecutionPlaintextFieldMaxBytes); err != nil {
		return nil, err
	}
	return files, nil
}
