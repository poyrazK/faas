# ADR-20261019120026: Operator recovery of promotion migrations

## Status

Implemented. Migration promotion admission remains default off pending dedicated
native x86_64 KVM qualification. Recovery of existing qualified work does not
require that admission switch to remain enabled. Native recovery qualification
is pending.

## Context

An uncertain migration result blocks new project migrations, even after database
inspection. Task terminal status alone cannot establish that the executing guest
has stopped. Operators need a durable, scoped recovery decision without inventing
a successful exit or replaying SQL.

## Decision

`projects environments recover-migration` accepts an inspected task/generation,
a stable request ID, an evidence SHA256 and explicit database-state attestation.
Resume additionally requires the operator to confirm that the expected expansion
is present and compatible. Resolve requires a different succeeded, health-verified
nonmigration promotion currently active in the same project and destination.
Arbitrary SQL and private evidence contents are not accepted.

Before a promotion migration VM is restored, schedd records its node, lease and
runtime UUID. The runtime UUID is derived from the task and restoring lease, so
an old restore cannot reuse the VM identity of a newer dispatch lease. The
running fence requires that registration; older dispatchers fail closed. Runtime
placement becomes immutable after command dispatch. Existing task kinds retain
their existing VM identities. A task from before this registration protocol cannot
be recovered through the new endpoint without a registered runtime.

Recovery requests are durable operational records with immutable identities and
at most one pending or completed resolution per promotion. Blocked attempts remain
in history and may be retried with a new request and current health generation.
The scheduler consumes teardown intents and calls the existing routed vmmd Destroy
operation for the exact recorded VM. Only a confirmed acknowledgement can advance
to verification. APID never calls vmmd. Terminal task status, a user attestation,
or an unreachable node does not substitute for that acknowledgement.

Resume repeats the original capture and destination runtime guards, records a
separate resumed decision, then starts a fresh preflight window. Baseline and
candidate probes still gate cutover and success. The original task result and
exit code remain unchanged. Publication accepts this explicit resolution only
with the captured runtime identity and teardown receipt.

Resolve runs fresh attributed critical probes against the verified repair graph,
then checks active graph/configuration and every member runtime fingerprint again
inside the project transaction. It leaves the original promotion failed and closes
its migration blockade. It never overwrites a newer destination intent.

Completed recovery decisions are immutable. Exact replay returns the original
receipt before checking newer intent; modified acknowledgements conflict. Runtime
and recovery evidence are excluded from environment cloning, and schema downgrade
refuses to discard them. Original task dispatch remains permanently fenced.

## Consequences

A lost migration result has a supported recovery path. Operators still inspect
SQL effects: HTTP health cannot prove arbitrary database semantics. Missing runtime
identity, an unreachable executor, changed runtime inputs, stale generations and
unverified repair graphs fail closed. Recovery is bounded by the existing ten-minute
verification deadline, and it can be observed through normal promotion status.
Schema changes are never automatically reversed.
