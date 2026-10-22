# ADR-993: durable managed PostgreSQL cutover member retirement

Status: accepted for internal controller plumbing; customer activation disabled.

## Context

ADR-466 prevents new admission, and ADR-469/470 retain physical and scheduler
reservations until teardown completes. Neither records which original VM a
cutover retired. Generic Destroy, an absent inventory entry, lease expiry and
terminal instance state cannot substitute for native physical evidence.

## Decision

Only schedd may explicitly select an instance for an already fenced cutover.
Selection persists an immutable member frame: cutover, fence timestamp, app,
deployment, instance, compute node, wake ID, app owner and ownership revision.
The app's scaling-policy revision is a conservative epoch: any revision change
invalidates the old authority, including ownership transfer. Selection is
idempotent for this exact frame. Prepare and Verify never select members.

schedd routes a dedicated retirement RPC to the recorded instance node. vmmd
requires its configured local identity, matching correlation metadata, native
process recovery and authoritative PostgreSQL access. The database guard locks
the app/cutover, member and original instance through native retirement. Owner,
fence, cancellation and incarnation changes cannot pass this boundary during a
stop. Unsupported hosts and missing journal ownership fail closed; there is no
generic Destroy fallback.

Manager joins boot/restore/capture under a teardown reservation, verifies the
original journal generation, retires it, then checks that the same kernel boot,
generation and physical lease record confirms revocation, process exit and
resource removal. The reservation remains held through this final check. A
failed cleanup retains host ownership. An immutable native terminal record lets
the original member retry after a lost reply or Manager restart. Missing records
or a different kernel incarnation cannot mint physical evidence. A daemon crash
before cleanup finishes can still leave unknown host-helper ownership; recovery
retains the reservation until that provenance can be confirmed. It does not infer
helper absence from the Firecracker process exit.

schedd atomically commits the native receipt, STOPPED lifecycle state and host-port
release against the still-current frame, then releases its local RAM/CPU ledger.
A failed or stale write retains them. Frames have no parent foreign keys, so
account deletion/decommissioning cannot erase evidence or be blocked by it;
missing authority prevents further stops. Receipts survive instance-row retention.
Bounded cursor recovery retries existing pending intents only, advances beyond
failed members and returns on a later pass. It does not discover or automatically
retire other instances, install fences, publish bindings or resume workloads.

## Limits and next steps

This is a **member retirement receipt**, not proof that all app/database writers
stopped. A future cutover controller must enumerate every workload and scope,
account for late dispatch and catalog gaps, converge routes, fence external SQL
sessions, and require fresh target verification before atomic binding publication.
Parked/no-dispatch instances and migrated-instance lineage need their own explicit
evidence; this slice refuses unsupported lifecycle states and migrated members.
Live private qualification executions retain their separate cleanup authority.
It does not discard source writes made after the restore point or define rollback.

Portable native-journal tests use fixture process/network observations. PostgreSQL
tests exercise ownership, cancellation and incarnation locking and atomic receipt
writes; gRPC tests reject unsupported hosts and substituted/incomplete responses.
Native x86_64 Linux KVM `test-metal` and `leakcheck` remain required before customer
activation. A Mac, nested GCE node, tagged compilation and fake observations cannot
supply that qualification. Production provisioning remains disabled.
