# ADR-655: Retain host helper private sessions

Date: 2026-10-07
Status: Implemented locally; dedicated native acceptance pending.

## Context

ADR-654 associates orphan helpers with their original private session. A child
can call `setsid`, escape its original cgroup and outlive every recorded ancestor.
Observation then cannot reconstruct that original relationship. New producers
need an inherited affiliation constraint before the execution gate opens.

## Decision

New command and device helpers use explicit release-matched session modes. After
fork creates the private session, the trusted helper sets no-new-privileges and
installs a seccomp filter with thread synchronization. Native x86_64 `setsid`
returns EPERM; other architectures, x32 and historical compat syscall aliases
are refused. Native process-group changes, fork, exec, namespace and mount
operations remain available. Fork/exec descendants inherit the original policy.
This is an affiliation constraint for trusted host utilities, not a general
sandbox against arbitrary root code or external host writers.

The child acknowledges the exact policy version over a separate inherited pipe,
then waits for the existing execution gate. Command readiness uses FD 4; device
readiness uses FD 8 after its four unchanged input descriptors. The parent bounds
acknowledgement to five seconds and the caller's cancellation/deadline, rejects
missing, extra or changed bytes, and independently checks the original session,
PID/start time and every stable thread's no-new-privileges/seccomp state. Only
then does it durably publish policy version 1 and open the gate. A failed kernel
installation, synchronization or observation cannot execute the command.

The original frame retains session policy provenance. Old shapes remain readable
without backfill, including ADR-654 frames with only a session ID. Such authorized
frames remain unresolved and cannot acknowledge family absence, release parent
resources or permit replacement. Kernel status alone cannot upgrade history.
Legacy helper command modes remain available to old release callers; new journals
refuse them. Older binaries reject the new field, so rollback requires compatible
binaries or a drained ownership journal.

## Consequences and validation

Private-session observation can now find newly constrained descendants after
ancestry/cgroup changes. This does not reconstruct unrecorded historical sources,
authorize session-wide signals or prove complete physical native inventory.
Original source/run/task row binding and first-fence capacity reconciliation
remain necessary. Regional admission remains frozen. Snapshot capture, pause,
migration and immutable-input latency qualification, shared global authorization
and billing, independent regional provisioning/stores/replication, cached ingress
serving every selected healthy eligible region, and dedicated native Linux
x86_64 KVM metal/leakcheck/fleet recovery acceptance remain in the full scope.

Portable contracts cover negotiation, cancellation, publication order, kernel
status failures and historical provenance. Linux diagnostics compare an
unrestricted control with synchronized existing threads and fork/exec descendants,
and reject native/compat session escapes. They create no guest or cgroup and do
not qualify native lifecycle or host utility compatibility.

The [Linux seccomp interface](https://man7.org/linux/man-pages/man2/seccomp.2.html)
defines synchronization, inheritance and ABI checks. The
[session interface](https://man7.org/linux/man-pages/man2/setsid.2.html) defines
fork/exec session inheritance and session creation. Native utility and migration
latency acceptance must still establish operational compatibility.
