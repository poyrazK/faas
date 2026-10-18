package objectnode

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// Object nodes run on Linux. These darwin twins keep the package building
// and testable on developer Macs (make test must pass on any machine).

// renameNoReplace is renamex_np(RENAME_EXCL), which also fails with EEXIST.
func renameNoReplace(oldpath, newpath string) error {
	return unix.RenamexNp(oldpath, newpath, unix.RENAME_EXCL)
}

func statChangedNanos(st *syscall.Stat_t) int64 {
	return st.Ctimespec.Sec*1e9 + st.Ctimespec.Nsec
}
