package objectnode

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// renameNoReplace moves oldpath to newpath and fails if newpath exists, so
// a publish never overwrites a committed object, blob or backup.
func renameNoReplace(oldpath, newpath string) error {
	return unix.Renameat2(unix.AT_FDCWD, oldpath, unix.AT_FDCWD, newpath, unix.RENAME_NOREPLACE)
}

// statChangedNanos is the inode change time in nanoseconds.
func statChangedNanos(st *syscall.Stat_t) int64 {
	return st.Ctim.Sec*1e9 + st.Ctim.Nsec
}
