//go:build unix

package devpatch

import (
	"io/fs"
	"syscall"
)

// singleLink reports whether info describes a file with exactly one hard link.
func singleLink(info fs.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Nlink == 1
}
