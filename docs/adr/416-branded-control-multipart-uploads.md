# ADR-416: Branded control multipart uploads

Date: 2026-10-04
Status: Accepted

## Context

Fixed-layout control API multipart part URLs still expose provider placement
and bypass current grants and owned encryption acknowledgments. Their lifetime
also delays verified abort. Existing multipart journals capture encryption and
completion proof, while public S3 transfers already have a durable part fence.

## Decision

Extend ADR-415's sealed URL credentials with a private owned session/part
binding. Freeze the logical key, exact part length, PUT method and headers.
Keep native upload IDs and signing credentials private. Issue atomically against
the active fixed-layout session and original issuer's current grant. Reject
URLs for a different session, part, size or operation.

At redemption, atomically validate current authority, admit the request budget,
lock the owned session, create the native transfer fence, advance its revision
and meter the winning provider attempt. Repeated part uploads may replace a
settled part while the session and URL remain active, matching UploadPart's
contract. Concurrent or uncertain attempts keep the part fenced. Settlement
uses a distinct token for each attempt and may finish after issuer revocation.

Fixed sessions already reserve the full object. Their transfer rows therefore
carry zero additional capacity and cannot be used for dynamic S3 multipart
uploads. Database guards enforce that distinction, current dispatch authority
and pending-transfer fences on completion and terminal cleanup. Older binaries
reject the extended private URL descriptor. Previously issued direct part URLs
retain their existing immutable drain deadline.

Allow explicit owned encryption on control session creation. Capture enrollment
before provider access and use the existing immutable encryption journal for
initiation, part acknowledgment mapping, completion and restart recovery.
Multipart part URLs do not carry initiation encryption headers. Customer
responses return owned selections; native key identities remain private.

## Consequences

Unused branded part URLs stop working immediately when their session begins
abort or completion. Actual native attempts retain the existing bounded
transfer/cleanup window. Uncertain attempts preserve capacity until verified
cleanup. Fixed session accounting keeps its existing conservative object grant
until inventory reconciliation; no new refund is claimed.

Bucket defaults, upload routes, GCS encryption/proof and the other gaps in the
implementation ledger remain separate work. No real provider test is required.

## References

Amazon S3 documents sequential replacement of a part and capture of non-SSE-C
encryption parameters at initiation in its
[UploadPart API](https://docs.aws.amazon.com/AmazonS3/latest/API/API_UploadPart.html).

## Acceptance

Local memory/PostgreSQL state and control API → branded SigV4 gateway → native
HTTP fixture tests pass. Acceptance covers concurrent URL admission and part
dispatch, current grant revocation, sequential part replacement, abort and
completion fences, lost acknowledgment and restart recovery with disabled KMS.
Raw PostgreSQL old-writer paths and rollback cannot bypass live authority.

Focused state/control/gateway race tests, related multipart/encryption/URL
regressions, full provider/gateway suites, standalone Go SDK tests, Node/Python
client tests, OpenAPI parity, 3,842 generated-source parity checks, SDK coverage,
repository gates and changed-line lint (zero issues) pass. No live provider,
deployment or push was performed. Detailed local logs and source hashes are
recorded in `multipart-url-acceptance.json` under the task evidence directory.
