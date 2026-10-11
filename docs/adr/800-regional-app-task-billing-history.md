# ADR-800: Retain regional app-task lease billing history

Status: accepted · 2026-10-11

## Context

ADR-799 retains ordinary instance billing intervals. App tasks use separate
durable task rows and lease attempts (ADR-781). Restoration can allocate a VM
before the command's running dispatch fence. Retry and sweep clear the original
lease, and app/deployment collection cascades task rows. Current task rows cannot
reconstruct original regional compute observations.

## Decision

Retain each committed restoring/running lease interval independently of task,
app and deployment rows. Preserve the original app, task, deployment, lease
token and scheduler owner. The scheduler owner is not a native node identity.
The first restoring claim starts the interval; running publication and renewal
do not reset it. Normal terminal acknowledgement before lease expiry closes its
reported billing interval. Retry retains that attempt before a new lease starts.
Lease expiry, bare requeue, replacement or deletion marks unresolved history;
later minutes must still expose it. Scheduler history does not prove native
absence, even when a normal billing interval is closed.

Capture in the task transaction under the original app quota journal lock, with
canonical two-app lock order on reparenting. Rollback leaves no history. Refuse
direct interval edits, deletion and truncation, and populated downgrade. Lock
task writers before installation and retain that boundary. Existing active tasks
at installation have explicitly incomplete starts; do not invent old coverage.
The portable store mirrors committed task writes under its mutex, including
the terminal intermediate state of a retry before its timestamps are cleared.

Permanent app purge removes task rows before crons. Deleting a cron first would
apply its `SET NULL` foreign key to an immutable terminal task and fail the
purge. Task interval history survives both deletions.

Read all original issued app-task capacity receipts for the exact original app,
deployment and lease. Validate the versioned task/lease native instance identity
from ADR-781. Retain full original permit and launch dimensions; never borrow
current app settings, account ownership, node configuration or quota policy.
Missing native source, task identity or dimensions remain explicit. Multiple
issued receipts remain ambiguous rather than selecting one as complete evidence.

## Integration boundary

This completes retained scheduler interval capture for ordinary instance and
app-task paths. Pages alone do not establish complete minute coverage. The
producer still needs sealing under the app journal lock, protection against late
history, original issuance/native-source reconciliation, min-instance floors,
signed retained delivery, daemon wiring and global accounting enforcement.
Regional execution fences remain in place until end-to-end requirements and
dedicated native KVM acceptance pass.
