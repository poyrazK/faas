package state

import "strings"

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
//	select id from compute_nodes where name = DefaultLocalNodeName
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

// DefaultLocalityLabel is the region/zone value the synthetic
// default-local compute node is backfilled to in the memstore
// (memstore.go::seedDefaultLocalNodeLocked) and in
// migrations/00069_compute_nodes_region_zone.sql. Mirrored as a
// single constant so the linter's goconst rule sees one
// declaration and a future migration drift surfaces here, not
// across three files. The SQL string literal is intentionally
// not bindable from Go — postgres receives a literal
// 'local' — but the test assertions in memstore_test.go
// compare against this const to keep the contract
// single-sourced on the Go side.
const DefaultLocalityLabel = "local"

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

// WarmSnapMemKey (issue #470 / PR #470-FU-A) returns the canonical
// StorageBackend key for a deployment's warm-tier snapshot mem blob.
// Mirrors <snapDir>/<deploymentID>/warm/mem on the local backend.
// The warm tier captures the VM in RUNNING state (runner is alive and
// can keep serving requests across the pause window) so the next wake
// resumes handlers mid-flight instead of going through cold boot. The
// /warm/ segment keeps the blob physically separate from the init-tier
// /mem blob so PR-C's per-tier GC floor (2 warm + 2 init) can address
// them with a single prefix match.
func WarmSnapMemKey(deploymentID string) string {
	return "snap/" + deploymentID + "/warm/mem"
}

// WarmSnapVMStateKey (issue #470 / PR #470-FU-A) is the warm-tier
// sibling of SnapVMStateKey. Mirrors <snapDir>/<deploymentID>/warm/vmstate
// and lands under the same /warm/ namespace as WarmSnapMemKey so a
// single wildcard GC predicate covers both blobs.
func WarmSnapVMStateKey(deploymentID string) string {
	return "snap/" + deploymentID + "/warm/vmstate"
}

// SnapshotCaptureMemKey gives a capture its own immutable object namespace.
// The row is published only after both objects have been written successfully.
func SnapshotCaptureMemKey(deploymentID, tier, captureID string) string {
	prefix := "snap/" + deploymentID + "/"
	if tier == SnapshotTierWarm {
		prefix += "warm/"
	}
	return prefix + "captures/" + captureID + "/mem"
}

// SnapshotVMStateKey derives the paired device state from the memory key.
// Legacy rows with noncanonical keys retain the deployment/tier fallback.
func SnapshotVMStateKey(s Snapshot) string {
	if strings.HasPrefix(s.StorageKey, "snap/") && strings.HasSuffix(s.StorageKey, "/mem") {
		return strings.TrimSuffix(s.StorageKey, "/mem") + "/vmstate"
	}
	if s.Tier == SnapshotTierWarm {
		return WarmSnapVMStateKey(s.DeploymentID)
	}
	return SnapVMStateKey(s.DeploymentID)
}

// IsSnapshotCaptureKey distinguishes immutable capture objects from legacy
// mutable deployment keys. Cleanup must never remove a legacy shared pair.
func IsSnapshotCaptureKey(key string) bool {
	parts := strings.Split(key, "/")
	if len(parts) == 6 && parts[2] == "warm" {
		parts = append(parts[:2:2], parts[3:]...)
	}
	return len(parts) == 5 && parts[0] == "snap" && parts[1] != "" && parts[2] == "captures" && parts[3] != "" && parts[4] == "mem"
}
