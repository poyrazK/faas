package fcvm

// Snapshot park/wake (ADR-005). A snapshot is a *cache*, never the truth:
// Firecracker only guarantees a snapshot loads on the exact version that made it,
// so every snapshot is pinned to its `fc_version`. On a Firecracker upgrade all
// snapshots go stale and apps lazily re-snapshot via cold boot. Therefore wake
// must NEVER depend on a snapshot existing — cold boot from rootfs always works
// and is the fallback for a missing, stale, or version-mismatched snapshot.

// Snapshot is the metadata for one parked deployment's snapshot (mirrors the
// `snapshots` table, spec §5). StorageKey is the canonical key the mem and
// vmstate blobs live under in the StorageBackend; vmmd resolves it to local
// staging paths before loading. After #96 slice 3, this struct no longer
// carries per-instance local paths — those are vmmd-internal concerns
// populated from Storage.Get and never leave the daemon.
type Snapshot struct {
	DeploymentID string
	FCVersion    string // the Firecracker version that made it; load only on a match
	VMStatePath  string // vmstate file (caller-supplied legacy field; survives until #91 follow-up)
	// StorageKey (issue #96 / ADR-025 axis 2) is the canonical key the
	// mem blob lives under in the StorageBackend. Required since #96
	// slice 3 (F-1 contract on CreateSnapshot).
	StorageKey string
	// VMStateStorageKey (issue #121 / ADR-025 axis 2 slice 4) is the
	// canonical key the vmstate blob lives under when the new
	// StorageBackend carrier is used. The legacy single-box / default
	// path uses VMStatePath instead (a vmmd-internal host file); the
	// remote path populates this instead and leaves VMStatePath empty.
	// Mutually inclusive — both may be set when the caller populated
	// both proto fields — but VMStateStorageKey is the authoritative
	// locator when non-empty (Usable() honours both, Manager picks
	// accordingly).
	VMStateStorageKey string
	MemBytes          int64
	Stale             bool // set true on FC upgrade or a failed restore
}

// Usable reports whether snap can be loaded by the given running Firecracker
// version: it must be non-nil, not stale, have a mem storage key + at least
// one vmstate locator (path OR storage key), and match the version.
// ADR-005 + #121: cold-boot fallback always works, so a snap with no
// vmstate locator at all is not "usable" but also not a fault here —
// Manager.Wake treats it as cold-boot.
func (snap *Snapshot) Usable(currentFCVersion string) bool {
	if snap == nil || snap.Stale {
		return false
	}
	// Mem F-1 contract: StorageKey required since #96 slice 3.
	if snap.StorageKey == "" {
		return false
	}
	// Vmstate via either locator (legacy host path or new
	// StorageBackend key). Default-local single-box keeps the host
	// path branch bit-for-bit; remote nodes use the storage key.
	if snap.VMStateStorageKey == "" && snap.VMStatePath == "" {
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
// #96 (ADR-025 axis 2) — StorageKey is the canonical key the mem blob
// lives under in the StorageBackend. The VMM resolves it via Storage.Get
// into a tmp file whose absolute path is then used as the FC restore
// source. The local driver on the production box maps "snap/" to
// /srv/fc/snap and the resolution is essentially a stat; the OCI driver
// streams the bytes over HTTP.
//
// #121 (ADR-025 axis 2 slice 4) — VMStateStorageKey is the canonical key
// the vmstate blob lives under when the new StorageBackend carrier is
// used. When non-empty, the VMM resolves it via Storage.Get into a tmp
// file used as the FC restore source for vmstate specifically, leaving
// the legacy VMStatePath branch untouched. Default-local single-box
// always sends the empty value so the host-path behaviour is preserved.
type RestoreSpec struct {
	VMStatePath string
	Tap         string
	KernelPath  string // /srv/fc/base/vmlinux-6.1.x — re-staged as basename in chroot
	BasePath    string // drive0 shared ro base rootfs
	LayerPath   string // drive1 per-app layer (overlay upper)
	VsockDevice *VsockDevice
	// StorageKey is the prefix-matched key under which the mem blob lives
	// (e.g. "snap/<deploymentID>/mem"). Restore resolves it via
	// Storage.Get into a tmp file used as the FC restore source.
	StorageKey string
	// VMStateStorageKey (issue #121 / ADR-025 axis 2 slice 4) is the
	// prefix-matched key under which the vmstate blob lives
	// (e.g. "snap/<deploymentID>/vmstate"). When non-empty, Restore
	// resolves it via Storage.Get into a tmp file used as the FC
	// restore source for vmstate specifically. When empty, Restore
	// falls back to VMStatePath (default-local behaviour).
	VMStateStorageKey string
}

// SnapshotSpec is where to write a new snapshot's files (spec §4.4).
//
// StorageKey is the canonical key the mem blob will be Put under. The
// VMM allocates StageMemPath under os.TempDir(), stages the FC-paused
// mem file there, streams the file into the configured StorageBackend
// at StorageKey, and removes the tmp after a successful Put.
// StageMemPath is therefore vmmd-internal and never round-trips across
// the wire (after #96 slice 3 removed the mem_path proto field).
//
// #121 (ADR-025 axis 2 slice 4) — VMStateStorageKey is the canonical key
// the vmstate blob will be Put under, mirroring StorageKey for mem.
// When non-empty, the VMM streams the FC-paused vmstate file into the
// configured StorageBackend at VMStateStorageKey and the host VMStatePath
// is left unused (logged as metadata only). When empty, the VMM keeps
// the legacy moveOut(VMStatePath) behaviour bit-for-bit.
type SnapshotSpec struct {
	StageMemPath string // vmmd-allocated; never caller-supplied post-#96 slice 3
	VMStatePath  string // host location vmmd hands to the FC socket during pause
	// StorageKey (mem only) is the storage key the mem blob is published
	// under post-snapshot.
	StorageKey string
	// VMStateStorageKey (issue #121 / ADR-025 axis 2 slice 4) is the
	// storage key the vmstate blob is published under post-snapshot.
	// Semantically optional: empty ⇒ the VMM keeps the legacy
	// VMStatePath host-file path (single-box behaviour, unchanged);
	// non-empty ⇒ the VMM publishes via Storage.Put at this key.
	VMStateStorageKey string
}

// SnapshotInfo is the result of a snapshot create.
type SnapshotInfo struct {
	MemBytes     int64
	VMStateBytes int64
}
