package state

// StorageBackend key shape for snapshot mem blobs (issue #96, ADR-025
// axis 2). Lives in pkg/state because state owns the snapshots table's
// storage_key column; sched/paths.go's SnapshotMemKey wraps this for
// callers that already import sched, so neither definition can drift.

// SnapMemKey returns the canonical StorageBackend key for a
// deployment's snapshot mem blob. Mirrors <snapDir>/<deploymentID>/mem
// (where snapDir defaults to /srv/fc/snap on a single-box deploy) and
// the legacy sched.SnapshotMemKey form. Local backends resolve it to
// a file under /srv/fc; remote backends (e.g. OCIRegistryStorageBackend)
// resolve it to an OCI manifest tag.
//
// Fixtures and any caller constructing a state.Snapshot literal must
// populate Snapshot.StorageKey with this value (or the equivalent from
// the snapshot_written payload); empty values are rejected at the
// CreateSnapshot boundary on both backends.
func SnapMemKey(deploymentID string) string {
	return "snap/" + deploymentID + "/mem"
}

// SnapVMStateKey returns the canonical StorageBackend key for a
// deployment's snapshot vmstate blob. Mirrors
// <snapDir>/<deploymentID>/vmstate.
func SnapVMStateKey(deploymentID string) string {
	return "snap/" + deploymentID + "/vmstate"
}