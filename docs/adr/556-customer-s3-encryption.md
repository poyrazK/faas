# ADR-556: Customer S3 encryption and owned response metadata

Date: 2026-10-04
Status: Accepted

## Context

ADRs 554 and 555 provide native S3 encryption and durable private snapshots, but
customer requests still reject encryption directives. A public native key ARN
would expose operator resource identities and bypass account ownership. Native
acknowledgments must establish the requested cipher before journals settle.

## Decision

Accept explicit AES256, aws:kms and aws:kms:dsse selections on ordinary branded
S3 PUT, same-bucket copy and multipart initialization. Every supplied encryption
header must be singular, valid and covered by SigV4. KMS selections require the
bucket owner's enrolled Gregale reference; native ARNs, aliases and foreign
references are rejected before provider access. KMS bucket-key selection is
frozen explicitly. Encryption context follows the existing strict bounded JSON
contract. Unsupported directives, SSE-C, query directives and cipher directives
on read/part/completion operations remain rejected.

Capture the immutable selection in the admitted journal before native key probes
or writes. Private provider PUT URLs remain inside the gateway. Meter actual KMS
calls and native initialization list/create attempts. An encrypted copy's native
mutation has a separate dispatch fence after key validation. Missing or
mismatched successful write acknowledgments retain a recovery intent; no write
or copy is replayed to repair an uncertain result.

Return owned encryption references on successful PUT/copy/init/completion and
map GET/HEAD native metadata through the bucket owner's allowlist. Unknown keys
or ambiguous encryption metadata fail before object bytes are forwarded. Native
key resources, context and proof metadata remain private. Optional part cipher
response headers are validated and mapped when supplied; final completion still
requires exact object/session proof. Expose these public response headers to
allowed browser origins.

Add GET encryption-capabilities under the logical bucket, a typed Go/Node/Python
client and the bucket encryption-keys CLI command. Discovery requires storage
write scope and the bucket write grant, uses no provider requests, remains
available while ingress is disabled, and reports enrollment rather than key
health or effective provider permissions.

Recovering a native multipart upload identity validates the captured binding
but does not require an enabled-key probe. Probe enabled KMS identity immediately
before creating a new native upload. Exact-key adoption retains the existing
private-credential and one-live-session-per-key contract; native list responses
do not establish cipher proof, which is required at final completion.

## Consequences

This enables explicit encryption on the branded S3 gateway. Bucket defaults,
control API upload requests and signed URL brokerage, upload routes, GCS,
cross-bucket copy and SSE-C remain in the implementation ledger. Enrollment does
not create keys or manage native policies. Changing/removing an enrollment can
make old encrypted objects unavailable until the matching binding is restored.

SDK regeneration also refreshes previously stale Python contracts from the
unchanged scheduled-work schemas in the canonical OpenAPI document.

## Acceptance

Real AWS SDK requests traverse the local branded gateway and native HTTP fixture
with both memory and PostgreSQL stores for all three cipher modes, PUT/copy,
GET/HEAD, multipart upload and exact completion, durable selection and native
request accounting. Tests cover foreign/native references, disabled keys,
ambiguous acknowledgment retention and recovery without write replay or a new
key probe, strict signed-header parsing, owned read projection, disabled-key
multipart adoption and failed copy dispatch fencing. Control API tests cover
scope/grant ownership, disabled ingress and provider-free discovery. Client,
related regressions, races, generation parity, lint and repository checks are
recorded in local acceptance evidence. No real provider is required.
