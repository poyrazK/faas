# ADR-943: Exclusive recovery checkpoint ownership

Date: 2026-10-11
Status: Accepted; production qualification pending
Related: ADR-942

## Context

Atomic replacement prevents partial checkpoint files, but concurrent watches
can replace the same file with competing observations. Advisory ownership is
needed across the entire watch rather than only during each rename.

## Decision

Use the existing gofrs/flock library to acquire a nonblocking exclusive lock
on the canonical checkpoint path plus .lock. Resolve directory aliases before
selecting that path. Acquire ownership before authentication, loading saved
status and polling; release it on every ordinary CLI return. Operating-system
locks are released when the process exits or crashes. Contention returns a
clear error immediately, without polling or checkpoint replacement.

Keep the sidecar inode separate from the atomically replaced checkpoint.
Create it privately with mode 0600 and retain it after release. Never unlink
it during cleanup: removing it could let another process lock a new inode
while the original owner still holds the old one. An existing sidecar does
not mean ownership is held; only the OS lock does. Reject nonregular and
symlink sidecars, and nonprivate permissions on non-Windows systems.

Reject sidecar aliases of the checkpoint, plan, apply result, supplied
resume/refresh status and stdout. Record the acquired sidecar identity and
recheck ownership/identity before saving and before replacement. If it changes,
stop with a checkpoint error. These checks cover ordinary filesystem mistakes;
hostile replacement races and writers bypassing advisory locks are outside
this contract. Use a filesystem that supports the library's OS locking and
atomic rename behavior; this is not a distributed ownership lease.

The lock remains held through final partial saving and JSON/error output.
Checkpoint data format, resume binding, bounds, unknown-outcome semantics and
read-only delivery behavior are unchanged. CLI invocations without a checkpoint
remain independent and acquire no lock. SDK progress callbacks are unchanged.

## Consequences

Cooperating Gregale watches cannot overwrite the same canonical checkpoint
concurrently. A crash does not require manual removal of a stale sidecar.
Lock-acquisition failures are distinguished from contention. Live concurrent,
crash and filesystem qualification remains pending. No local tests are added
or run, and compliance registries remain unchanged.
