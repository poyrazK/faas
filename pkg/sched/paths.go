package sched

import (
	"sync/atomic"

	"github.com/onebox-faas/faas/pkg/storagekey"
)

// paths.go is the single place schedd derives the host filesystem locations of
// an instance's boot inputs (spec §8: /srv/fc/base read-only bases, lv-fc app
// layers + snapshots). vmmd is told these paths on the wire (ADR-014); it never
// discovers them itself. imaged (PR3) consumes the same convention on the
// snapshot_written handshake so a park and the next wake agree on where a
// snapshot lives.
//
// The storage key layout itself lives in pkg/storagekey.

// snapDir is the snapshot blob directory root (spec §8). Held as a var so
// tests in pkg/imaged can override it via SetSnapDirForTesting; production
// never mutates it.
var snapDir atomic.Value // stores string; kept as a seam for legacy tests

func init() {
	snapDir.Store("/srv/fc/snap")
}

// SnapDir returns the per-deployment snapshot blob directory root. imaged
// uses this for F5 filesystem cleanup (delete the snap dir when a deployment
// falls out of the bounded rollback retention window or when its app is
// soft-deleted).
func SnapDir() string { return snapDir.Load().(string) }

// baseKey returns the StorageBackend key for the drive0 shared base
// rootfs for an app's runtime. Function apps (runtime set) boot the
// matching runner base; plain apps boot the generic base image (spec
// §2, ADR-003 — same data plane either way). Mirrors basePath's
// switch but returns the canonical key the wake wire carries
// (issue #96 / ADR-025 axis 2 / PR #116).
func baseKey(runtime string) string {
	return BaseKey(runtime)
}

// layerKey returns the StorageBackend key for the drive1 per-app layer
// for a deployment. Prefers the rootfs_key column on the deployments
// row (populated by imaged at build time, see migration 00025). Falls
// back to sched.LayerKey for rows where imaged predates the column
// (rare in practice — every new row gets one on build).
//
// (rootfsKey, deploymentID) keeps the helper decoupled from pkg/state
// — sched doesn't need the full Deployment struct to derive a key.
//
// Issue #96 / ADR-025 axis 2 / PR #116: this replaces layerPath on the
// wake wire.
func layerKey(rootfsKey, deploymentID string) string {
	if rootfsKey != "" {
		return rootfsKey
	}
	return LayerKey(deploymentID)
}

// --- Storage key helpers (issue #96 / ADR-025 axis 2) ---------------------
//
// The key layout lives in pkg/storagekey, a leaf package, so the daemons
// that name keys (vmmd, imaged, builderd) do not link the scheduler. These
// wrappers keep sched's callers unchanged; new code outside pkg/sched
// should call pkg/storagekey directly.

// AppLayerKey returns the storage key for a per-app drive1 ext4 layer
// (storagekey.AppLayer).
func AppLayerKey(slug, deploymentID string) string {
	return storagekey.AppLayer(slug, deploymentID)
}

// AppSidecarLayerKey returns the storage key for one sidecar's ext4 layer
// (storagekey.AppSidecarLayer, issue #463 / ADR-069).
func AppSidecarLayerKey(slug, deploymentID, sidecarName string) string {
	return storagekey.AppSidecarLayer(slug, deploymentID, sidecarName)
}

// SnapshotMemKey returns the storage key for a deployment's snapshot mem
// blob (storagekey.SnapshotMem).
func SnapshotMemKey(deploymentID string) string {
	return storagekey.SnapshotMem(deploymentID)
}

// SnapshotVMStateKey returns the storage key for a deployment's snapshot
// vmstate blob (storagekey.SnapshotVMState).
func SnapshotVMStateKey(deploymentID string) string {
	return storagekey.SnapshotVMState(deploymentID)
}

// SnapshotWarmMemKey returns the storage key for a deployment's warm-tier
// snapshot mem blob (storagekey.SnapshotWarmMem, issue #470). The
// engine's captureWarmSnapshotLocked uses it to frame the
// vmmdgrpc.WarmSnapshot RPC.
func SnapshotWarmMemKey(deploymentID string) string {
	return storagekey.SnapshotWarmMem(deploymentID)
}

// SnapshotWarmVMStateKey is the warm-tier sibling of SnapshotVMStateKey
// (storagekey.SnapshotWarmVMState).
func SnapshotWarmVMStateKey(deploymentID string) string {
	return storagekey.SnapshotWarmVMState(deploymentID)
}

// BaseKey returns the storage key for a runtime's shared drive0 base ext4
// on this host's arch (storagekey.Base).
func BaseKey(runtime string) string {
	return storagekey.Base(runtime)
}

// BaseKeyForArch returns the per-arch storage key for a runtime's shared
// drive0 base ext4 (storagekey.BaseForArch). This is the form schedd's
// wake wire carries and the form imaged publishes under.
func BaseKeyForArch(runtime, arch string) string {
	return storagekey.BaseForArch(runtime, arch)
}

// ParentBaseRuntime is the synthetic runtime name of the shared parent
// ext4 (storagekey.ParentBaseRuntime, ADR-053).
const ParentBaseRuntime = storagekey.ParentBaseRuntime

// IsParentBaseKey reports whether storageKey names the parent ext4 for an
// arch the box supports: vmmd's MountParentExt4ReadOnly allow-list
// (storagekey.IsParentBase).
func IsParentBaseKey(storageKey string) bool {
	return storagekey.IsParentBase(storageKey)
}

// Arch name constants for the execution-profile checks.
const (
	archAMD64 = storagekey.ArchAMD64
	archARM64 = storagekey.ArchARM64
)

// BaseDigestKey returns the storage key for a runtime's base-image config
// digest sidecar on this host's arch (storagekey.BaseDigest).
func BaseDigestKey(runtime string) string {
	return storagekey.BaseDigest(runtime)
}

// BaseDigestKeyForArch mirrors BaseKeyForArch for the digest sidecar
// (storagekey.BaseDigestForArch).
func BaseDigestKeyForArch(runtime, arch string) string {
	return storagekey.BaseDigestForArch(runtime, arch)
}

// LayerKey returns the legacy storage key for a deployment's drive1 layer
// (storagekey.Layer).
func LayerKey(deploymentID string) string {
	return storagekey.Layer(deploymentID)
}

// JobLayerKey returns the storage key for a materialized job image
// (storagekey.JobLayer).
func JobLayerKey(jobID string) string {
	return storagekey.JobLayer(jobID)
}

// JobLayerAttemptKey returns the immutable storage key of one job image
// build attempt (storagekey.JobLayerAttempt).
func JobLayerAttemptKey(jobID, artifactID string) string {
	return storagekey.JobLayerAttempt(jobID, artifactID)
}

// KernelKey returns the storage key for a firecracker kernel artifact
// pinned to a firecracker version (storagekey.Kernel).
func KernelKey(fcVersion string) string {
	return storagekey.Kernel(fcVersion)
}
