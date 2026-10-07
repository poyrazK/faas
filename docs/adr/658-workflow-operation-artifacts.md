# ADR-658: Private result files for workflow Operations

## Status

Accepted — 2026-10-06. Production admission remains disabled.

## Context

ADR-676 retains confirmed workflow outputs across approved recovery. Export
customers also need private files. An HTTP invocation capability cannot grant
workflow authority, and a successful storage write does not confirm that its
workflow action completed. Lost responses must not cause file regeneration.

## Decision

The first slice allows only the final linear HTTP workflow action to prepare
files. Scheduler dispatch adds an explicit workflow execution family and private
step nonce. Runtime endpoints require a current Operations workload assertion,
matching account/app/running instance on the pinned deployment, operation/run,
recovery generation, final step, monotonic attempt, current nonce and live lease.
Tenant/link/account state is checked with the native workflow authorization.
These proofs cannot substitute for ordinary HTTP invocation claims.

A stable report identity and full artifact declaration bind one logical file
across approved resumes. Before external copy I/O, reserve a durable staging
intent under a fresh random platform key. Verify the existing managed source's
private bucket ownership, scope, exact size and SHA-256 using the existing
bounded verifier; retain its platform copy and private operation receipt in one
claim-fenced transaction. Retained pending files count against the existing
operation/account quotas and remain pinned for reconciliation and recovery.
Account quota reservations serialize across both execution families.

A prepare acknowledgement exposes a verified-copy receipt only. Customer
artifact references are published in the same transaction as confirmed final
step success, before business completion and its independent outbox. A failed
or interrupted dispatched step keeps its prepared files private and requires
reconciliation. Explicit operator success confirmation can publish a verified
pending file. Approved resume preserves the copy; a current-proof receipt check
rebinds it to the new attempt, with no source read or write. The new attempt must
be confirmed before normal publication. Declaration conflicts fail closed.

The Node helper requires stable report/source identities, checks an explicit
available response before invoking the app's existing bucket writer, coalesces
concurrent calls, obtains fresh workload assertions, and guards its original
request. An uncertain write is never automatically repeated by that prepared
object. A fresh approved resume without a verified receipt still requires
provider reconciliation or an idempotent source-write contract. This is not an
exactly-once guarantee for arbitrary provider effects.

Blob intents have exactly one HTTP or workflow execution family and no cascading
owner FK: deletion leaves cleanup discoverable. Existing expiry, fenced cleanup,
bounded verified download and business/delivery separation remain in force.

## Validation and rollout

Memory/PostgreSQL scheduler tests cover preparation privacy, concurrent replay,
owned proof fencing, declaration conflicts, preserved completed prefix, resumed
copy reuse, publication once and completion delivery retry. HTTP acceptance
verifies source mismatch rejection, source deletion after retain, stale proofs,
private download before confirmation and byte-exact download after resume.
SDK and sample tests cover preflight failure, lost responses, stable source keys,
request confinement and one writer invocation across approved generations.
Apply the migration, update API/workers/SDKs and perform native execution
qualification before enabling customer admission. This change activates no
production configuration or deployment.
