//go:build !linux

package fcvm

import (
	"os"
	"strconv"
	"time"
)

// writeMemoryReclaim writes the request; only Linux has memory.reclaim, so
// other platforms reach it only through unit tests with a fake cgroup tree.
func writeMemoryReclaim(path string, n int64, _ time.Duration) error {
	return os.WriteFile(path, []byte(strconv.FormatInt(n, 10)), 0o644)
}
