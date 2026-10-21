// Package storagekey is the StorageBackend key layout (issue #96, ADR-025
// axis 2) that schedd, vmmd, imaged and builderd agree on: where a base
// image, an app or job layer, a snapshot blob and a kernel live in storage.
// Keys map 1:1 to today's host paths when the production PrefixRouter is
// rooted at /srv/fc with apps/ → /var/lib/faas/apps.
//
// The layout used to live in pkg/sched (paths.go), which made every daemon
// that names a key link the scheduler engine; pkg/sched and pkg/state now
// delegate here. Keep this package a leaf: it must not import the
// scheduler or the store, so a scheduler fix does not change the inputs
// of vmmd, imaged or builderd.
package storagekey

import (
	goruntime "runtime"

	"github.com/onebox-faas/faas/pkg/api"
)

// Arch names a base image key can carry.
const (
	ArchAMD64 = "amd64"
	ArchARM64 = "arm64"
)

// ParentBaseRuntime is the synthetic runtime name of the shared
// debian:12-slim parent ext4 every child runtime (node22, node24,
// python312, python313) layers on top of (ADR-053). vmmd's
// MountParentExt4ReadOnly allow-list and imaged's staging share this
// spelling. Keep in sync with pkg/imaged's BaseRefDebianParent constant.
const ParentBaseRuntime = "base-debian-parent"

// AppLayer is the key of a per-app drive1 ext4 layer:
// apps/<slug>/<deploymentID>.ext4.
func AppLayer(slug, deploymentID string) string {
	return "apps/" + slug + "/" + deploymentID + ".ext4"
}

// AppSidecarLayer is the key of one sidecar's ext4 layer (issue #463 /
// ADR-069), next to the app layer: apps/<slug>/<deploymentID>-<name>.ext4.
// Sidecar names are validated to a portable charset without slashes
// (api.Sidecar.Validate), so embedding one is safe.
func AppSidecarLayer(slug, deploymentID, sidecarName string) string {
	return "apps/" + slug + "/" + deploymentID + "-" + sidecarName + ".ext4"
}

// Layer is the legacy key of a deployment's drive1 layer, for rows that
// predate deployments.rootfs_key: layers/<deploymentID>.ext4.
func Layer(deploymentID string) string {
	return "layers/" + deploymentID + ".ext4"
}

// JobLayer is the key of a materialized job image: jobs/<jobID>.ext4.
func JobLayer(jobID string) string {
	return "jobs/" + jobID + ".ext4"
}

// JobLayerAttempt is the immutable key of one job image build attempt. The
// artifact ID is a UUID minted by imaged, so an expired worker can never
// overwrite or delete a later attempt's object. The key stays flat to
// preserve the OCI backend's jobs/<tag>.ext4 layout.
func JobLayerAttempt(jobID, artifactID string) string {
	return "jobs/" + jobID + "__" + artifactID + ".ext4"
}

// SnapshotMem is the key of a deployment's snapshot memory blob (the RAM
// state at Pause): snap/<deploymentID>/mem. It is the
// snapshots.storage_key value.
func SnapshotMem(deploymentID string) string {
	return "snap/" + deploymentID + "/mem"
}

// SnapshotVMState is the key of a deployment's Firecracker vmstate blob:
// snap/<deploymentID>/vmstate.
func SnapshotVMState(deploymentID string) string {
	return "snap/" + deploymentID + "/vmstate"
}

// SnapshotWarmMem is the key of a deployment's warm-tier snapshot memory
// blob (issue #470), under its own /warm/ segment so the per-tier GC can
// address both warm blobs with one prefix: snap/<deploymentID>/warm/mem.
func SnapshotWarmMem(deploymentID string) string {
	return "snap/" + deploymentID + "/warm/mem"
}

// SnapshotWarmVMState is the warm-tier sibling of SnapshotVMState.
func SnapshotWarmVMState(deploymentID string) string {
	return "snap/" + deploymentID + "/warm/vmstate"
}

// BaseForArch is the key of a runtime's shared drive0 base ext4, per arch
// (issue #197 B3.3): the initramfs, kernel modules and userland do not
// cross arches.
//
//   - runtime == "": base/base-<arch>.ext4
//   - runtime != "": base/runner-<runtime>-<arch>.ext4
//
// A runtime that shares another runtime's base (binary → go124, ADR-987)
// resolves to that base's key.
func BaseForArch(runtime, arch string) string {
	if runtime == "" {
		return "base/base-" + arch + ".ext4"
	}
	return "base/runner-" + api.FunctionBaseRuntime(runtime) + "-" + arch + ".ext4"
}

// Base is BaseForArch for this host's arch, for single-box callers.
func Base(runtime string) string {
	return BaseForArch(runtime, goruntime.GOARCH)
}

// BaseDigestForArch is the key of the config-digest sidecar next to a base
// ext4, the check on whether the staged base needs re-pulling. Same per-arch
// partition as BaseForArch, so a fresh arm64 install cannot remove an
// amd64 sidecar in a shared storage root.
func BaseDigestForArch(runtime, arch string) string {
	return BaseForArch(runtime, arch) + ".digest"
}

// BaseDigest is BaseDigestForArch for this host's arch.
func BaseDigest(runtime string) string {
	return BaseDigestForArch(runtime, goruntime.GOARCH)
}

// Kernel is the key of the Firecracker kernel artifact pinned to a
// Firecracker version. vmmd fetches it on the first boot of the version.
func Kernel(fcVersion string) string {
	return "kernel/" + fcVersion
}

// IsParentBase reports whether storageKey names the parent ext4 for an
// arch the box supports. It is the gRPC allow-list for vmmd's
// MountParentExt4ReadOnly: anything else (an app base, a layer, an
// arbitrary blob) is refused at the wire, so a misbehaving caller cannot
// read arbitrary storage through vmmd's loopback mount. The host arch's
// sibling is accepted too, for a mixed amd64/arm64 cluster.
func IsParentBase(storageKey string) bool {
	if storageKey == "" {
		return false
	}
	for _, alt := range ParentBaseAliases() {
		if storageKey == alt {
			return true
		}
	}
	return false
}

// ParentBaseAliases is every key IsParentBase accepts: the parent base for
// this host's arch and its sibling arch.
func ParentBaseAliases() []string {
	arches := []string{goruntime.GOARCH}
	switch goruntime.GOARCH {
	case ArchAMD64:
		arches = append(arches, ArchARM64)
	case ArchARM64:
		arches = append(arches, ArchAMD64)
	}
	out := make([]string, 0, len(arches))
	for _, a := range arches {
		out = append(out, BaseForArch(ParentBaseRuntime, a))
	}
	return out
}
