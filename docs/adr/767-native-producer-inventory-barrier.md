# ADR-767: Coordinate native inventory with complete producer operations

Date: 2026-10-06
Status: Implementation in progress; independent observation and native acceptance pending.

## Context

Regional reconciliation must not mistake a gap between ownership preparation,
launch, and cleanup for the absence of a physical attempt. Per-instance journal
locks protect individual transitions but leave those gaps open. Legacy launches,
builders, host helpers, resource staging, asynchronous CPU changes, and daemon
mount sweeps also need to participate before a native inventory can be trusted.

## Decision

Each JailerVMM operation takes a shared producer permit for its entire lifecycle
call, including rollback and allocator release. Native inventory takes an
exclusive permit. Manager startup recovery retains that exclusive permit through
resource discovery, regional recovery checks, and allocator quarantine. This
startup gate remains a one-time initializer; its cached completion is not a fresh
regional inventory or a capacity reconciliation receipt.

The canonical protected chroot root contains private close-on-exec flock files.
An exclusive turnstile prevents new outer producers while an inventory waits for
existing producers to drain, including other VMM objects and processes using the
same root. Native ownership mode is initialized under the exclusive permit;
legacy producers cannot bypass an existing native journal. A closed or lost daemon
ownership descriptor refuses further effects and cannot be reacquired as original
local producer authority. Unsupported platforms refuse the
native filesystem lock capability.

Nested synchronous calls inherit a private, active context permit and acquire
references to the same lock family. Existing producers can finish nested cleanup
while inventory waits. The root lock outlives every nested reference; returning
from a parent invalidates that parent's context immediately. Inventory cannot
upgrade an active producer, and its context cannot authorize a mutation. Failed
lock release poisons that barrier and prevents subsequent effects or inventory.

Detached CPU and builder halt callbacks acquire fresh permits when they run and
recheck their original tail or process record after waiting. Daemon health
observers do not retain a completed wake permit. Parent mount periodic and
shutdown sweeps, and startup legacy jail/clone reapers, use a Manager maintenance
hook; it is an in-process vmmd callback with no new RPC surface.

This barrier coordinates platform producers. It cannot stop kernel events,
natural guest/process exits, escaped helper work from an earlier daemon, or
unrelated external mutation. Independent native shape/resource observation,
first-fence reconciliation, current run/task ownership, and safe row deduplication
remain required. All regional capacity heads stay frozen. Capture, pause,
migration, global directory authority and billing, independently provisioned
regional stores, and routing from all selected eligible healthy regions remain
part of the complete multi-region feature. Dedicated native Linux x86_64 KVM
metal, leakcheck, and recovery acceptance remain required.

## Validation

Exercise pending inventory with nested rollback, parent/child lifetime, stale
contexts, cross-object filesystem exclusion, cancellation, ownership-mode
transition, failed descriptor release, whole wake rollback, producer surface
coverage, and detached CPU callback replacement. Run affected portable race
suites and pinned lint/policy gates. Portable fake kernel resource backends do
not establish native lifecycle acceptance.
