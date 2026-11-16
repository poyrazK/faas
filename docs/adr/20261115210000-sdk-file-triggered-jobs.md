# ADR-20261115210000: Typed file-triggered jobs

Status: Accepted

## Context

The file and Jobs SDKs cover uploads and execution, but the CSV example starts
its job with a second browser request. A completed file can remain unprocessed
when that caller disconnects. Native storage triggers already deliver confirmed
mutations through durable async invocations.

## Decision

Add preview Node and async Python file pipelines, composing existing APIs with
application-owned PostgreSQL submission receipts. Pipelines use optional expected
account/endpoint submission guards, checked against the typed job snapshot before
preparing inputs or POST. Existing callers can omit them. Client reconfiguration
cannot cross the pipeline scope. Existing generated methods and generally
available entry points remain unchanged. No server API changes.

Normalize native S3 Records and `gregale.storage/object.created` CloudEvents into
an immutable typed file identity. Native notifications contain one record. Decode
S3 form-encoded keys exactly once; CloudEvents keys are decoded already. Validate
owned account/app/bucket, receipt, size, ETag and public version selector. Empty
and mutable `null` selectors are not immutable versions. Never infer ordering
from timestamps or random receipt identifiers.

The native verifier re-reads account-scoped invocation proof through an
authenticated SDK client. Match scope, payload and event metadata to the retained
original identity from `state.PublishedEventInvocationID`. A replay must match
its original. Missing/pruned original proof fails closed, including an
independently salted later fanout acceptance without that proof. Header claims
alone are not authentication. Custom verifiers own their trust boundary. HTTP
adapters run behind Gregale's gateway or an authenticated proxy; the example also
requires gateway-owned invocation source/app/account headers.

Resolve an application-owned upload record before processing. Pin the owner,
pipeline revision/configuration, event and contract. Read an immutable public
version when available; mutable objects require a trusted SHA-256 or the upload
example's fixed 1 MiB block digest. Verify size and supplied digest before input
mapping. An unavailable older version never falls back to current bytes.

Mapping must be pure. Validate its typed inputs, then persist their manifest
digest and binding hashes with non-payload scope/event/owner metadata. Large
inputs use the SAME content-addressed JobStore as the job, with a stable URI for
the same digest; the managed adapter provides it. File mapping buffers within
`maxBytes` (default 16 MiB), with a default maximum of 1,000 inputs. Effects belong
in job handlers, whose own input/output limits still apply.

Only the caller that atomically inserts and acknowledges a new intent can POST
a job. The provided SQL adapter requires one AUTOCOMMIT statement per query,
outside ambient transactions. The example uses a pg pool and fresh psycopg
autocommit connections. Configure driver timeouts and install the schema through
an explicit owner migration. Retain receipts while events/replays can arrive.
Deleting a receipt permits another submission.

Duplicates read saved receipts. An intent without a checkpoint returns
`unresolved` and never reposts, even after the API idempotency window. An uncertain
intent insertion also prevents submission. Persist an accepted checkpoint
separately; an uncertain checkpoint save exposes its known run ID. Explicit
reconciliation performs read-only Jobs lookup, accepting only the saved account,
endpoint, contract, original manifest and bindings. It does not claim the API
exposes the original idempotency key. Files need not remain available to recover
a run. A crash before POST can leave an intent with no run; investigate rather
than automatically generate work. Task side effects remain at least once.

One deadline includes proof, owner lookup, reads, mapping, store and Jobs calls.
Cancellation fences late callbacks before subsequent writes. A late database
statement can still commit a safe unresolved intent.

The CSV browser stops calling `/start`. In automatic mode that compatibility
route reports status only. Native root POST consumes notifications. Authenticated
progress/report routes recover checkpoints from the ledger. The demonstration's
private upload records still require a shared state directory; replicated apps
should put ownership records in their application database too. Separate input
and output filters so result writes cannot trigger another import.

## Validation

Shared protocol and portable receipt fixtures test native identity, Unicode keys,
duplicates/concurrency, delayed/out-of-order versions, lost acknowledgments,
reconciliation, cancellation and deadlines. Local integration uses PostgreSQL,
Node/Python consumers competing for one intent, both actual worker languages,
delivery after uploader return and app restart, two users, and forged payloads.
Chrome checks progress, report download and reload without `/start`. Native
storage/routing remain protocol fixtures; live acceptance needs an isolated
configured preview target. Packaging/types and generator preservation cover the
preview boundary.
