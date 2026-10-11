package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// standardDevLinks are the /dev entries container runtimes (runc, Docker)
// create and OCI images rely on. A fresh devtmpfs has none of them.
//
// production-us rc.251: nginx-unprivileged ships /var/log/nginx/error.log as a
// symlink to /dev/stderr. With no /dev/stderr in the guest, nginx's open
// tried to create it in root-owned /dev and failed with EACCES ("could not open
// error log file ... (13: Permission denied)"), so the image crash-looped even
// after the workload's stdio pipe was chowned to its user (H8-4).
var standardDevLinks = []struct{ name, target string }{
	{"fd", "/proc/self/fd"},
	{"stdin", "/proc/self/fd/0"},
	{"stdout", "/proc/self/fd/1"},
	{"stderr", "/proc/self/fd/2"},
}

// ensureStandardDevLinks creates the missing standard links under dev and
// leaves anything the kernel or image already provides in place.
func ensureStandardDevLinks(dev string) error {
	var errs []error
	for _, l := range standardDevLinks {
		path := filepath.Join(dev, l.name)
		if _, err := os.Lstat(path); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("inspect %s: %w", path, err))
			continue
		}
		if err := os.Symlink(l.target, path); err != nil && !errors.Is(err, os.ErrExist) {
			errs = append(errs, fmt.Errorf("link %s: %w", path, err))
		}
	}
	return errors.Join(errs...)
}
