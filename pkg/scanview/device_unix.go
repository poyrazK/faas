//go:build linux || darwin

package scanview

import (
	"io/fs"
	"os"
	"syscall"
)

// Overlay non-directory st_dev may identify a backing layer. Overlay directory
// st_dev identifies the merged filesystem and detects nested mounted trees.
func sameDirectoryDevice(root, other fs.FileInfo) bool {
	a, ok := root.Sys().(*syscall.Stat_t)
	b, valid := other.Sys().(*syscall.Stat_t)
	return ok && valid && a.Dev == b.Dev
}

func openRegular(root *os.Root, name string) (*os.File, error) {
	// A replaced final symlink or FIFO cannot escape or block this opener.
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
