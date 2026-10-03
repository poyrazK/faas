package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// preloadDaemon reads the already-verified, published release executable just
// before its restart. It does not execute code or keep the file in userspace
// memory. Linux's page cache absorbs the cold disk reads while the old process
// still accepts requests; readiness and public availability remain independent
// gates because cache residency alone cannot guarantee a bounded restart.
func (r hostRuntime) preloadDaemon(ctx context.Context, service string) error {
	if r.binaryDir == "" {
		return errors.New("preload daemon: binary directory is not configured")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("preload daemon %s: %w", service, err)
	}
	path := filepath.Join(r.binaryDir, service)
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("preload daemon %s: %w", service, err)
	}
	// Reject a special file before opening it: a FIFO could block without
	// ever reaching the cancellation check. Verified bundles use regular
	// executable files, never final-component links or device nodes.
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("preload daemon %s: target is not a regular executable", service)
	}
	//nolint:forbidigo // Registry-selected daemon in the controller-verified release; Lstat above rejects links and special files.
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("preload daemon %s: %w", service, err)
	}
	defer func() { _ = file.Close() }()
	buffer := make([]byte, 64*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("preload daemon %s: %w", service, err)
		}
		n, err := file.Read(buffer)
		total += int64(n)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("preload daemon %s: %w", service, err)
		}
	}
	fmt.Fprintf(os.Stderr, "deployctl: preloaded %s (%d bytes) before restart\n", service, total)
	return nil
}
