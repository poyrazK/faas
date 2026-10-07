# ADR-607: Direct private artifact uploads for HTTP Operations

Status: implemented locally in closed admission; native qualification pending.

## Context

ADR-606 removes the bucket writer from Job exports. Ordinary HTTP Operations
still require an application-managed source object or return CSV inline. Their
existing artifact ledger, dispatch authority, retention and private downloads
can support the same developer contract without changing HTTP settlement.

## Decision

Add binary upload and JSON receipt-lookup routes under
`/v1/runtime/operations/{id}`. Both require the Operations workload JWT and
current invocation ID, attempt and capability. Customers and account API keys
cannot replace this proof. The descriptor contains a stable report ID, filename,
exact size and SHA-256. The platform derives an opaque
`operation://<operation>/artifacts/<artifact>` identity from the invocation
attempt and report ID. No source URI, storage key, bucket or provider credential
is accepted. Managed-source attachments remain available.

Receipt lookup checks the existing immutable report namespace and retained blob
binding without reserving storage, consuming reports or emitting events. A
changed declaration conflicts. Each copy reserves a unique staging object under
existing captured file quotas and current account storage quotas. Shared transfer
budgets bound concurrent temporary files and account/node usage. Exact size and
digest are verified before private storage writes. Workload ownership/instance
checks repeat around storage I/O; the store atomically rechecks the live dispatch
claim and cancellation when committing a new attachment. PostgreSQL lock waits
count against the lease and staging lifetime. Cancellation prevents new writes;
an already committed matching receipt can be replayed while its claim is live.
Concurrent copies converge on one receipt. Failed and losing staging copies
remain eligible for existing durable cleanup.

The receipt and `artifact_attached` metadata event preserve the HTTP ledger's
existing behavior. Neither completes an invocation. Downloads remain scoped and
require HTTP business success or existing explicit confirmed-success recovery.
Uploads do not renew leases, repeat handlers, reopen executions or change
reconciliation and notification policy. No database migration or VM lifecycle
change is required.

The Node HTTP runtime exposes `uploadArtifact({report_id, name, data, maxBytes})`.
It shares the bounded byte snapshot, receipt validation, coalescing and transport
retry implementation with Job uploads. Lookup precedes every byte retry. Fresh
workload identity is fetched per HTTP request, and finished/detached contexts are
denied. Cooperative scope cancellation aborts transfer I/O. The SDK memory bound
defaults to 8 MiB independently of the captured server quota. Go exposes streaming
upload and lookup methods; generated Node/Python contracts expose both routes.

The HTTP export starter requires `artifact_id` and `rows` in its typed output and
downloads that exact artifact. The starter uses direct private storage and needs
no bucket writer. Its compiled output contract rejects the former inline CSV
shape. Existing deployed immutable definitions retain their original schema;
deploying the updated starter creates a new definition revision.

## Validation and rollout

Qualify memory and PostgreSQL stores, HTTP proof rejection, read-only receipt
lookup, conflicting reports, byte integrity, concurrent copies, lost responses,
cancellation during transfer/commit, claim loss, cleanup and private download
publication. Exercise both starters against the packed SDK outside the checkout,
and check generated contracts, SDK coverage and OpenAPI parity. Native KVM and
fleet activation remain separate gates; portable checks do not qualify them.
Production admission remains closed.
