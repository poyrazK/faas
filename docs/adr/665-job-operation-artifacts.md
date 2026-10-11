# ADR-665: Verified private files for Job Operations

## Status

Implemented in closed-admission preview — 2026-10-06. Native Job qualification remains pending.

## Context

ADR-664 confirms typed results through the native Job task outcome. Customers
also need verified, private files without another download service. A storage
write alone cannot confirm a Job's business outcome.

## Decision

Dedicated tokenless Job runtime routes prepare files and inspect preparation
receipts using the current run, instance, generation, attempt and capability.
The stores recheck the live task lease, deadline, active account/app/customer
and native instance before reservation and after copying. A stable report ID
shares the Job report namespace; changed declarations conflict.

Reserve a fresh durable staging intent before I/O, verify the managed private
source's ownership, scope, exact size and SHA-256, and retain an immutable
platform copy with its private receipt atomically. Existing operation/account
artifact limits, spool bounds and cleanup leases apply across all families.
Concurrent preparations converge on one receipt; losing copies remain visible
to cleanup. Same-generation replay returns the retained copy without reading
the source again. Blob intents have exactly one execution family and no owner
or run foreign key, preserving cleanup after deletion.

Only host-confirmed task success with a typed result publishes prepared files,
in the business-completion transaction before the independent delivery outbox.
Uncertain outcomes retain private files for account recovery inspection.
Explicit success recovery publishes the current generation's files. Approved
retry clears receipts and bindings and creates a fresh run; it does not reuse
an uncertain file or repeat a source write automatically. Inspection revisions
include Job receipts and blob binding evidence. Existing customer-scoped
downloads use the private copy and existing retention policy.

SDK helpers check control before writes, support receipt lookup before source
upload, and preserve stable report IDs across lost acknowledgements. Node's
prepared file helper coalesces calls and never repeats an uncertain upload.

## Validation and rollout

Exercise both stores and the HTTP boundary for concurrent/lost-ack replay,
lease and generation fencing, conflicting reports, source ownership/hash,
publication privacy, delivery failure, explicit recovery and orphan cleanup.
Apply the migration and update API/SDKs before using these routes. This changes
no VM lifecycle or admission gate; native Job execution and leak qualification
from ADR-664 remains required before production admission.

Native guest result preparation, lost-ack receipt lookup and private downloads
are included in [ADR-667](667-customer-job-operations-native-qualification.md).
The [native qualification guide](../ops/customer-job-operations-native.md) records
the remaining hardware and provider evidence; qualification is still pending.
