package fcvm

// Snapshot park/wake (ADR-005). A snapshot is a *cache*, never the truth:
// Firecracker only guarantees a snapshot loads on the exact version that made it,
// so every snapshot is pinned to its `fc_version`. On a Firecracker upgrade all
// snapshots go stale and apps lazily re-snapshot via cold boot. Therefore wake
// must NEVER depend on a snapshot existing — cold boot from rootfs always works
// and is the fallback for a missing, stale, or version-mismatched snapshot.

// Snapshot is the metadata for one parked deployment's snapshot (mirrors the
// `snapshots` table, spec §5). Paths point at the file-backed memory + vmstate on
// NVMe (spec §8).
type Snapshot struct {
	DeploymentID string
	FCVersion    string // the Firecracker version that made it; load only on a match
	MemPath      string // memory file
	VMStatePath  string // vmstate file
	// StorageKey (issue #96 / ADR-025 axis 2) is the canonical key the
	// mem blob lives under in the StorageBackend. Empty keeps the
	// MemPath-only workflow intact.
	StorageKey string
	MemBytes   int64
	Stale      bool // set true on FC upgrade or a failed restore
}

// Usable reports whether snap can be loaded by the given running Firecracker
// version: it must be non-nil, not stale, have both files, and match the version.
func (snap *Snapshot) Usable(currentFCVersion string) bool {
	if snap == nil || snap.Stale {
		return false
	}
	if snap.MemPath == "" || snap.VMStatePath == "" {
		return false
	}
	return snap.FCVersion == currentFCVersion
}

// WakeMethod is how an instance was (or will be) brought up.
type WakeMethod int

const (
	// WakeColdBoot boots from rootfs (the always-works path, ADR-005).
	WakeColdBoot WakeMethod = iota
	// WakeRestore loads a snapshot (the fast path, ~150–300 ms).
	WakeRestore
)

func (w WakeMethod) String() string {
	if w == WakeRestore {
		return "restore"
	}
	return "cold_boot"
}

// PlanWake picks the wake method: restore iff the snapshot is usable on the
// current Firecracker version, else cold boot. This is the WAKING branch of the
// state machine (spec §6.1).
func PlanWake(snap *Snapshot, currentFCVersion string) WakeMethod {
	if snap.Usable(currentFCVersion) {
		return WakeRestore
	}
	return WakeColdBoot
}

// RestoreSpec locates the snapshot files to load into a fresh netns and the
// images the restored VM still references (spec §4.4).
//
// Drive paths + Kernel are required because Park→Kill removes the entire
// chroot (it lives on tmpfs — see vmm.Kill). The snapshot itself records the
// chroot-relative basename of every backing file, so on Restore we must
// re-stage the kernel and the drives under the same basenames in the new
// chroot before loading the snapshot. Without this the snapshot's recorded
// drive path resolves to a file that no longer exists and Firecracker 400s
// with "Error manipulating the backing file: No such file or directory".
//
// VsockDevice is the same device BuildColdBootConfig attaches on the cold-boot
// path; the VMM PUTs /vsock after /snapshot/load and dials it to fire the
// guest's post-restore resume hook (spec §4.8, §11 V6, ADR-022). nil is
// tolerated (test seam) but production always sets it.
//
// #96 (ADR-025 axis 2) — the StorageBackend seam: StorageKey is the
// canonical key under sched.SnapshotMemKey / sched.SnapshotVMStateKey the
// VMM should pull from. When StorageKey is set, the VMM resolves the bytes
// to a local tmp file and writes the absolute path into MemPath /
// VMStatePath before staging. The two-field shape (MemPath + StorageKey)
// preserves the pre-#96 contract for one release — the migration slice
// (future) flips it to a single StorageKey field.
type RestoreSpec struct {
	MemPath     string
	VMStatePath string
	Tap         string
	KernelPath  string // /srv/fc/base/vmlinux-6.1.x — re-staged as basename in chroot
	BasePath    string // drive0 shared ro base rootfs
	LayerPath   string // drive1 per-app layer (overlay upper)
	VsockDevice *VsockDevice
	// StorageKey is the prefix-matched key under which the mem blob lives
	// (e.g. "snap/<deploymentID>/mem"). When set, Restore resolves it via
	// Storage.Get into a tmp file whose absolute path is written into
	// MemPath. The local driver on the production box maps "snap/" to
	// /srv/fc/snap and the resolution is essentially a stat; a future OCI
	// driver would actually stream the bytes. Empty keeps the legacy
	// MemPath workflow intact.
	StorageKey string
}

// SnapshotSpec is where to write a new snapshot's files (spec §4.4).
//
// StorageKey is the optional storage key the mem blob will be Put under.
// The VMM streams the tmp mem file into the storage backend and removes
// the tmp after a successful Put. MemPath is still required for the
// legacy in-place move; one-release deprecation window per ADR-025.
type SnapshotSpec struct {
	MemPath     string
	VMStatePath string
	// StorageKey (mem only) is the storage key the mem blob is published
	// under post-snapshot. Empty keeps the legacy MemPath workflow
	// intact.
	StorageKey string
}

// SnapshotInfo is the result of a snapshot create.
type SnapshotInfo struct {
	MemBytes     int64
	VMStateBytes int64
}
