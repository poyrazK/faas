package daemonunitspec

import "testing"

// TestUnitSchedd_FileCacheHeadroom pins the two-level memory policy for
// faas-schedd. Layer verification materialises remote OCI blobs into XFS;
// dirty file-backed pages are charged to schedd's cgroup until writeback.
// On 2026-09-19 the old 256M hard cap OOM-killed schedd with about 221M of
// file cache while its steady-state anonymous memory remained about 34M.
//
// MemoryHigh preserves reclaim pressure at the old budget. MemoryMax is
// deliberately larger so a bounded materialisation can flush instead of
// killing the platform's lifecycle owner.
func TestUnitSchedd_FileCacheHeadroom(t *testing.T) {
	u := UnitSchedd()
	if got := u.MemoryHigh; got != "256M" {
		t.Errorf("schedd: MemoryHigh = %q, want 256M", got)
	}
	if got := u.MemoryMax; got != "1G" {
		t.Errorf("schedd: MemoryMax = %q, want 1G", got)
	}
}
