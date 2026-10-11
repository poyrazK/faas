//go:build linux

package storage

import (
	"os"

	"golang.org/x/sys/unix"
)

// punchHoles deallocates ranges CopySparse skipped. Every range is all zero in
// the source and was never written to f, so a reader sees the same zero bytes
// before and after; only blocks the filesystem allocated speculatively are
// released. It is best-effort: a filesystem without hole punching (or an
// error) leaves the file correct, merely larger.
func punchHoles(f *os.File, holes []fileRange) {
	for _, h := range holes {
		if err := unix.Fallocate(int(f.Fd()), unix.FALLOC_FL_PUNCH_HOLE|unix.FALLOC_FL_KEEP_SIZE, h.off, h.length); err != nil {
			return
		}
	}
}
