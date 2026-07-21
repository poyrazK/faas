package sched

// SetSnapDirForTesting overrides the snapshot directory root used by
// SnapDir() and snapshotPaths(). It exists for cross-package tests
// (pkg/imaged/loop_test.go's TestLoopDeleteSnapshotsAndFiles) that need
// to drive deleteSnapshotsAndFiles against a hermetic t.TempDir() rather
// than /srv/fc/snap (which is not writable on dev macOS hosts).
//
// Production callers never touch this — it is unsafe for concurrent use
// (the value lives in a package-level var) but the loop_test.go fixture
// is sequential within a single test. Restore with defer SetSnapDirForTesting("").
func SetSnapDirForTesting(path string) {
	snapDir = path
}
