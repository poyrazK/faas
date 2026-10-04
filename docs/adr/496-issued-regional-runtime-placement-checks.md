# ADR-496: Issued regional runtime placement checks

Date: 2026-10-05
Status: Receipt enforcement implemented; issuance integration and activation pending.

## Context

Identity equality does not establish that the cell issued a physical attempt
or serving seat. A planned, fabricated, fenced or released receipt must not
reach host execution. Separate receipt reads could combine observations from
opposite sides of a concurrent fence or retirement.

## Decision

Require a coherent journal check through the mandatory state-store interface,
including decorators. Under the same app lock used by reservation, issuance,
fencing and signed quota retirement, validate the current complete grant,
retained signed grant receipt, current app/tenant binding, exact physical
permit and optional exact serving seat. Physical and serving receipts must
have issued markers, no fence, no release and no stop evidence. A serving
physical placement must carry a seat; paused warm, prime and task placement
may carry only their physical permit. Lifecycle purpose and plan remain
independently checked by ADR-495.

MemStore uses its journal mutex. PostgreSQL uses the existing app advisory
lock and READ COMMITTED transaction before reading quota, app and receipt
state through SQLC. A waiting check observes a committed fence; rollback
preserves the original issued placement. Missing or unissued bookkeeping
never falls back to a full regional plan. Storage errors remain errors.

The production cell authorizer performs this check before its existing task
lease/instance and frozen-admission checks. A successful receipt check is
bookkeeping only. It does not hold a database lock across a host operation,
activate quota admission or replace the host epoch and operation fences.
Issued holds remain charged when a check rejects execution. No elapsed lease,
failed RPC, paused guest or terminal instance row supplies stop proof.

Scheduler permit and seat issuance, signed compute admission, fresh-seat warm
promotion, native resource identity and recovery/teardown verification remain
required before activation. The end-to-end feature also retains explicit
quota consent, global billing, deployment orchestration and ingress serving
from all selected healthy eligible regions without a directory request-path
dependency. Native Linux x86_64 KVM recovery, test-metal and leakcheck remain
required qualification gates.

## Validation

Run shared memory/PostgreSQL contracts for missing, planned, partially issued,
fully issued, fenced and retired placements; immutable physical/seat pins;
paused warm versus serving seats; cancelled checks; and retained holds.
Exercise PostgreSQL journal-lock waits with committed and rolled-back physical
and seat fences. Verify the real vmmd authorizer refuses fabricated permits,
unissued seats and fenced seats while keeping even valid issued quota heads
frozen. Run affected scheduler/host regression checks, diff lint and policy
gates. Receipt tests are not native stop proof.
