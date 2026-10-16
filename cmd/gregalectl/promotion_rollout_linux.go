//go:build linux

package main

import (
	"os"
	"syscall"
)

func preserveRolloutOwner(file *os.File, original os.FileInfo) error {
	owner := original.Sys().(*syscall.Stat_t)
	return file.Chown(int(owner.Uid), int(owner.Gid))
}
