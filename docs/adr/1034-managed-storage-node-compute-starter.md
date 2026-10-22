# ADR-1034: Managed storage Node compute starter

Status: Accepted

## Context

Managed storage binding helpers and request leases require application wiring.
The existing uploader uses startup provider secrets and does not demonstrate
atomic compute adoption, readiness or stream retirement.

## Decision

Ship an independent object-storage-node template with authenticated PUT/GET
routes, managed read_write storage, atomic storage/auth consumer reloads,
read/list candidate validation, request leases and bounded graceful shutdown.
Stage uploads on private temporary disk for bounded memory and exact length;
forward downloads as non-executable attachments. Bound sample app transfers
independently of the platform's account/bucket quotas. Sanitize diagnostics.

Bundle generated, dependency-free canonical Node SDK runtime helpers so the
starter can build without an unpublished Gregale package. Compare generated
copies in the SDK test gate. Pin the AWS client using a starter lockfile.

Give the AWS client its own mutable credential copy: it annotates resolved
credentials, whereas binding configurations remain immutable. Accept the SDK's
literal /bucket/ URI as a bucket operation without rewriting the signed URI or
changing nonempty object keys. The actual journey qualifies these adapter seams.
Preserve explicitly signed zero Content-Length when reconstructing both header
and presigned signatures. Go's signer otherwise omits zero lengths, rejecting
valid empty uploads. Changed signed lengths must still fail verification.

Qualify the materialized CLI template with local app tests and an actual Node
process crossing the production SigV4 gateway, S3 provider adapter and memory
state. Exercise byte integrity, rotation with a held stream, failed adoption
after revocation, recovery, cancellation and shutdown. Run this on PR CI and
the full release CI. Local provider fixtures do not qualify a real provider,
database persistence, microVM wake/restore or production deployment.

## Consequences

Applications have a complete managed compute example with no provider secrets.
Disk staging adds guest disk use and latency. Readiness checks consume gateway
budgets and prove read/list access only; write permission and release readiness
still require managed policy and operation canaries. Existing uploader behavior
is preserved. Authentication belongs to the sample app, not the S3 gateway.
