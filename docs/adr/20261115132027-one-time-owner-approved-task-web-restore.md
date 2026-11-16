# ADR: One-time owner-approved Task web restoration

Date: 2026-11-15
Status: Accepted

## Context

Task recovery handovers fence the original executor and require a normal exit
acknowledgement before qualification. Owners can separately approve one exact
restore target and evidence digest for a fixed deadline. A client-side approval
check followed by an ordinary traffic request would leave a race: authority
could change before the write, or a lost reply could trigger a second restore.

## Decision

Add a local-only agent restore tool whose inputs are the original intent,
operation, handover and qualification identities plus explicit execution consent.
The trusted CLI resolves the immutable private profile and claim locally. Agent
inputs and receipts never contain the claim, database URLs or credential values.
The original release authorization remains fenced and is never renewed or reused
as recovery authority. Recovery requires its own current owner verdict.

The CLI holds the existing Task release advisory lock across retained handler
compatibility/admission checks and a fixed native restore hook. The hook rechecks
sources and dependencies, separated database roles, Task worker identities and
readiness, previous workers, observer and the exact captured web artifact's MCP
catalog/role/readiness and access audit. It captures a separate native VM instance
snapshot. Task worker UUIDs are not VM instance UUIDs. The hook submits one native
request; uncertain replies cause a journal read, never an automatic POST retry.

A dedicated native route delegates through the existing traffic handler, retaining
release approval, lifecycle, canary, binding, MCP policy/readiness and exact
serving-deployment CAS gates. The actual traffic transaction locks the recovery
journal before traffic locks. It then validates the requesting key, current
non-agent owner authority, fixed approval digest and deadline, native app snapshots,
VM generations/instance identities, target residency and observer heartbeat.
PostgreSQL checks deadline, credential expiry and heartbeat freshness again at
receipt persistence using clock_timestamp(), so lock waits cannot preserve an
expired transaction-start timestamp. The guarded traffic change and approval
consumption commit or roll back together. Memory mirrors this under one mutex.

The native receiver requires a store advertising atomic recovery support. A
successful delegated HTTP response without a durable commit receipt is refused.
The receipt binds the exact operation, approval, checkpoint, old/new deployments
and fresh evidence. Concurrent requests cannot consume approval twice. Exact
repeats return that historical receipt even after expiry or revocation and do not
change newer traffic. Clients must inspect current observations separately; a
traffic-commit receipt does not certify runtime health.

## Consequences

This milestone restores only the captured web release. It does not run migrations,
change Task admission, resume the original executor, quarantine or retire workers.
Legacy claims and missing/abnormal executor exit acknowledgements remain blocked.
Owner qualification remains an attestation of local Task/database checks; the
server independently fences native facts. Every restore binds a native access-policy
fingerprint. Open endpoints must have no covering JWT rule and retain the same
fingerprint across readiness probes. Protected restores require a native
access-policy audit expectation. No live staging or production acceptance is part
of this implementation.
