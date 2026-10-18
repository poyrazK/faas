//go:build linux || darwin

package vmmdmount

import (
	"io/fs"
	"os"
	"syscall"
)

func ownRuntimeScanView(root *os.Root, name string) error {
	info, err := root.Lstat(".")
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ErrInvalidOverlayPath
	}
	return root.Lchown(name, int(stat.Uid), int(stat.Gid))
}

func sameRuntimeScanTargetMetadata(before, after fs.FileInfo) bool {
	a, okA := before.Sys().(*syscall.Stat_t)
	b, okB := after.Sys().(*syscall.Stat_t)
	return okA && okB && a.Uid == b.Uid && a.Gid == b.Gid && before.Mode().Perm() == after.Mode().Perm()
}
