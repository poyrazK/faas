//go:build !windows

package main

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// checkPrivateStateDir refuses a fallback state directory that another user
// owns or that others can open.
func checkPrivateStateDir(dir string, info fs.FileInfo) error {
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s belongs to another user", dir)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s is open to other users (mode %v)", dir, info.Mode().Perm())
	}
	return nil
}
