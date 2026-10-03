package buildpublisher

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"io"
	"os"
)

type contextReader struct {
	ctx context.Context
	io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

// MeasureExport hashes the complete opened export, including tar padding.
// It does not validate OCI content, select layers or confer publisher trust.
func MeasureExport(ctx context.Context, path string) (string, int64, error) {
	f, err := os.Open(path) //nolint:forbidigo // Builder-owned completed export or cache lease; the opened descriptor is bounded and checked as regular before reading.
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > api.LocalOCIMaxArchiveBytes {
		return "", 0, ErrInvalid
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(contextReader{ctx, f}, api.LocalOCIMaxArchiveBytes+1))
	if err != nil {
		return "", 0, err
	}
	after, err := f.Stat()
	if err != nil || n != before.Size() || n != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return "", 0, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	return fmt.Sprintf("sha256:%x", h.Sum(nil)), n, nil
}
