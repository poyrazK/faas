//go:build linux

package fcvm

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// reflinkFile makes dst a copy-on-write clone of src (FICLONE). It fails on
// filesystems without shared extents, such as ext4.
// lastUse is a copy's access time, which accountCopy sets on every use.
func lastUse(info os.FileInfo) time.Time {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return time.Unix(st.Atim.Sec, st.Atim.Nsec)
	}
	return info.ModTime()
}

// pmemBaseFileIdentity names the base file itself rather than its
// timestamps: its device and inode, and its birth time where the filesystem
// records one (XFS and ext4 do), so a recycled inode number is not mistaken
// for the base it replaced. info is base's os.Stat result.
func pmemBaseFileIdentity(base string, info os.FileInfo) (string, error) {
	var stx unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, base, 0, unix.STATX_INO|unix.STATX_BTIME, &stx); err != nil {
		return "", fmt.Errorf("statx %s: %w", base, err)
	}
	id := fmt.Sprintf("%d:%d:%d", stx.Dev_major, stx.Dev_minor, stx.Ino)
	if stx.Mask&unix.STATX_BTIME != 0 {
		id += fmt.Sprintf(":%d.%d", stx.Btime.Sec, stx.Btime.Nsec)
	} else if st, ok := info.Sys().(*syscall.Stat_t); ok {
		// Without a birth time, the change time still moves when the file is
		// replaced; it also moves on a touch, which only costs a re-clone.
		id += fmt.Sprintf(":c%d.%d", st.Ctim.Sec, st.Ctim.Nsec)
	}
	return id, nil
}

func reflinkFile(dst, src string) error {
	in, err := os.Open(src) //nolint:forbidigo // cache-owned base image; read-only source of the clone.
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:forbidigo // vmmd-owned pmem copy directory.
	if err != nil {
		return err
	}
	if err := unix.IoctlFileClone(int(out.Fd()), int(in.Fd())); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
