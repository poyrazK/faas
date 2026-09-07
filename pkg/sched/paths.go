package sched

import (
	goruntime "runtime"

	"github.com/onebox-faas/faas/pkg/state"
)

// paths.go is the single place schedd derives the host filesystem locations of
// an instance's boot inputs (spec §8: /srv/fc/base read-only bases, lv-fc app
// layers + snapshots). vmmd is told these paths on the wire (ADR-014); it never
// discovers them itself. imaged (PR3) consumes the same convention on the
// snapshot_written handshake so a park and the next wake agree on where a
// snapshot lives.
//
// SnapshotMemKey / SnapshotVMStateKey are thin wrappers over the
// state.SnapMemKey / state.SnapVMStateKey helpers in pkg/state — the
// canonical form lives there because pkg/state owns the
// snapshots.storage_key column. Sched is a higher-level layer that
// already imports pkg/state (engine.go), so the helper can be
// re-exported without an import cycle.

// snapDir is the snapshot blob directory root (spec §8). Held as a var so
// tests in pkg/imaged can override it via SetSnapDirForTesting; production
// never mutates it.
var snapDir = "/srv/fc/snap"

// SnapDir returns the per-deployment snapshot blob directory root. imaged
// uses this for F5 filesystem cleanup (delete the snap dir when a deployment
// falls out of the bounded rollback retention window or when its app is
// soft-deleted).
func SnapDir() string { return snapDir }

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

// AppSidecarLayerKey returns the storage key for ONE sidecar's
// per-workload ext4 layer (issue #463 / ADR-069 / PR-B). Mirrors
// the apps/<slug>/<depID>.ext4 shape with a "-<sidecarName>"
// suffix so each sidecar's ext4 lives next to the main app layer
// in the same prefix. Production PrefixRouter maps "apps/" to
// /var/lib/faas/apps; the cleanup walk in
// pkg/imaged/handler.go::cleanupAppFiles uses these keys to drop
// stale sidecar ext4s when an app is deleted or replaced.
//
// sidecarName is the customer-chosen name (validated to a portable
// charset at pkg/api/dto.go::Sidecar.Validate) — alpha-num +
// dash + underscore, max 32 chars, no slashes — so embedding it
// in the storage key is safe.
func AppSidecarLayerKey(slug, deploymentID, sidecarName string) string {
	return "apps/" + slug + "/" + deploymentID + "-" + sidecarName + ".ext4"
}

// SnapshotMemKey returns the storage key for a deployment's snapshot mem
// blob (the RAM state at Pause). Mirrors the legacy
// <snapDir>/<deploymentID>/mem path. Thin wrapper over
// state.SnapMemKey so the canonical form lives in one place — pkg/state
// owns the snapshots.storage_key column.
func SnapshotMemKey(deploymentID string) string {
	return state.SnapMemKey(deploymentID)
}

// SnapshotVMStateKey returns the storage key for a deployment's snapshot
// vmstate blob (Firecracker microVM state file at Pause). Mirrors
// <snapDir>/<deploymentID>/vmstate. Thin wrapper over
// state.SnapVMStateKey (same rationale as SnapshotMemKey).
func SnapshotVMStateKey(deploymentID string) string {
	return state.SnapVMStateKey(deploymentID)
}

// SnapshotWarmMemKey (issue #470 / PR #470-FU-A) returns the storage
// key for a deployment's warm-tier snapshot mem blob. Mirrors the
// /warm/ segment of <snapDir>/<deploymentID>/warm/mem. Thin wrapper
// over state.WarmSnapMemKey so the canonical form lives in one place.
// The engine's captureWarmSnapshotLocked (pkg/sched/engine.go) calls
// this when framing the vmmdgrpc.WarmSnapshot RPC.
func SnapshotWarmMemKey(deploymentID string) string {
	return state.WarmSnapMemKey(deploymentID)
}

// SnapshotWarmVMStateKey (issue #470 / PR #470-FU-A) is the warm-tier
// sibling of SnapshotVMStateKey. Mirrors the /warm/ segment of
// <snapDir>/<deploymentID>/warm/vmstate. Thin wrapper over
// state.WarmSnapVMStateKey for the same reason as SnapshotWarmMemKey.
func SnapshotWarmVMStateKey(deploymentID string) string {
	return state.WarmSnapVMStateKey(deploymentID)
}

// BaseKey returns the storage key for a runtime's shared drive0 base ext4
// image. Returns "base/base.ext4" for plain apps, "base/runner-<runtime>.ext4"
// for function apps. The key is per-arch (issue #197 B3.3) — the same
// runtime produces different base ext4s on amd64 vs arm64. Thin wrapper
// over BaseKeyForArch that pins the arch to runtime.GOARCH for the
// single-box case (this daemon's host arch).
func BaseKey(runtime string) string {
	return BaseKeyForArch(runtime, goruntime.GOARCH)
}

