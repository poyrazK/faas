# ADR-20261104001500: MCP Task key preflight and lease-bound writes

## Status

Accepted; extends ADR-426 and ADR-20261103204500.

## Context

Go/Python could start against a retained queue with unreadable payloads. Claims
committed before decrypting, consuming attempts and eventually failing work
without invoking its handler. All starters also allowed completion or client-input
state writes from an expired lease before another worker reclaimed it.

## Decision

Before starting web or worker writers, Go/Python authenticate all unexpired
payloads in the selected namespace with their configured current and retained
keys. Stream rows to bound memory and use the existing 10-second startup budget.
Check arguments, results, errors and input state. Sampling a v1 envelope cannot
prove coverage because different keys share its untagged format. Startup refuses
unreadable data, database failures or a scan exceeding its budget. Observers
remain read-only and do not need payload secrets. No schema change is required.

All starters authenticate claimed payloads before committing the claim. Failure
rolls back state, lease, attempt and fairness-row changes; a sequence may retain
harmless gaps. This protects against data or key changes after startup as well.

Require both live lease ownership and unexpired Task TTL for completion,
cancellation, retries and input requests. Terminal writes lock the Task row,
then capture the database clock used for their expiry checks. Input requests
check expiry when reading and again when parking the Task. Caller cancellation
of an unexpired Task remains an authorized intent update, independent of worker
lease ownership.

The CLI accepts the API's tagged JWT action envelope and legacy flat action
responses using one strict decoder shared by policy application and auditing.
Unexpected action kinds or fields remain rejected. Treat implicit and explicit
default expiration requirements equivalently without accepting a weaker setting.
Modern diagnostic resource reads mirror the resource URI in MCP-Name; starters
validate the decoded routing header against the body.

## Consequences

Large retained histories add database read work to startup and may need shorter
Task TTL or a faster database to meet the existing startup budget. Key or payload
failures preserve work for operator repair rather than consuming handler retries.
Retain keys across every writer during rotations. Expired worker writes do not
resurrect ownership or park Tasks after their lease is lost. Local PostgreSQL
regressions cover preflight, rollback, lease/TTL expiry and row-lock waits; local
HTTP regressions cover policy envelopes and modern resource routing. Live OAuth
and native KVM qualification remain separate release gates.
