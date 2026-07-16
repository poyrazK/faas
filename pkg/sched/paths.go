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
	layerDir = "/srv/fc/layers"
	// snapDir holds per-deployment snapshot blobs (mem file + vmstate).
	snapDir = "/srv/fc/snap"
)

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
func layerPath(deploymentID string) string {
	return layerDir + "/" + deploymentID + ".ext4"
}

// snapshotPaths returns the mem file and vmstate file for a deployment's
// snapshot. vmmd writes both on PauseAndSnapshot and reads both on restore; the
// snapshot row imaged records (PR3) stores snapDir/<deployment> as its Path.
func snapshotPaths(deploymentID string) (memPath, vmstatePath string) {
	dir := snapDir + "/" + deploymentID
	return dir + "/mem", dir + "/vmstate"
}
