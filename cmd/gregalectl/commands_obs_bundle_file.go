package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// Publish complete bytes using a same-directory hard link, which atomically
// refuses an existing destination (including symlinks). No partially written
// bundle is visible at the requested path. CreateTemp starts owner-only.
func writeObsBundleFile(path string, body []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".gregalectl-obs-bundle-*")
	if err != nil {
		return fmt.Errorf("create bundle temporary file: %w", err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		return fmt.Errorf("write bundle: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync bundle: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close bundle: %w", err)
	}
	if err := os.Link(tmp, path); err != nil {
		return fmt.Errorf("publish bundle without overwriting destination: %w", err)
	}
	return nil
}
