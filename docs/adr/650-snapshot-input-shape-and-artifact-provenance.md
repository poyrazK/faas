# ADR-650: Snapshot input shape and artifact provenance

Date: 2026-10-07
Status: Implemented locally; native lifecycle acceptance pending.

## Context

Paused native load currently observes guest dimensions only after Firecracker
has allocated the snapshot's machine. Snapshot rows retain memory length but
not original CPU dimensions. An updated app plan cannot supply the original
shape. Region-prefixed snapshot keys also failed to derive the existing
backing-image sidecar, making those captures unrestorable through ADR-510.

## Decision

Extend the existing capture-specific backing sidecar with optional versioned
`SnapshotInputs`. The sidecar travels with the retained immutable memory key;
no current app policy or new snapshot row is used to reconstruct it. Regional
backing keys preserve their original region prefix, just like paired memory,
VM-state and private-drive keys. Existing cleanup already removes this sidecar.

For a complete storage-mediated v2 capture, the legacy producer observes the
actual paused Firecracker instance, version, RAM, vCPU and SMT configuration.
It repeats the instance/state observation around the config read and checks
the same shape/version again after creation and input hashing, before early
warm resume or publication. The input record names the exact three artifact
keys, lengths and SHA-256 digests. Memory length must equal measured guest RAM.
Manager preserves this original record with the images remembered at boot,
including across terminal park teardown. Legacy host-path captures retain
their explicit unknown input shape. Native capture remains gated: this metadata
does not supply transient capture capacity, output producer or pause authority.

Native Restore independently fetches a bounded canonical sidecar before chroot
staging or task creation, compares its original RAM/vCPU against retained native
resources, and requires its producer version and paired VM-state storage key.
Historical regional records without original CPU/resource provenance and snapshots
without input provenance refuse. The selected request shape is also compared when
available. Missing/incompatible snapshots
keep the existing serving cold-boot fallback; paused warm reservations refuse.

Before load, memory, VM-state and original captured private-drive source are
hashed through pinned descriptors. Length, device/inode, mode, modification and
change timestamps are checked before/after hashing and around synchronous load.
Atime is excluded because reading changes it. Symlinks and changing/replaced
files refuse. Host-owned resolver/environment preparation intentionally modifies
the private clone; it must not rewrite or be mistaken for the captured source.
The original dimensions are checked again under the native task/journal pin
before the load request. Actual paused loaded shape must match the same record
before resume. Existing entropy, clock, readiness and teardown paths remain.

Canonical bounded decoding rejects duplicate/unknown fields, alternate casing,
null substitutions, malformed dimensions/digests, mismatched artifact keys and
trailing bytes. Old sidecars without inputs remain readable as legacy identity;
they cannot authorize native snapshot load. The optional field preserves old
capture-journal representations when absent.

Whole-file integrity verification currently adds a read of the snapshot on
capture and restore. It is not a measured native latency result; storage-level
verified immutable descriptor reuse and native p95 qualification remain needed.
External root mutation and natural task exit cannot be frozen by an observation.
This record is input provenance, not admission, original run/task authority,
capture permission, migration proof or a physical-absence acknowledgement.
Regional heads stay frozen. Full inventory/capacity reconciliation, native
capture/migration, shared authorization/billing, independently provisioned regional
stores/replication, cached ingress for all selected healthy eligible regions and
dedicated native x86_64 KVM acceptance remain in scope.

## Validation

Portable fixtures cover region-preserving sidecar transport, original resource
and version comparisons, historical provenance refusal, strict decoding,
completed file binding, cancellation, corruption, symlinks and mutation/replacement
around the consumer. Native machine fixtures retain before-load original-shape
and post-load/paused shape checks. These fixtures and nested-node diagnostics do
not qualify native snapshot execution, latency, leakcheck or fleet recovery.
