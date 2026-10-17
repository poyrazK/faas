# ADR-1351: Durable owned-node settlement acknowledgement delivery

Status: Accepted

## Context

ADR-1350 permits collection of historical node proofs only after an exact
acknowledgement of central terminal settlement and a further 30-day retention
window. A single delivery attempt can be lost after the database commit or
during a restart, pinning otherwise collectible metadata indefinitely.

## Decision

Add `object_node_settlement_outbox` in PostgreSQL with MemStore parity. New
UUIDv7 PUT, copy, route, signed URL and multipart admissions enroll only when
their concrete owned S3 backend enables both `owned_node_receipts` and
`owned_node_settlements`. Enrollment commits with admission before native I/O.
It copies the original mutation binding's backend ID, fingerprint and physical
bucket into a waiting row. There are no cascading foreign keys: delivery must
survive deletion of the customer receipt, bucket, app or account.

The existing settlement owner publishes the exact terminal payload in that
same transaction as its customer result, accounting and event publication.
Any outbox error rolls back settlement. Prepared failures discard the waiting
row; dispatched unknown writes remain waiting. Completed/rejected PUTs bind the
original size and committed ETag. Multipart completion binds the frozen parts,
conditions and checksum manifest; rejection becomes deliverable only after
verified abort commits. Plain verified abort uses zero size and empty ETag and
manifest. No job can be created from transient terminal caller input alone.
Terminal replay never re-enrolls a delivered job.

Apid's existing object recovery loop claims at most 16 ready jobs per sweep
using `FOR UPDATE SKIP LOCKED`. A UUID token and two-minute lease fence both
successful deletion and retry scheduling, including workers that outlive their
lease. Attempts saturate at the signed 32-bit maximum. Each sweep has a
40-second deadline; each signed RPC has the existing two-second timeout and
performs one attempt without redirects. Cancellation stops unstarted jobs;
their leases expire for another worker. Database finalization uses the existing
bounded settlement context.

Delivery resolves the saved backend fingerprint and sends the saved payload to
the saved physical bucket, independently of current bucket existence or the
new-write feature switch. Disabling new enrollment permits existing jobs to
drain while the receipts capability and original placement remain configured.
Missing or changed configuration receives a one-hour retry delay. Other
failures back off from five seconds, doubling to one hour. The existing loop's
15-second cadence can delay a due attempt further. There is no retry age limit
or automatic dead-letter deletion. A missing proof, malformed reply or lost
reply always retains the job. Exact repeated node acknowledgements preserve
the node's original acknowledgement time.

Expose waiting/ready backlog counts and the age of the oldest ready job. Add
fixed `settlement` operation outcomes to the existing recovery attempt counter.
Metrics and logs do not label customer keys, receipt IDs or raw provider errors.
The delivery worker cannot mutate the customer outcome or capacity ledger.

## Deployment and recovery

Apply the additive migration before deploying writers or workers. Enable new
enrollment after all gateways, apid writers and recovery workers have been
upgraded. During a
rolling upgrade, old writers can still produce terminal records without jobs;
there is no retrospective backfill or fabricated acknowledgement. Existing
historical settlements can still use the explicit signed adapter/offline CLI
after an operator verifies their exact durable terminal evidence.

Back up the outbox with central PostgreSQL state. Restore compatible central
and node recovery points under ADR-1350's 30-day post-acknowledgement contract.
A database rollback that resurrects an already collected proof cannot be
resolved from absence: its job stays deferred for operator reconciliation.
The outbox is delivery durability, not a distributed rollback guarantee.

Indexed online node cleanup and qualification on separate hosts and disks are
separate milestones. This change adds no automatic GC or failover authority.
