package state

// StorageBackend key shape for snapshot mem blobs (issue #96, ADR-025
// axis 2). Lives in pkg/state because state owns the snapshots table's
// storage_key column; sched/paths.go's SnapshotMemKey wraps this for
// callers that already import sched, so neither definition can drift.

// DefaultLocalNodeName is the stable identifier of the synthetic
// single-host vmmd node seeded by migrations/00024_compute_nodes.sql.
// The row's id is gen_random_uuid() (the column default) — not a
// hard-coded sentinel — so re-applies don't race on a magic UUID
// literal. Callers that need the UUID resolve it via
//
//   select id from compute_nodes where name = DefaultLocalNodeName
//
// and cache the result. schedd's NodeLedger keeps this cached for
// the daemon lifetime (cmd/schedd/main.go's runHeartbeat reads
// ActiveComputeNodes once at startup and threads the resolved id
// into the per-node reservation map).
//
// Why a name, not an id: the operator-facing identity of the row
// is the name (POST /v1/compute-nodes' body, `faas compute-node
// list` output, log lines). The id is an implementation detail.
// Pinning the name keeps every fixture, test, and migration literal
// referencing the same value — no magic UUID scattered across
// files. PR #112 (issue #97 / ADR-025 axis 3) ships the column +
// state plumbing; PR #113 evolves the Wake flow to consult
// compute_nodes via this row.
const DefaultLocalNodeName = "default-local"

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
