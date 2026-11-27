package storage

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// cloneFile reflinks src to dst where it can, and otherwise copies src's data
// regions into a sparse dst. dst is replaced.
func cloneFile(dst, src string) (cloned bool, err error) {
	in, err := os.Open(src) //nolint:forbidigo // a node-owned volume path the caller resolved.
	if err != nil {
		return false, fmt.Errorf("storage: clone %s: %w", src, err)
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return false, fmt.Errorf("storage: clone %s: %w", src, err)
	}
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("storage: clone %s: %w", dst, err)
	}
	out, err := os.OpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600) //nolint:forbidigo // see above.
	if err != nil {
		return false, fmt.Errorf("storage: clone %s: %w", dst, err)
	}
	defer func() {
		if cerr := out.Close(); cerr != nil && err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(dst)
		}
	}()
	if cloneInto(out, in) {
		return true, nil
	}
	extents, err := fileDataExtents(in, info.Size())
	if err != nil {
		return false, fmt.Errorf("storage: clone %s: map data: %w", src, err)
	}
	for _, e := range extents {
		if _, err := io.Copy(io.NewOffsetWriter(out, e.off), io.NewSectionReader(in, e.off, e.length)); err != nil {
			return false, fmt.Errorf("storage: clone %s: copy: %w", src, err)
		}
	}
	if err := out.Truncate(info.Size()); err != nil {
		return false, fmt.Errorf("storage: clone %s: size: %w", dst, err)
	}
	return false, out.Sync()
}
