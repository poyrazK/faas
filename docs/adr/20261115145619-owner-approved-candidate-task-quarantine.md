# ADR: Owner-approved candidate Task quarantine

Date: 2026-11-15
Status: Accepted

## Context

A recovered Task release can restore its web deployment while its candidate
workers continue acquiring leases. The existing CLI can quarantine known worker
IDs, but the agent restore approval grants no such authority. The Task database
and native recovery journal are separate databases; a client cannot claim they
commit atomically.

## Decision

Add a separate immutable quarantine qualification and owner verdict under the
settled recovery handover. It requires a retained native restore receipt, the
original checkpoint and worker IDs, the restored serving deployment, unchanged
app reviews and at least one compatible previous worker app. Its one-minute
deadline is fixed at qualification, and an exact digest binds owner approval.
Quarantine never renews the original executor claim or the restore approval.

A local agent tool accepts public intent, operation, handover and qualification
identities plus execution consent. Source, dependency and database-role checks,
worker startup claim-fence attestations, previous capacity, observer, restored
MCP endpoint and native access policy are checked by the fixed CLI. The CLI owns
the existing private journal and holds the Task release advisory lock around the
compatibility/admission checks, native admission and Task fence observation.

Native admission locks the journal, current owner/requester keys, app reviews,
VM identities, policy and observer heartbeat. It consumes authority once by
recording `applying` and a digest of the private attempt token. PostgreSQL checks
credential and qualification expiry against `clock_timestamp()` after lock waits.
Admission is a point-in-time authorization for one attempt; it does not lock the
external Task database or promise that unrelated native changes cannot follow it.
Revocation before admission blocks the attempt. Revocation after admission cannot
cancel SQL already in flight or undo fences, and cannot prevent historical
read-only reconciliation.

The CLI durably creates its private attempt file before admission. A failed or
lost admission reply never starts SQL. After confirmed admission, the existing
Task helper fences only the checkpoint's candidate worker IDs under the claim
transaction lock. Active leases are preserved; no Tasks, admission policies,
schema, web traffic or desired VM state are changed. Runtime heartbeats and
cleanup retain the fences according to the existing worker contract.

After SQL, the observer reads all recorded worker IDs under the same claim lock.
Only an observation that every ID is present and draining can become the native
`quarantined` result. That result binds the exact identities, evidence digest and
observation time. It is a historical fence observation, not a cross-database
commit receipt, shutdown completion or current-health verdict.

A repeated pending operation takes a read-only path and never repeats the SQL
attempt. Missing fences, missing private files or an unavailable database remain
uncertain and require owner reconciliation. A lost observation reply is recovered
from the native result. Completed results remain readable after expiry or owner
revocation without reading private files or changing either database.

## Consequences

Agents can stop recorded candidate workers from taking new Tasks after web
recovery, while active work and replacement capacity remain protected. New worker
IDs, changed generations, traffic, policy or authority before admission fail shut.
Worker retirement, lease cancellation, executor resumption and live staging
acceptance remain separate work. No unconditional parking fallback is introduced.