// BaseKeyForArch returns the storage key for a runtime's shared drive0
// base ext4 image, partitioned by arch. Different arches need distinct
// base ext4s (the initramfs, kernel modules, and userland binaries don't
// cross over). The legacy BaseKey(runtime) collapses to a single host
// arch; this is the form schedd's wake wire carries and the form imaged
// publishes under.
//
// Shapes:
//   - runtime == "": "base/base-<arch>.ext4"
//   - runtime != "": "base/runner-<runtime>-<arch>.ext4"
func BaseKeyForArch(runtime, arch string) string {
	if runtime == "" {
		return "base/base-" + arch + ".ext4"
	}
	return "base/runner-" + runtime + "-" + arch + ".ext4"
}

// ParentBaseRuntime is the synthetic runtime name pkg/imaged uses for
// the shared debian:12-slim parent ext4 every child runtime
// (node22, node24, python312, python313) layers on top of (ADR-053).
// The name lives in pkg/sched so the gRPC allow-list check
// (pkg/vmmdgrpc/server.go::MountParentExt4ReadOnly) and the imaged
// staging-time composition share one canonical spelling. Keep in
// sync with pkg/imaged's BaseRefDebianParent constant.
const ParentBaseRuntime = "base-debian-parent"

// IsParentBaseKey returns true when storageKey names the canonical
// parent ext4 for an arch the box supports. The check is the gRPC
// allow-list for MountParentExt4ReadOnly — anything else (a per-app
// base key, a layer key, an arbitrary blob) is rejected at the
// wire boundary so a misbehaving imaged caller (or a leaked token
// from another `faas`-group member) cannot read arbitrary storage
// bytes through vmmd's loopback mount. The set of supported
// arches is constructed at startup from runtime.GOARCH plus the
// x86_64 sibling (a single-arch box vs a heterogeneous cluster).
func IsParentBaseKey(storageKey string) bool {
	if storageKey == "" {
		return false
	}
	for _, alt := range parentBaseKeyAliases() {
		if storageKey == alt {
			return true
		}
	}
	return false
}

// parentBaseKeyAliases returns every storageKey that names a
// parent-base ext4 vmmd is willing to mount. Production boxes ship
// one arch; we include the cross-arch sibling so a heterogenous
// cluster (one box amd64, one arm64) doesn't reject a sibling's
// staging request routed through this vmmd.
//
// Kept as a function (not a const slice) so a future arch can be
// added without touching every callsite — IsParentBaseKey is the
// single consumer.
func parentBaseKeyAliases() []string {
	arches := []string{goruntime.GOARCH}
	switch goruntime.GOARCH {
	case archAMD64:
		arches = append(arches, archARM64)
	case archARM64:
		arches = append(arches, archAMD64)
	}
	out := make([]string, 0, len(arches))
	for _, a := range arches {
		out = append(out, BaseKeyForArch(ParentBaseRuntime, a))
	}
	return out
}

// Arch name constants. Defined here (rather than reusing literals)
// to keep goconst happy across the helpers — the arch strings
// appear in BaseKeyForArch call sites, the parent-allow-list switch,
// and the test fixtures.
const (
	archAMD64 = "amd64"
	archARM64 = "arm64"
)

// BaseDigestKey returns the storage key for a runtime's base-image
// config digest sidecar. The sidecar is the immutable check on whether
// the staged base ext4 needs re-pulling. Thin wrapper over
// BaseDigestKeyForArch that pins the arch to runtime.GOARCH.
func BaseDigestKey(runtime string) string {
	return BaseDigestKeyForArch(runtime, goruntime.GOARCH)
}

// BaseDigestKeyForArch mirrors BaseKeyForArch for the digest sidecar.
// Same per-arch partition — a fresh arm64 install must not nuke an
// existing amd64 sidecar when both arches share the same storage root.
func BaseDigestKeyForArch(runtime, arch string) string {
	if runtime == "" {
		return "base/base-" + arch + ".ext4.digest"
	}
	return "base/runner-" + runtime + "-" + arch + ".ext4.digest"
}

// LayerKey returns the storage key for a deployment's drive1 layer.
// Returns "layers/<deploymentID>.ext4".
func LayerKey(deploymentID string) string {
	return "layers/" + deploymentID + ".ext4"
}

// KernelKey returns the storage key for a firecracker kernel artifact
// pinned to a firecracker version. vmmd fetches this on first boot of
// the version.
func KernelKey(fcVersion string) string {
	return "kernel/" + fcVersion
}
