//go:build linux

package main

import "golang.org/x/sys/unix"

func statJailFS(path string) (size, avail uint64, ok bool) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil || st.Bsize <= 0 {
		return 0, 0, false
	}
	bs := uint64(st.Bsize)
	return st.Blocks * bs, st.Bavail * bs, true
}
