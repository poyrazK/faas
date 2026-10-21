# ADR-20261021101500: Deployment attempt request fingerprints

## Status

Implemented. Amends ADR-20261019120049 (deployment submission receipts).

## Context

An app deployment sent with `Deployment-Attempt-ID` records a receipt naming
the accepted deployment. A later request with the same attempt ID replays that
deployment. The receipt did not record what the attempt asked for, so a client
that reused an attempt ID for a different image or source received the earlier
deployment as if the new request had been accepted. Project receipts already
bind their request semantics (ADR-20261019120002).

## Decision

Receipts store a request fingerprint, a SHA-256 over the route pattern, query
and request content:

- JSON requests (image deploys, source-ref deploys) hash the canonical JSON
  body, so key order and whitespace do not count. A body that is not one JSON
  value gets no fingerprint; the handler rejects it before any deployment.
- Multipart source deploys hash each field by name and content as the handler
  reads it, and the legacy source-tarball route hashes its parsed form. The
  boundary, part order and file names do not count, because clients choose a
  new boundary for every request.
- Upload commits hash the upload session ID; the session fixes the content.

A request whose attempt already has a receipt is replayed only when the
fingerprints match. A different request returns 409
`deployment_attempt_mismatch` and creates nothing. Multipart handlers make this
decision after reading the fields and source, before validation or admission.
Receipts accepted before fingerprints existed have none and keep replaying, as
does a receipt attached to a cached replay under a new attempt.

The fingerprint is `deployment_submission_receipts.request_fingerprint`, a
nullable column added online with a format CHECK. It is not part of the receipt
API response.

## Consequences

Reusing an attempt ID is safe: it either recovers the same deployment or fails
loudly. Clients that generate a fresh attempt per deployment, as the CLI does,
are unaffected.

## Validation

`TestDeploymentAttemptFingerprintHTTP` covers JSON and multipart replay and
mismatch through the Go client; `deploystaging` tests cover canonical JSON and
boundary-independent multipart fingerprints.
