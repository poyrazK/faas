//go:build linux

package fcvm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/storage"
)

// mergeDiffSnapshot writes dst as the full memory image a Diff capture
// describes: parent (the image the guest was restored from or last captured
// to) with the diff's data extents laid over it. dst is cloned from parent
// (FICLONE, no data copied on XFS) and only the dirtied pages are written; a
// dirtied page that is now all zeros becomes a hole, so dst stays as sparse
// as a Full capture's sparse export. It returns dst's size and whether the
// clone shared parent's extents (false: parent was copied).
func mergeDiffSnapshot(ctx context.Context, parent *os.File, diffPath, dst string) (size int64, reflinked bool, err error) {
	// nolint:forbidigo // diffPath is the jail-resident file Firecracker just wrote.
	diff, err := os.Open(diffPath)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = diff.Close() }()
	// nolint:forbidigo // dst is a vmmd-allocated snapshot destination.
	out, err := os.OpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return 0, false, err
	}
	defer func() {
		if closeErr := out.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(dst)
		}
	}()
	if cloneErr := unix.IoctlFileClone(int(out.Fd()), int(parent.Fd())); cloneErr == nil {
		reflinked = true
	} else {
		if err = out.Truncate(0); err != nil {
			return 0, false, err
		}
		if _, _, err = storage.CopySparse(ctx, out, io.NewSectionReader(parent, 0, 1<<62)); err != nil {
			return 0, false, fmt.Errorf("copy parent after failed clone: %w", errors.Join(err, cloneErr))
		}
	}
	info, err := diff.Stat()
	if err != nil {
		return 0, reflinked, err
	}
	parentInfo, err := parent.Stat()
	if err != nil {
		return 0, reflinked, err
	}
	if info.Size() != parentInfo.Size() {
		return 0, reflinked, fmt.Errorf("diff is %d bytes, parent %d: guest memory size changed", info.Size(), parentInfo.Size())
	}
	if err = overlayDataExtents(ctx, diff, out, info.Size()); err != nil {
		return 0, reflinked, err
	}
	return info.Size(), reflinked, nil
}

// overlayDataExtents copies src's data extents (SEEK_DATA/SEEK_HOLE) to the
// same offsets in dst, punching a hole wherever a whole page is zero.
func overlayDataExtents(ctx context.Context, src, dst *os.File, size int64) error {
	const chunk = 1 << 20
	const page = 4096
	buf := make([]byte, chunk)
	zero := make([]byte, page)
	fd := int(src.Fd())
	for off := int64(0); off < size; {
		start, err := unix.Seek(fd, off, unix.SEEK_DATA)
		if errors.Is(err, unix.ENXIO) {
			return nil // no data past off
		}
		if err != nil {
			return fmt.Errorf("seek data: %w", err)
		}
		end, err := unix.Seek(fd, start, unix.SEEK_HOLE)
		if err != nil {
			return fmt.Errorf("seek hole: %w", err)
		}
		for pos := start; pos < end; {
			if err := ctx.Err(); err != nil {
				return err
			}
			n := min(int64(chunk), end-pos)
			if _, err := src.ReadAt(buf[:n], pos); err != nil {
				return fmt.Errorf("read diff at %d: %w", pos, err)
			}
			if err := writePagesOrHoles(dst, buf[:n], pos, zero); err != nil {
				return err
			}
			pos += n
		}
		off = end
	}
	return nil
}

// writePagesOrHoles writes data at off in runs: non-zero pages are written,
// all-zero pages are punched out.
func writePagesOrHoles(dst *os.File, data []byte, off int64, zero []byte) error {
	page := len(zero)
	for i := 0; i < len(data); {
		isZero := bytes.Equal(data[i:min(i+page, len(data))], zero[:min(page, len(data)-i)])
		j := i
		for j < len(data) && bytes.Equal(data[j:min(j+page, len(data))], zero[:min(page, len(data)-j)]) == isZero {
			j += page
		}
		j = min(j, len(data))
		if isZero {
			if err := unix.Fallocate(int(dst.Fd()), unix.FALLOC_FL_PUNCH_HOLE|unix.FALLOC_FL_KEEP_SIZE, off+int64(i), int64(j-i)); err != nil {
				return fmt.Errorf("punch hole at %d: %w", off+int64(i), err)
			}
		} else if _, err := dst.WriteAt(data[i:j], off+int64(i)); err != nil {
			return fmt.Errorf("write at %d: %w", off+int64(i), err)
		}
		i = j
	}
	return nil
}
