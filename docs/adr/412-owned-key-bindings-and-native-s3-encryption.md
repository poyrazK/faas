# ADR-412: Owned key bindings and native S3 encryption

Status: Accepted (2026-10-03)

## Context

Customer encryption remains an open end-to-end implementation gap. Forwarding
an AWS key ID from a customer request would bypass Gregale's account ownership
boundary. Accepting an ordinary write acknowledgment or recovering only its
receipt and size would also lose the customer's encryption choice.

## Decision

Introduce an operator-owned encryption capability and managed-key allowlist per
backend. Built-in S3 supports explicitly declared `AES256`, `aws:kms` and
`aws:kms:dsse` modes. A declaration requires the complete optional provider
interface; built-in GCS has no declaration until its implementation exists.
Existing registries omit the declaration and retain their existing behavior.

Each binding has a canonical nonzero Gregale key UUID, owning account UUID and
fully qualified native key resource. Customers select only
`arn:gregale:kms:<public-region>:<account>:key/<key-id>`. Resolve it against the
authenticated account. Reject aliases, raw material, native IDs from customers,
cross-account references, duplicate references and ambiguous native owners,
including across backends. S3 native resources must identify a key in the
backend's signing region. Configuration snapshots own their slices; dispatch
selections own their optional bucket-key value.

Capture a private immutable binding identity over the backend placement,
explicit KMS endpoint, owned reference and native key. Reconstructing the same
configuration preserves an admitted snapshot. Changing its native key,
placement or KMS endpoint prevents its redispatch. Capabilities and key
enrollment do not alter the established bucket placement fingerprint, so adding
an unrelated key does not invalidate existing buckets.

S3 uses its existing credential provider with the native AWS KMS SDK. An
optional `kms_endpoint` is an operator origin for compatible services and local
fixtures; it is never derived from a customer request or the S3 endpoint.
The credential identity must have kms:DescribeKey separately from any
S3-service-scoped data-key grants. DescribeKey requires one bounded read and
verifies the exact ARN, key ID,
provider account, customer-managed symmetric encryption type and enabled state.
This is identity/type validation, not proof of S3's effective grants. The
actual provider write/read still enforces GenerateDataKey/Decrypt permissions;
there is no permission bypass or fallback to an implicit AWS-managed key.
Recovering already written proof does not require an enabled key probe.

Native PUT, receipt-bound private signed PUT, tracked copy and multipart
initiation explicitly select the resolved mode, key, optional context and
bucket-key setting. KMS selects bucket keys disabled when omitted, rather than
inheriting a mutable provider default. Dual-layer KMS excludes bucket keys.
Metadata COPY preserves the inspected source metadata and source identity
fence; destination encryption is independent. Parts inherit the initiation's
managed encryption; they do not introduce another key selection.

Bind the entire selection, including the non-secret base64 JSON context and
account, into a reserved private metadata digest alongside the unique write or
multipart receipt. Customers cannot supply it, metadata COPY strips it, and
the branded gateway hides it on reads. Write/copy/initiation acknowledgments
must contain an unambiguous matching mode/key and valid bucket-key setting.
Missing, duplicate or mismatched acknowledgments remain uncertain after
dispatch and cannot establish rejection or release admission.

Current and retained-version recovery additionally require the exact stored
encryption digest. Historical cursors bind to the encryption selection.
Multipart completion verifies its acknowledgment and then inspects the returned
native version for its session, size, ETag and initiation digest; its response
headers alone cannot prove the context. Recovery after a lost completion also
requires that digest. Operation results expose only the owned selection; native
key IDs, private binding hashes and native version selectors stay private.

Bounds live in `pkg/api/limits.go`: 256 bindings per backend, 512-byte resource
references, 8 KiB decoded context with at most 32 unique string-valued entries,
64 KiB provider key responses and 32 levels of JSON nesting. Reject duplicate
JSON identity fields before SDK parsing. Close provider bodies and normalize
errors without native resource names, key material or service detail.

## Verification

Local native S3/KMS SDK HTTP fixtures cover AES256, KMS and dual-layer KMS;
owned binding reconstruction and remapping fences; signed PUT execution;
independent destination copy encryption; multipart initiation, signed part
execution, lost completion and exact-version completion proof; retained history
after overwrite; invalid acknowledgments, key state/type/identity, bounded
responses, denied native KMS authorization, private metadata and safe results.
Unit checks cover registry capability claims, account isolation, configuration
aliasing, strict context parsing and explicit bucket-key choices. Related
regressions and focused race checks pass. Full API/provider and gateway/daemon
regressions, related control-API/OpenAPI checks, changed-line lint and repository
policy gates pass. Local crypto snapshots reconstruct fresh provider clients;
this is not PostgreSQL admission/recovery qualification for customer encryption.

## Completion boundary

This is the internal provider foundation, not completion of the customer
encryption feature. S3 customer directives remain explicitly unsupported until
owned public configuration, durable write/multipart snapshots, per-request
metering, reconciliation, response mapping and Go/Node/Python/CLI support are
wired and qualified with memory/PostgreSQL restart tests. Native signed URLs
remain private to Gregale; this increment does not enable encrypted public
provider URLs. GCS CMEK, cross-bucket encrypted copies, bucket-default changes
and SSE-C remain open. Live provider qualification is excluded from the user's
implementation request. No deployment or activation is performed here.

## References

- [AWS KMS DescribeKey](https://docs.aws.amazon.com/kms/latest/APIReference/API_DescribeKey.html)
- [S3 KMS permissions and encryption context](https://docs.aws.amazon.com/AmazonS3/latest/userguide/UsingKMSEncryption.html)
- [S3 PutObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObject.html)
- [S3 multipart initiation](https://docs.aws.amazon.com/AmazonS3/latest/API/API_CreateMultipartUpload.html)
