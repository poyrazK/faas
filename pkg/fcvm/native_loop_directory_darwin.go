//go:build darwin

package fcvm

import (
	"errors"
	"os"
	"syscall"
)

// Portable journal tests model mounts; this checks only directory provenance.
func nativeLoopPortableDirectoryIdentity(path string) (nativeLoopIdentity, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nativeLoopIdentity{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm()&0o077 != 0 || stat.Uid != uint32(os.Geteuid()) {
		return nativeLoopIdentity{}, errors.New("native loop mount: point is not a private owned directory")
	}
	return nativeLoopIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}, nil
}
