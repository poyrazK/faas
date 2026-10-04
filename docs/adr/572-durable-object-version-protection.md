# ADR-572 · Manage exact-version retention and legal holds durably

- **Status:** Accepted
- **Date:** 2026-10-04
- **Amends:** ADR-563, ADR-564 and ADR-571

## Context

Native Object Lock and permanent bucket configuration already exist. Customers
need to inspect and change protection on an owned data version without exposing
provider version IDs or losing an accepted change after a provider timeout.
A successful-looking acknowledgment alone cannot establish the resulting policy.
Repeating an uncertain mutation can undo a later protection decision.

## Decision

Expose fixed GOVERNANCE/COMPLIANCE retention and independent ON/OFF legal holds
through the control API, standard S3 subresources, Go/Node/Python clients and CLI.
Require an explicit owned public UUIDv4 version ID or the literal `null`;
there is no implicit current-version mutation. Null selection requires fresh
Enabled native versioning and permanent Object Lock. The private version is
pinned in the journal before dispatch. Cross-key, cross-bucket and cross-account
references cannot resolve. Native delete-marker responses fail closed.

Retain one active immutable operation per bucket. Serialize admission in the
account-before-bucket lock order and drain accepted writes, live multipart work
and configuration/deletion journals before accepting protection. Pending intent
fences new writes, deletion, inventory reclamation, configuration changes and
bucket/account cleanup. PostgreSQL triggers enforce admission fences for older
writers too. Reads and operation inspection remain available. Receipt inspection uses only
owned metadata and remains available when backend placement is unavailable.

The control PUT requires a canonical UUIDv4 operation ID and returns a durable
202 receipt. Reusing that ID with different intent conflicts. Standard S3 PUT
can supply a signed `X-Gregale-Protection-Id`; otherwise a new ID is assigned.
An identical active policy request coalesces onto the existing receipt, including
SDK retries with a new ID. Clients must retain the returned ID. Terminal receipts
are retained until physical bucket deletion; migration rollback refuses existing
receipts.

A leased worker verifies fresh native Object Lock and versioning, reads the
exact target, and journals dispatch before sending at most one native PUT.
Only matching readback completes the operation. A known, strictly parsed native
400/403/404 rejection can terminate it as failed. Timeouts, malformed responses,
unknown errors and mismatched policy preserve the journal and fence. After
dispatch, recovery only reads; it never repeats the PUT. An undispatched
reclaimed lease may send the original accepted intent. Provider permissions and
stable placement still apply. Accepted recovery ignores enrollment/ingress
flags; disabling those flags blocks new mutations.

Active fixed retention cannot be shortened or cleared. Active COMPLIANCE cannot
be downgraded to GOVERNANCE. Dates round upward to native millisecond precision.
No governance bypass is sent. Fixed retention changes over observed event holds
are rejected rather than discarding event protection. Event-hold changes and
per-write protection headers/snapshots remain unsupported in this increment.
Native event-hold observations can be read.

S3 GET reports native policy. S3 PUT returns 200 only after verified settlement,
409 for competing/pending work, and 503 for uncertainty. Owned receipts include
only public selectors and bounded error codes; leases, private IDs and provider
messages stay private. Control routes require storage manage scope, bucket
read/write permission and existing MFA policy. Native requests are metered;
policy changes do not change object capacity.

## Limits and consequences

Limits live in `pkg/api/limits.go`: a two-minute lease, 45-second operation
deadline, 30-second retry and a batch of at most 50 due operations. Existing
16 KiB protection body and bounded XML/JSON limits apply.

One unresolved mutation can fence a bucket indefinitely. Missing or mismatched
readback does not prove that the PUT failed. Operators inspect the receipt and
provider truth; they must not clear dispatch evidence to force retries.
Out-of-band provider mutations and provider lifecycle rules remain outside
Gregale's serialization boundary. New Object Lock enrollment stays disabled
for release until write protection snapshots and protected lifecycle deletion
are implemented and qualified.

## Validation

Local tests cover standard AWS SDK → TLS S3 gateway → native HTTP and control
API → daemon recovery → native HTTP with MemStore and PostgreSQL reconstruction.
They exercise exact private targeting, lost acknowledgments, single dispatch,
disabled enrollment recovery, retry identity, conflicting policies, active
retention weakening, ownership, lease races, raw SQL fences and guarded rollback.
Typed Go/Node/Python client and CLI tests cover requests and receipt inspection.
No live provider test or deployment is required by this task.

## References

- [PutObjectRetention](https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObjectRetention.html)
- [PutObjectLegalHold](https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObjectLegalHold.html)
