//go:build linux

// adr: 20261026113920
package fcvm

import (
	"errors"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

func kernelBootID() (string, error) {
	raw, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	boot := strings.TrimSpace(string(raw))
	if !looksLikeInstanceID(boot) {
		return "", errors.New("invalid kernel boot id")
	}
	return boot, nil
}

func onTmpfs(path string) (bool, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return false, err
	}
	return st.Type == unix.TMPFS_MAGIC, nil
}
