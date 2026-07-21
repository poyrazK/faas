package sched

// paths.go is the single place schedd derives the host filesystem locations of
// an instance's boot inputs (spec §8: /srv/fc/base read-only bases, lv-fc app
// layers + snapshots). vmmd is told these paths on the wire (ADR-014); it never
// discovers them itself. imaged (PR3) consumes the same convention on the
// snapshot_written handshake so a park and the next wake agree on where a
// snapshot lives.

const (
	// baseDir holds the shared read-only base rootfs images (spec §4.6 two-drive
	// scheme, drive0). One per runtime; plain apps boot a generic base.
	baseDir = "/srv/fc/base"
	// layerDir holds per-deployment app layers (drive1). One ext4 per deployment.
	// Default location; layerPath uses deployments.rootfs_path when set.
	layerDir = "/srv/fc/layers"
)

// snapDir is the snapshot blob directory root (spec §8). Held as a var so
// tests in pkg/imaged can override it via SetSnapDirForTesting; production
// never mutates it.
var snapDir = "/srv/fc/snap"

// SnapDir returns the per-deployment snapshot blob directory root. imaged
// uses this for F5 filesystem cleanup (delete the snap dir when a deployment
// falls out of the "current + previous" retention window or when its app
// is soft-deleted).
func SnapDir() string { return snapDir }

// basePath returns the drive0 shared base rootfs for an app's runtime. Function
// apps (runtime set) boot the matching runner base; plain apps boot the generic
// base image (spec §2, ADR-003 — same data plane either way).
func basePath(runtime string) string {
	switch runtime {
	case "node22":
		return baseDir + "/runner-node22.ext4"
	case "python312":
		return baseDir + "/runner-python312.ext4"
	default:
		return baseDir + "/base.ext4"
	}
}

// layerPath returns the drive1 per-app layer for a deployment.
//
// imaged stamps the canonical path (appsRoot/<slug>/<deploymentID>.ext4) into
// deployments.rootfs_path after Build succeeds (pkg/imaged/handler.go);
// schedd trusts that row rather than recomputing. The legacy constant
// layerDir is the fallback for rows where imaged predates the path stamp
// (rare in practice — every new row gets a path on creation).
//
// Two-arg signature (rootfsPath, deploymentID) keeps this helper decoupled
// from pkg/state — sched doesn't need the full Deployment struct to derive
// a path, and the struct's other fields aren't on this code path.
func layerPath(rootfsPath, deploymentID string) string {
	if rootfsPath != "" {
		return rootfsPath
	}
	return layerDir + "/" + deploymentID + ".ext4"
}

// snapshotPaths returns the mem file and vmstate file for a deployment's
// snapshot. vmmd writes both on PauseAndSnapshot and reads both on restore; the
// snapshot row imaged records (PR3) stores snapDir/<deployment> as its Path.
func snapshotPaths(deploymentID string) (memPath, vmstatePath string) {
	dir := snapDir + "/" + deploymentID
	return dir + "/mem", dir + "/vmstate"
}

// --- Storage key helpers (issue #96 / ADR-025 axis 2) ---------------------
//
// Each helper returns a StorageBackend key (see pkg/storage) instead of
// a host path. The helpers are the single source of truth so call sites
// in imaged, vmmd, and sched agree on the canonical form. Keys map to
// today's absolute paths 1:1 when the Production PrefixRouter is rooted
// at /srv/fc with apps/ → /var/lib/faas/apps.
//
// The helpers live in sched (not storage) because they encode the
// namespaced layout sched already owns in this file; introducing a new
// package would have the same interface twice.

// AppLayerKey returns the storage key for a per-app drive1 ext4 layer.
// Mirrors the legacy <appsRoot>/<slug>/<deploymentID>.ext4 path; the
// production PrefixRouter maps "apps/" to /var/lib/faas/apps.
func AppLayerKey(slug, deploymentID string) string {
	return "apps/" + slug + "/" + deploymentID + ".ext4"
}

// SnapshotMemKey returns the storage key for a deployment's snapshot mem
// blob (the RAM state at Pause). Mirrors the legacy
// <snapDir>/<deploymentID>/mem path.
func SnapshotMemKey(deploymentID string) string {
	return "snap/" + deploymentID + "/mem"
}

// SnapshotVMStateKey returns the storage key for a deployment's snapshot
// vmstate blob (Firecracker microVM state file at Pause). Mirrors
// <snapDir>/<deploymentID>/vmstate.
func SnapshotVMStateKey(deploymentID string) string {
	return "snap/" + deploymentID + "/vmstate"
}

// BaseKey returns the storage key for a runtime's shared drive0 base ext4
// image. Mirrors the legacy <baseDir>/<runtime>.ext4 (e.g. base.ext4 for
// plain apps).
func BaseKey(runtime string) string {
	if runtime == "" {
		return "base/base.ext4"
	}
	return "base/runner-" + runtime + ".ext4"
}

// BaseDigestKey returns the storage key for a runtime's base-image
// config digest sidecar. The sidecar is the immutable check on whether
// the staged base ext4 needs re-pulling.
func BaseDigestKey(runtime string) string {
	if runtime == "" {
		return "base/base.ext4.digest"
	}
	return "base/runner-" + runtime + ".ext4.digest"
}

// LayerKey returns the storage key for a deployment's drive1 layer in
// the legacy location (<layerDir>/<deploymentID>.ext4). Kept so any
// rows that still carry a layerDir-rooted path resolve identically
// after #96.
func LayerKey(deploymentID string) string {
	return "layers/" + deploymentID + ".ext4"
}

// KernelKey returns the storage key for a firecracker kernel artifact
// pinned to a firecracker version. vmmd fetches this on first boot of
// the version.
func KernelKey(fcVersion string) string {
	return "kernel/" + fcVersion
}
