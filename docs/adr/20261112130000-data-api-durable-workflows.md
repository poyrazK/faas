# ADR: Data API upload recovery, background work and behavior contracts

Status: Accepted

## Decision

An absent database commit response is an unknown outcome. Uploads reconcile by
their unique object key and preserve bytes if reconciliation is inconclusive.
Deletion commits metadata first and cleans up only keys returned by that commit.
Failed cleanup may retain unreferenced bytes; it must not delete a referenced file.

Optional upload sessions live in the customer database. Internal invoker RPCs,
hidden from public REST and OpenAPI, bind sessions to a hash of the verified
authorization context. Five MiB immutable chunks have SHA-256 checksums. A bounded
stream composes them using ordinary S3 GET/PUT operations so every existing S3
binding can support resumability. PostgreSQL commits the file pointer and receipt
in one transaction, with optional version comparison. There is no claimed atomic
transaction between PostgreSQL and S3. An uncertain result retains bytes. Owner
cleanup must confirm that bytes are unreferenced. Composition incurs extra bucket
I/O; expired uploads and replaced session versions need owner retention/cleanup.

The SDK offers an explicit lossless decoding mode with matching generated types.
It maps all JSON numeric tokens to decimal strings, including nested JSON, and
uses the same codec for Data API live reads. Existing number decoding stays the
default. This is a read codec; it does not preserve the distinction between a
numeric JSON token and a numeric-looking JSON string when serializing data back.

The Teams starter supplies an optional customer-database queue for imports and
exports. An existing Gregale Job leases work with a backend server key and acts
for the recorded user. Every batch rechecks current membership. Import receipts
and offsets remain the authority after failures; cancellation stops future
batches. Exports materialize a bounded statement snapshot in one transaction,
and later downloads use that saved result. Progress, cancellation, results and
error reports persist in customer tables protected by RLS. There is no new
control-plane scheduler or privileged SQL execution outside customer workloads.

An optional scheduled Job can wake a parked API through an owner-only hook-drain
route. Publication retains the API app's workload identity, existing outbox
leases, ordering and deduplication. Owner observations expose backlog, failures,
leases and oldest-event lag. Each invocation uses normal Job and HTTP admission
and incurs their ordinary usage. No delivery latency is promised while optional
work cannot be admitted or the publisher is unavailable.

Owner-only behavior snapshots hash policy expressions, function bodies (including
private helpers), relation/column grants, role inheritance/settings, defaults,
constraints, triggers, view options and extension versions. These digests are
separate from the serving fingerprint. Changed objects require an explicit
mapping to successful authorization scenarios from the current local rehearsal.
Digests detect changes; they do not prove policy correctness. SQL definitions
never enter public schema metadata or the digest artifact.

An optional `/openapi-3.1.json` exports standard request bodies, unions, nullable
schemas, components and bearer security. The existing Swagger endpoint remains.

## Consequences

Customer SQL upgrades and worker scheduling are explicit opt-ins. Existing
deployments continue to serve ordinary storage/REST without these migrations.
Upload session bounds are canonical platform limits; Teams queue bounds are
application example limits. Native local acceptance covers PostgreSQL,
PostgREST, storage fault injection and worker HTTP flows. Deployment, publishing
and native KVM acceptance remain separate release steps.
