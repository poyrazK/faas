package daemonunitspec

import "testing"

// TestUnitVmmd_NoMemoryHigh pins the removal of MemoryHigh from
// faas-vmmd.service. This is a "do not re-add without reading this"
// guard, not a style preference.
//
// MemoryHigh=512M was added on 2026-09-03 to contain what looked like
// a vmmd memory leak (2.1 GB RSS). It made the platform strictly
// worse and no deployment could reach `live` until it was removed.
//
// The reason is that vmmd's cgroup is not mostly vmmd. tmpfs pages are
// charged to whichever cgroup writes them, and vmmd populates every
// jail chroot on /srv/fc/jail (tmpfs) with a ~400 MB layer.ext4 — so
// this cgroup carries roughly (live instances x layer size). One Scale
// instance already exceeds 512M.
//
// Crossing MemoryHigh forces reclaim on every allocation, but shmem
// without swap cannot be reclaimed. Measured on a live node: pgscan
// 138,686,137 against pgsteal 2,494,654 — 55 pages scanned per page
// freed — and 2.1M file refaults, because the only evictable pages
// left were the executables' own text. Every process vmmd forked
// (tc, ip, nft, jailer) thrashed on its own code: a `tc qdisc add`
// sat in D state in filemap_fault for a full 35 s cold-boot budget
// before being killed. Lifting the bound live took the cold-boot
// network phase from 18,586 ms to 87 ms.
//
// If you want to bound vmmd's own memory, the jail tmpfs charge has to
// move off this cgroup first (ADR).
func TestUnitVmmd_NoMemoryHigh(t *testing.T) {
	if got := UnitVmmd().MemoryHigh; got != "" {
		t.Errorf("vmmd: MemoryHigh = %q, want empty. "+
			"MemoryHigh throttles against the jail tmpfs charged to this cgroup, "+
			"which is unreclaimable without swap; it wedged cold boot and every "+
			"snapshot capture. Read the comment above this test before re-adding it.", got)
	}
}

// TestUnitVmmd_MemoryMaxIsSliceCeiling pins that vmmd's cap is the
// slice ceiling rather than a tighter per-unit number.
//
// A tighter cap would really be a cap on jail residency (see
// TestUnitVmmd_NoMemoryHigh), so it would OOM-kill vmmd as a function
// of how many instances are live — the exact failure that took a node
// out of rotation on 2026-09-03. The slice remains the real bound, and
// the CI hardening gate still sees a MemoryMax= line. imaged sets 4G
// on the same reasoning.
func TestUnitVmmd_MemoryMaxIsSliceCeiling(t *testing.T) {
	if got := UnitVmmd().MemoryMax; got != FaasCPSliceMemoryMax {
		t.Errorf("vmmd: MemoryMax = %q, want %q (the slice ceiling)", got, FaasCPSliceMemoryMax)
	}
}
