# ADR-158 · Provider-neutral resumable multipart object uploads

- **Status:** accepted
- **Date:** 2026-09-06
- **Decision:** expose Gregale-owned durable multipart sessions backed by the
  provider interface, without exposing native S3 credentials or upload IDs.

## Context

The first object-storage slice signs one PUT with an exact length. That is a
poor fit for large objects and unreliable client networks: any interruption
restarts the whole transfer, and the S3 single-operation ceiling cannot cover
the configured 5 TiB object limit. Calling S3 multipart APIs directly from the
customer would expose the upstream upload identifier and couple Gregale's API
to one provider's lifecycle behavior.

Multipart completion and initiation also have ambiguous failure windows. The
provider may commit an operation while apid loses the response. A safe protocol
must survive process exits and multiple API replicas without completing the
wrong object or leaking unfinished parts indefinitely.

## Decision

Gregale creates a durable upload row containing the logical bucket/key, declared
final size and content type, calculated part layout, expiry, state, completion
ETags, retry metadata, and a private provider upload identifier. There is at
most one live session per bucket/key and 100 per bucket. Creation with the same
key, size and content type returns the live session; a different shape conflicts.
The entire declared size is admitted through the existing conservative capacity
accounting before any upstream parts can be created.

`objectstorage.Provider` owns ensure, sign-part, complete and abort operations.
The generic S3 driver uses 64 MiB parts by default, up to 10,000 parts and a
5 TiB final object. Every part URL binds its exact Content-Length. Customers see
only the Gregale session ID and submit ordered provider ETags at completion;
the native upload ID remains an implementation detail, so another S3-compatible
backend or a future Gregale-operated gateway can implement the same contract.

Initiation is an ensure operation. Before creating an S3 upload the driver lists
exact-key multipart uploads; the catalog's single-live-key invariant makes one
match recoverable and multiple matches a conflict requiring operator cleanup.
New uploads store the Gregale session ID in object metadata. Completion ETags
are persisted before the provider call. If S3 reports a missing upload after a
lost completion response, an exact HEAD size plus that session metadata proves
the intended object completed; otherwise Gregale fails closed.

Initiating, completing and aborting states use durable two-minute leases,
bounded provider calls, retry backoff and lease-token fencing. Active sessions
expire after 24 hours and recovery moves them to abort. Disabling `s3_enabled`
blocks new initiation and part signing, but completion, explicit abort and
expired-session cleanup continue. Operators must also configure an upstream
abort-incomplete-multipart lifecycle rule with a longer window as defense in
depth. Live sessions block bucket deletion.

All routes require `storage:write` and the existing bucket write grant for
non-admin keys. Signed part URLs remain short-lived bearer capabilities. No
list-all-uploads route, native S3 credential, provider upload ID, customer price,
or invoice behavior is introduced.

## Consequences

Clients can upload large objects concurrently, retry individual parts, and
recover state after an API or network failure without changing when Gregale
switches S3 providers. Conservative admission may retain capacity after an
abandoned attempt, consistent with ordinary signed PUT behavior. Provider
qualification must now cover multipart pagination, length binding, ETag syntax,
completion ambiguity, abort idempotency, lifecycle cleanup and concurrent
replica recovery. A live-provider test remains required before launch.

Native S3 protocol compatibility, checksum negotiation beyond provider ETags,
server-side part copying, customer-selected part sizes, list-all sessions and
automatic cross-provider data migration remain deferred.

## Rollout

Apply the migration before deploying new API replicas and keep `s3_enabled=false`
until all replicas understand multipart sessions. Before rollback, disable new
signing, finish or abort every live session, wait for issued URLs to expire, and
restore the configured upload ceiling plus durable capacity grants to the old
5 GiB bound. Only then stop recovery and run the down migration. Dropping live
session rows would orphan upstream parts, so the rollback procedure must never
use the migration to discard active work.
