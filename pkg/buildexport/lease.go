// Package buildexport owns the node-local handoff between builderd and imaged.
//
// A successful builder VM leaves an OCI archive at
// <export-root>/<build-id>/build/out/image.tar. PostgreSQL's
// deployments.rootfs_path is the durable ownership record until imaged stamps
// the published application layer. A shared flock on image.tar is the active
// reader lease; the builderd sweeper takes an exclusive non-blocking flock
// before removing an export directory. Together those two mechanisms make a
// lost notification or daemon restart retryable without allowing cleanup to
// race a reader that has already started publication.
package buildexport

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ErrBusy means another process currently owns the artifact. Callers should
// leave the durable handoff untouched and retry from the normal recovery loop.
var ErrBusy = errors.New("build export: artifact lease busy")

// Lease is a process-scoped shared reader lease on a builder export.
type Lease struct {
	file *os.File
}

// AcquireArtifact takes a non-blocking shared lease when path is a canonical
// builder export. Non-export paths (direct image deploys and cache leases) are
// deliberately ignored and return (nil, false, nil).
func AcquireArtifact(path string) (*Lease, bool, error) {
	if _, ok := ExportDir(path); !ok {
		return nil, false, nil
	}
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, true, fmt.Errorf("build export: open artifact: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, true, ErrBusy
		}
		return nil, true, fmt.Errorf("build export: acquire reader lease: %w", err)
	}
	return &Lease{file: f}, true, nil
}

// Close releases the reader lease. It is safe to call more than once.
func (l *Lease) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	f := l.file
	l.file = nil
	unlockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	closeErr := f.Close()
	return errors.Join(unlockErr, closeErr)
}

// ExportDir recognizes only the canonical image.tar handoff shape and returns
// its per-build directory. The strict suffix prevents cleanup code from ever
// treating an arbitrary customer path as a builder export.
func ExportDir(artifact string) (string, bool) {
	clean := filepath.Clean(artifact)
	if filepath.Base(clean) != "image.tar" || filepath.Base(filepath.Dir(clean)) != "out" ||
		filepath.Base(filepath.Dir(filepath.Dir(clean))) != "build" {
		return "", false
	}
	exportDir := filepath.Dir(filepath.Dir(filepath.Dir(clean)))
	if exportDir == "." || exportDir == string(filepath.Separator) || filepath.Base(exportDir) == "" {
		return "", false
	}
	return exportDir, true
}
