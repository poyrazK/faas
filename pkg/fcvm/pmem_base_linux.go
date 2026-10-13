//go:build linux

package fcvm

import (
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
