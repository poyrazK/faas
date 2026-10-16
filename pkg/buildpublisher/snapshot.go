package buildpublisher

// adr: 435. Freeze the exact approved opaque export, never its runtime rootfs.

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type ExportSnapshot struct {
	dir, path string
}

// Path names a private read-only archive owned by the consuming process.
func (s *ExportSnapshot) Path() string { return s.path }

func (s *ExportSnapshot) Close() error {
	if s == nil || s.dir == "" {
		return nil
	}
	if err := os.RemoveAll(s.dir); err != nil {
		return err
	}
	s.dir, s.path = "", ""
	return nil
}

// SnapshotExport copies and hashes the same stream into private storage.
// It authenticates a supplied digest, not the publisher; the caller must
// obtain current scoped approval before passing its expected digest/size.
func SnapshotExport(ctx context.Context, path, expectedDigest string, expectedBytes int64) (*ExportSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(expectedDigest, "sha256:") || !validHash(strings.TrimPrefix(expectedDigest, "sha256:")) || expectedBytes <= 0 || expectedBytes > api.LocalOCIMaxArchiveBytes {
		return nil, ErrInvalid
	}
	in, err := openExportFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != expectedBytes {
		return nil, ErrInvalid
	}
	dir, err := os.MkdirTemp("", "gregale-approved-export-")
	if err != nil {
		return nil, err
	}
	snapshot := &ExportSnapshot{dir: dir, path: filepath.Join(dir, "image.tar")}
	if err := copyExpectedExport(ctx, in, snapshot.path, expectedDigest, expectedBytes); err != nil {
		return nil, errors.Join(err, snapshot.Close())
	}
	return snapshot, nil
}

func copyExpectedExport(ctx context.Context, in io.Reader, path, expectedDigest string, expectedBytes int64) error {
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, h), io.LimitReader(contextReader{ctx, in}, expectedBytes+1))
	if copyErr == nil && (n != expectedBytes || fmt.Sprintf("sha256:%x", h.Sum(nil)) != expectedDigest) {
		copyErr = ErrInvalid
	}
	if copyErr == nil {
		copyErr = ctx.Err()
	}
	if copyErr == nil {
		copyErr = out.Chmod(0400)
	}
	closeErr := out.Close()
	return errors.Join(copyErr, closeErr)
}
