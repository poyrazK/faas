package sched

// SetSnapDirForTesting overrides the snapshot directory root used by
// SnapDir() and snapshotPaths(). It exists for cross-package tests
// (pkg/imaged/loop_test.go's TestLoopDeleteSnapshotsAndFiles) that need
// to drive deleteSnapshotsAndFiles against a hermetic t.TempDir() rather
// than /srv/fc/snap (which is not writable on dev macOS hosts).
//
// Production callers never touch this. The value is published atomically so
// legacy tests that still need the process-wide seam cannot race readers;
// new fixtures should prefer dependency injection such as DiskDrift.WithSnapDir.
func SetSnapDirForTesting(path string) {
	if path == "" {
		path = "/srv/fc/snap"
	}
	snapDir.Store(path)
}
