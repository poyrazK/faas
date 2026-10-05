# ADR-619: Regional host incarnations and operation fences

Date: 2026-10-04
Status: Implementation in progress; activation and native teardown qualification pending.

## Context

A quota check can race a boot, resume or command already in flight. A daemon
restart can also forget a guest whose launch RPC failed. The cell's physical
permit and serving-seat receipts need a durable host counterpart before they
can be used operationally. A signed stop requires actual native resource
enumeration and teardown; a local operation count is insufficient.

## Decision

Retain a private, exclusively locked journal on persistent host storage. Bind
its node identity to an immutable host epoch and the Linux kernel boot ID.
Daemon restart on the same boot keeps the epoch; kernel reboot creates a new
epoch while retaining all older identities and records. An older boot ID cannot
reactivate a historical epoch. Refuse reparenting, corrupt or oversized records,
ambiguous JSON, symlinks, hard-linked records and non-private storage. Identity
and its epoch copy use atomic replacement with file and directory fsync; a
missing epoch copy can be recovered from the exact durable current identity.
Missing current identity with retained records fails closed.

Each physical attempt and serving seat retains all original ADR-618 pins. Fences
may arrive before registration, are irreversible, and preserve the first fence
on retry. Lifecycle RPCs currently address instance IDs, so a host epoch never
rebinds an instance to a different permit or run. A registered physical launch
cannot be replayed after a failed RPC or daemon restart. Its hold requires
native reconciliation; an empty Manager map never authorizes a second launch.

Register a fresh physical attempt before host resource allocation. Keep an
operation barrier through restore/cold-boot publication and failure cleanup,
through resume, warm-snapshot embedded resumes, and through app-task dispatch
and teardown. A durable fence blocks all subsequent begins and boundary checks.
Earlier operations remain counted until they return, including late boots that
must clean up before publication. Waits are cancellable; timeout never undoes
the fence. Closing the journal refuses to release ownership during an operation.
Replacing a serving seat also waits for all old seat operations to return.

Carry bounded canonical Placement JSON on the internal scheduler-to-vmmd
restore, cold-boot, migration and app-task transports. Placement is bookkeeping,
not execution authority, and never enters the guest AppSpec. The host requires
both its cell authorizer and its local journal. Pin instance, app, account,
deployment, node and epoch; require a seat for serving execution, and refuse
builder/execution mode spoofing. Paused warm restores have no serving seat and
cannot resume through the legacy instance-only path. Production checks the
placement's entire grant against its durable cell head; an independent app
cannot gain regional execution by presenting placement metadata.

The optional vmmd `regional_runtime_journal_dir` opens this journal only after
node registration and before lifecycle RPCs. Operators must provision private
persistent storage, never `/run`, tmpfs or an ephemeral VM disk. Empty config
rejects supplied regional pins. Opening the journal activates no quota head;
all current regional heads remain frozen.

This step emits no operational stop signatures. Barrier zero means only that
this daemon's operations have returned, including after restart. It does not
prove that paused, untracked, legacy or unreachable guests are gone, and a
best-effort Kill is insufficient. Native resource identity registration,
enumeration and verified teardown, signed compute transport/retirement
reconciliation, scheduler permit and seat issuance, fresh-seat warm promotion,
legacy inventory and quota activation remain required. The full feature also
retains explicit quota consent, global billing/metering and app orchestration,
and serving from all selected healthy eligible regions as the routing default.

## Validation

Exercise restart and kernel reboot identity, exclusive ownership, registration
replay, fence-before-register, full-pin conflicts, independent serving-seat
replacement, corrupt/private storage and uncertain fsync. Race a real Manager
mock boot with fencing and deliberately delayed cleanup; prove the operation
cannot disappear before cleanup returns. Verify resume/warm snapshot/error
recovery boundaries, paused restore rejection without a fresh seat, immutable
request copies and scheduler/gRPC metadata round trips. These are portable
checks only. Dedicated native Linux x86_64 KVM test-metal, recovery acceptance
and leakcheck remain required before host enforcement is qualified.
