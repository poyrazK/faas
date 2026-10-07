# ADR-608: Direct private artifact uploads for workflow Operations

Status: implemented locally in closed admission; native qualification pending.

## Context

ADR-606/607 remove application bucket writers from Job and ordinary HTTP exports.
Workflow Operations already retain verified private files across approved resumes,
but their final action still requires a managed source and provider writer. The
same direct upload contract can use the workflow ledger without changing its
execution, recovery or publication semantics.

## Decision

Add binary upload and JSON receipt-lookup routes under
`/v1/runtime/workflow-operations/{id}`. Both require the Operations workload JWT
and the current native run, final step, generation, attempt and capability. Account
and customer tokens cannot substitute for this proof. Intermediate actions cannot
prepare result files. The descriptor contains a stable report ID, filename, exact
size and SHA-256; no source URI, storage key or provider credential is accepted.
Existing managed-source routes remain available.

Derive the opaque `operation://<operation>/artifacts/<artifact>` reference from
the existing operation/run/step/report identity, independently of generation and
attempt. This preserves the workflow ledger's artifact IDs. The report namespace
binds the complete declaration; changed declarations conflict. An approved resume
can rebind an existing verified receipt to its current attempt without transferring
bytes, consuming another report or emitting another event. Receipt lookup requires
live native authority, including after cancellation; it does not authorize recovery.

Each transfer reserves a unique private staging object under existing captured
file limits and current account storage quotas. Shared account/node spool budgets
and the receive timeout bound I/O. Verify exact byte count and digest before storage
writes. Recheck current ownership, instance, native attempt, cancellation, fixed
deadline and lease after receiving bytes and when committing the receipt. PostgreSQL
rechecks authority and staging expiry after blob lock waits. Concurrent transfers
converge on one retained receipt; failed and losing copies use existing durable
cleanup. No schema or SQL changes are required.

Retaining a file never confirms an action. It remains private until the final step
is confirmed successful or existing explicit success recovery supplies typed output
and evidence. Interrupted or failed actions retain reconciliation policy. Approved
resumes keep confirmed prefixes and original code; arbitrary external effects still
require reconciliation. Completion delivery stays separate from the business result.

The Node workflow runtime exposes
`uploadArtifact({report_id, name, data, maxBytes})`, sharing byte snapshots, hashing,
coalescing, receipt validation and bounded transport retries with HTTP/Job helpers.
Lookup precedes every transfer retry. Each request fetches fresh workload identity;
finished/detached contexts are denied and cooperative cancellation aborts I/O.
Go clients expose streaming upload and lookup methods; generated Node/Python
contracts expose both routes. The workflow export example returns `{rows,
artifact_id}` and uses direct uploads with no bucket writer or storage credential.

## Validation and rollout

Qualify memory and PostgreSQL stores with current/closed/stale proof rejection,
final-step restrictions, private publication, conflicting declarations, byte
integrity, concurrent copies, cleanup, lost acknowledgements and approved resume
without another transfer. Exercise cancellation, tenant revocation and deadline
expiry during I/O; verify retained files through customer-scoped downloads.
Run client wire/redirect checks, workflow control/helper tests and all three export
starters against the packed SDK outside the checkout. Check generated contracts,
SDK coverage, OpenAPI parity and repository policies. Portable evidence does not
qualify native KVM or fleet activation; production admission remains closed.
