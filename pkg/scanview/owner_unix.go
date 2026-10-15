//go:build linux || darwin

package scanview

import (
	"os"
	"syscall"
)

func projectionOwner(root *os.Root) (func(string) error, error) {
	info, err := root.Lstat(".")
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, ErrInvalid
	}
	return func(name string) error { return root.Lchown(name, int(stat.Uid), int(stat.Gid)) }, nil
}
