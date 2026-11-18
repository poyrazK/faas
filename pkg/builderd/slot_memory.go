package builderd

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// builderSliceDir is the cgroup-v2 directory of faas-cp-build.slice, the
// memory fence every builder VM runs under.
const builderSliceDir = "/sys/fs/cgroup/faas.slice/faas-cp.slice/faas-cp-build.slice"

// builderSliceFreeMB is memory.max minus memory.current of the builder slice,
// in MiB. ok is false when the slice cannot be read or has no limit: the
// second slot then stays closed, as it was before it existed.
func (b *Builderd) builderSliceFreeMB() (int, bool) {
	if b.sliceFreeMB != nil {
		return b.sliceFreeMB()
	}
	return cgroupFreeMB(builderSliceDir)
}

func cgroupFreeMB(dir string) (int, bool) {
	limit, ok := readCgroupBytes(filepath.Join(dir, "memory.max"))
	if !ok {
		return 0, false
	}
	used, ok := readCgroupBytes(filepath.Join(dir, "memory.current"))
	if !ok {
		return 0, false
	}
	if used >= limit {
		return 0, true
	}
	return int((limit - used) >> 20), true
}

func readCgroupBytes(path string) (int64, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	value, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	return value, err == nil && value >= 0
}
