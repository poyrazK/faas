# ADR-780: Observe retained original host helper tasks

Date: 2026-10-07
Status: Implemented locally; native lifecycle acceptance pending.

## Context

Recovery inventories helper cgroups and their immutable current/archived owner
frames. A retained original helper leader can exec an unrelated command or move
out of that group. Its ordinary UID and argv then escape both the managed jail
UID scan and recognizable native launch scan. A missing/empty helper cgroup
cannot by itself establish that the original recorded helper stopped.

## Decision

Independently observe every authorized helper leader named by the original
current or archived frame. Read PID/start time directly, without command-name,
UID-range or cgroup-name filters. Only the exact original start time matches;
a reused numerical PID supplies no task for the old frame. Check UID tuple and
actual unified-cgroup membership twice around the incarnation read, then repeat
the complete helper task scan and process-root identity check. Missing or
changing evidence refuses the sample; a fully disappeared original task is
omitted from the observation, without producing a retirement acknowledgement.

Fresh runtime inventory includes an optional copied helper-task observation.
Every retained helper task stays unresolved, including one still in its expected
group. An escaped, UID-changed or already-acknowledged original task remains
visible. Such a task invalidates a matching owner's advisory hold match. Schedd
independently compares helper instance/generation at the receiver. Omission
preserves old canonical inventory representations.

Before and after the synchronous consumer, vmmd rereads current/archived helper
frames and tasks under the active exclusive inventory permit. Native producers
are drained; existing machine sessions retain their owner locks, so the helper
recheck does not reacquire those same locks. Frame additions/changes, task exit,
incarnation, UID, cgroup and process-root changes invalidate the sample.

Startup refuses an acknowledged helper frame that still has its original task.
VM resource publication also requires an independent helper task observer when
authorized helper frames exist, and verifies their original leaders are absent
under the original owner lock before publication. This prevents a vanished
helper cgroup from acknowledging an escaped recorded leader. No signal or kill
authority is added by observing it. Unresolved resources remain retained.

This closes the recorded leader gap. It does not discover unrecorded historical
forks or escaped descendants, prove original guest/run ownership, deduplicate
rows/holds, release capacity or activate regions. Natural exits and external
root mutation cannot be frozen. Dedicated native x86_64 KVM metal/leakcheck and
fleet recovery acceptance remain required; the internal nested GCE node supplies
kernel diagnostics only. Complete physical inventory, original source/run/task
row binding, first-fence capacity reconciliation, native snapshot capture/pause/
migration and latency qualification, shared global authorization/billing,
independent regional stores/provisioning/replication, and cached ingress serving
every selected healthy eligible region remain in the full scope.

## Validation

Portable fixtures cover renamed/missing argv, escaped groups and changed UIDs,
retirement claims, reused PIDs, incomplete/duplicate evidence, cancellation,
process-root replacement, frame/task changes around consumers, copied payloads,
resource-publication refusal and receiver-side advisory matching. Linux checks
observe a real original PID through procfs regardless of the test binary's argv.
They do not qualify Firecracker execution, native cgroup cleanup or KVM recovery.
