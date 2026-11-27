# ADR 20261114010000 — Native recovery for Run template capture

Status: implemented; bounded internal-node functional acceptance passed.
Release enablement still requires the native host, provenance and reader gates.

## Decision

Run's payload-free template producer reuses the original native runtime capture
protocol from ADR 20261022184951. With `native_process_recovery`, it requires
`native_launch_owner_records`, `native_runtime_capture` and the private native
snapshot root. The explicit schedd capture switch and dedicated platform
account remain required. Defaults and remote-backend gates are unchanged.

A runtime capture authority with nested version 2 identifies only a fresh,
networkless Scale template under `snap/<nil-uuid>/captures/<uuid>/v2/`.
Version-1 ordinary capture authorities continue to require a non-nil deployment
through runtime admission. Only Manager's private template instance kind can
construct the new request. The publication-intent envelope remains version 2;
qualification capture and protocol-2 admitted capture gain no authority.

The native producer owns durable start, persistent output descriptors, pinned
pause/create/resume, frozen drive, exclusive publication and all four receipts.
Manager passes no alternate Storage backend, performs no legacy resume and
writes no second backing sidecar. Complete captures still require physical
VM retirement before the RPC returns and the immutable catalog insert occurs.

## Flight ownership and recovery

Before Wake, vmmd persists a private flight under its native journal's
`runtime-captures/templates/`. It binds the fresh template instance and capture
key to the kernel boot and exact daemon PID/start-time owner. Native launch
preparation requires the original private flight context for that UUID; an
ordinary Wake cannot borrow the reservation.

Startup native recovery first inventories ownership, then examines these
flights before opening admission or allocating fresh leases. It proves the
original daemon exited and matches any physical version-3 owner to the flight.
Manager retires the original lease through the existing native cleanup path,
requires exit and resource acknowledgements, and rescans reservations. Recovered
templates never enter the live map, become ready, or replay capture effects.
A flight without a physical record represents a pre-launch interruption only;
the native inventory must already have proved there are no unowned effects.
Malformed, replaced, foreign-owner and still-live-owner flights fail closed.

Successful cleanup removes only the exact flight record. Incomplete runtime
starts and publication intents remain audit evidence under the existing
native protocol; they are never completed or promoted during recovery.
Snapshot objects remain under catalog-aware imaged retention, since publication
acknowledgements may be uncertain. Ready and retired catalog rows protect them;
old unpublished captures are reclaimed through vmmd's existing private-object
retirement RPC. No recovered inventory grants artifact deletion authority.

## Networkless native device setup

Native networkless guests use version-2 device scopes under the existing
journaled helper protocol. The original task pidfd, private mount namespace and
jail root are pinned before setup; this also waits for the jailer to join its
cgroup before the pre-boot fence. The fourth inherited descriptor repeats the
pinned root. It conveys no host TUN device. The one-shot helper creates a fresh
private `/dev`, provisions KVM, proves KVM API 12 under the exact jail UID/GID,
and requires `/dev/net/tun` to be absent. Its durable receipt requires no TUN
mount or access. Ordinary networked scopes retain their existing shape and
TUN requirements. Scope kind must match the original physical lease.

Version-2 scopes require `native_launch_owner_records`. Without that gate,
native networkless device preparation fails closed. Cleanup uses the original
helper namespace and input-retirement proofs; it gains no unjournaled helper
or namespace-entry path. Every native Run wake in this cohort must attest its
original KVM-only receipt, including cold fallback and restored clones.

## Concurrent native image inventory

An image anchor can appear after the initial journal directory snapshot because
another original source producer has durably published its epoch meanwhile.
Inventory loads the exact canonical epoch record for that anchor, validates its
boot, inode, filename and ownership, and includes it in the inventory. Missing,
ambiguous, malformed and conflicting live-inode epochs still fail closed. This
read correction grants no new staging, replay or cleanup authority.

## Fresh restores after daemon restart

The scheduler waits for a cached vmmd channel to reconnect before sending a
fresh payload-free restore, within the caller's existing deadline. A caller
without a deadline receives the existing wire default. This is connection
readiness, not application retry: an RPC returning uncertainty after server
admission is returned to the coordinator and is never replayed here. Real gRPC
regressions require one delivery after reconnect, no delivery after expiry and
one delivery for a server-reported uncertain result. Idempotent stop delivery also waits for the successor channel within the
existing cleanup deadline. A session joins concurrent stops and caches success
only after the transport confirms retirement; failed stops remain retryable.
Private restore diagnostics
retain preparation causes before the bounded public failure is produced.

## Compatibility and rollout

Deploy compatible native journal readers before enabling the writer. Older
readers reject the new nested runtime authority and device scope versions; rollback with retained
incomplete version-2 template records is a separate operator gate. No wire RPC,
database column, public Run contract or default recovery setting changes here.
The version-7 adoption lane remains a separate profile.

## Acceptance

The `native-recovery` cohort uses isolated schemas, fixture image admission,
a private mount namespace and one private jail tmpfs per fixture. It retains
that jail across real daemon SIGKILL/restart and leaves the host journal alone.
Metal-only, root-owned checkpoint barriers are absent from production binaries.
They stop at durable flight, pristine ACK, pause, first object receipt and full
capture boundaries so the harness can kill the actual vmmd deterministically.

The cohort requires all five capture crashes, schedd death during capture,
committed publication survival, vmmd/schedd restart during customer execution,
receipt replay, cold fallback, GC and usage settlement. It repeats the complete
template/clone and warm/OOM matrices with native recovery and version-3 launch
owners. Every scenario and supporting ownership regression must run and pass;
skips cannot produce evidence. Physical leakchecks run before, within and
after the VM journeys.

The selected internal node provides bounded real-KVM functional evidence. Its
Google virtualization does not satisfy the non-virtualized native release gate.
Approved image provenance, other profiles, host reboot, cross-node recovery,
older-reader rollback and full billing reconciliation remain separate gates.
