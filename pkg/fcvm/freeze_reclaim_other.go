//go:build !linux

package fcvm

import (
	"os"
	"strconv"
	"time"
)

// writeMemoryReclaim writes the request; only Linux has memory.reclaim, so
// other platforms reach it only through unit tests with a fake cgroup tree.
func writeMemoryReclaim(path string, n int64, timeout time.Duration) error {
	return writeWithDeadline(path, strconv.FormatInt(n, 10), timeout)
}

// writeWithDeadline writes payload; tests use a fake file tree.
func writeWithDeadline(path, payload string, _ time.Duration) error {
	return os.WriteFile(path, []byte(payload), 0o644)
}
