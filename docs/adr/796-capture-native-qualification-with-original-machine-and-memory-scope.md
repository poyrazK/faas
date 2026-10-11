# ADR-796: Capture native qualification with the original machine and memory scope

Date: 2026-10-11
Status: Adapter implemented; dedicated native KVM acceptance pending.

## Context

The qualification Manager can record capture start and completion, and ADR-795
can publish three coherent files. The native backend still needs to pause and
capture its original Firecracker task and restore the temporary memory allowance
before resuming that task. Legacy paths and recovered journal ownership cannot
supply those capabilities.

## Decision

Enable the native adapter only for an original qualification warm capture. The
Manager retains its destruction-joined flight and incoming attempt lock, records
capture start before output creation, and chooses the immutable object namespace.
Require a complete image backend, pinned kernel cgroup support and protected disk
placement before capture effects. Use vmmd's existing disk TMPDIR on the same
filesystem as the retained private drive; do not add a host path to the RPC.

Stage distinct owned memory and VM-state outputs. Retain one original machine
session, physical journal lock, pidfd and executable identity through pause,
snapshot/create, freezing, resume and all uploads. Observe the actual guest shape
and Firecracker version before and after snapshot/create and before resume.
Freeze drive1 while paused, restore ordinary memory, recheck runtime admission,
then resume and publish the three held descriptors. Manager publishes the coupled
backing identity and is the only writer of durable capture completion.

Pin the original cgroup directory and kernel mount. Widen memory.max using the
existing limits table, only when its sole occupant is the original PID and its
current limit is ordinary. Restore through that same directory before resume and
on every exit, including cancellation. Refuse replaced scopes, changed occupants,
unexpected limits or overflow. An uncertain restoration forgets local producer
authority and revokes the physical owner; it grants neither resume nor resource
removal acknowledgement. The original capacity reservation remains held.

Incomplete captures cannot be repeated or deleted through legacy key cleanup.
After a daemon crash, the existing capture-start and physical journals preserve
uncertainty; a widened cgroup cannot pass ordinary runtime inventory. This
adapter does not enable generic native park capture, migration or regional
serving admission. Those require their own original authority and acceptance.

## Validation

Portable integration fixtures model original process, mount and cgroup identity
while using real source descriptors. They exercise pause/create/freeze/resume
ordering, Manager backing and completion, immutable replay, refused redirection,
failed production/publication, cancellation and memory quarantine. Linux checks
read host metadata and reject ordinary or redirected kernel roots and unsuitable
output placement. These checks cannot establish Firecracker or KVM acceptance.

The VM lifecycle change still requires dedicated native Linux x86_64 KVM metal,
leakcheck and regional fleet acceptance before rollout. The authorized internal
nested GCE node supplies diagnostic test and lint evidence only.
