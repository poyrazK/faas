//go:build linux

package fcvm

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// backingDigestXattr holds a file version's ADR-510 digest on the file
// itself (amendment 2). The trusted namespace needs CAP_SYS_ADMIN to read or
// write, so only vmmd, the one root daemon, can record or see it; the
// non-root daemons that share the node cache cannot forge one.
const backingDigestXattr = "trusted.faas.backing-digest"

// fileBirthTime is f's birth time where the filesystem records one (XFS and
// ext4 do). A recycled inode number then never inherits the identity, and
// so the digest, of the file it replaced.
func fileBirthTime(f *os.File) (sec int64, nsec uint32, ok bool) {
	var stx unix.Statx_t
	if err := unix.Statx(int(f.Fd()), "", unix.AT_EMPTY_PATH, unix.STATX_BTIME, &stx); err != nil {
		return 0, 0, false
	}
	if stx.Mask&unix.STATX_BTIME == 0 {
		return 0, 0, false
	}
	return stx.Btime.Sec, stx.Btime.Nsec, true
}

// xattrBackingDigests records digests in backingDigestXattr. Reads and
// writes go through the open descriptor, so they describe exactly the file
// version being identified even if a refresh renames another over its path.
type xattrBackingDigests struct{}

func defaultBackingDigestRecorder() backingDigestRecorder { return xattrBackingDigests{} }

func (xattrBackingDigests) recorded(f *os.File, size int64) (string, bool) {
	buf := make([]byte, 160)
	n, err := unix.Fgetxattr(int(f.Fd()), backingDigestXattr, buf)
	if err != nil || n <= 0 {
		return "", false
	}
	return parseRecordedBackingDigest(string(buf[:n]), size)
}

func (xattrBackingDigests) record(f *os.File, size int64, digest string) error {
	err := unix.Fsetxattr(int(f.Fd()), backingDigestXattr, []byte(formatRecordedBackingDigest(size, digest)), 0)
	if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EROFS) || errors.Is(err, unix.ENOTSUP) {
		// Not root (tests), a read-only image (the kernel under /opt), or a
		// filesystem without trusted xattrs: the in-memory memo still holds.
		return nil
	}
	return err
}
