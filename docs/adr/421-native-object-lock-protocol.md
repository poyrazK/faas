# ADR-421: Native Object Lock protocol and versioning lock order

Date: 2026-10-04
Status: Accepted

## Context

Completion recovery can locate private write proof in retained S3 versions, but
permanent deletion can remove that evidence. Object Lock supplies native version
protection and is a prerequisite for stronger retention of completion proof.
Gregale needs safe native primitives before exposing irreversible enablement or
per-version customer management. Existing versioning cutovers also take bucket
and account locks in the reverse order to quota admission and account deletion.

## Decision

Add optional native S3 interfaces for bucket Object Lock configuration, exact
version retention and exact version legal holds. Preserve fixed GOVERNANCE and
COMPLIANCE periods, independent legal holds, and current native event holds.
Bucket enablement is irreversible: clearing a default sends Enabled with no
default rule. No disable request is representable as an accepted native PUT.
Every version operation requires a nonempty private native selector; existing
null versions are supported only when the eventual service has qualified a
permanently enabled versioned bucket. These interfaces confer no tenant access
or advertised backend capability by themselves.

Bound all native success and error bodies to 16 KiB. Validate XML before the
SDK can discard unknown fields, checking roots, namespaces, attributes,
duplicates, singular fields, modes, periods, timestamps, depth and element
count. Unknown well-formed policy extensions return Unsupported and preserve a
known Enabled observation, which the durable service must turn into a sticky
versioning fence. Malformed policies and empty legal hold responses must never
become an unprotected observation. Only the exact configuration-not-found code
with HTTP 404 establishes absent bucket Object Lock configuration. Empty 200
configuration documents and Enabled documents with an empty Rule are unknown,
not an absent policy or an accepted default clear (qualified with ADR-422).

Sign requests with the configured provider credential and native SDK. PUTs
carry SHA256 checksums of the exact XML. Each operation makes one native attempt;
an uncertain mutation must be recovered by reading exact provider truth. Native
response version headers, when present, must match the exact selector. Never add
governance bypass implicitly; the service must separately authorize any explicit
request. Bypass cannot override COMPLIANCE mode or an independent legal hold.

Owned durations support 1–36,500 days or 1–100 years, exactly one unit per period.
Fixed and event defaults may coexist. Request retention timestamps round up to
the SDK's millisecond precision, preserving the requested minimum; observations
retain native precision. Capture that normalized intent before future durable
admission. Policy snapshots clone pointers so later caller changes cannot alter
the accepted target.

Event-hold release requests omit the duration. An OFF intent without an explicit
date requires a previously active event hold; S3 computes the final date from
the stored duration. Native OFF observations must contain that final date and
may retain the stored duration. New requests cannot reuse an observation's
duration to change it during release. The durable service must compare release
results semantically, preserving the computed date rather than retrying the
mutation because the response contains more fields than the request.

Change PostgreSQL versioning mutation lock order to account, then bucket. A
contender must leave the bucket available while waiting for an existing account
owner; a deterministic local PostgreSQL test reproduces both request and
observation contention.

## Acceptance

Native SDK → local HTTP fixtures qualify bucket defaults, fixed/event retention,
independent legal holds, default clear preserving Enabled, exact opaque/null
version selectors, Unicode keys, signatures and exact SHA256 request checksums.
Model tests qualify duration bounds, pointer snapshots, write timestamp rounding
and the separate event-release request/observation constraints. Invalid requests
make no native calls. Unknown/duplicate fields, namespaces, attributes, empty
policies, malformed errors, embedded Error documents, wrong version headers,
response sizes and XML structure bounds fail safely. Conservative enablement
observations survive unreadable bucket policies.

Each mutation makes one attempt after 5xx or connection loss. Reconstructed
adapters read the accepted native policy without repeating its mutation. Local
PostgreSQL tests prove account-before-bucket contention for both versioning
requests and observations. Related state/gateway/control versioning and
cross-bucket regressions, full provider/API suites, focused model/provider/state
race checks and repository policy gates pass. Changed-line lint reports zero
issues. Qualification uses local HTTP and PostgreSQL only.

This increment does not expose Object Lock to customers. Durable irreversible
enablement, version ownership/management, write policy snapshots, protected
deletion/lifecycle/account cleanup, API/SDK/CLI capability gating and end-to-end
recovery remain required. Automatic proof retention and the broader S3 gaps
goal remain open. Real provider qualification is excluded by user instruction.

## References

- [S3 Object Lock configuration](https://docs.aws.amazon.com/AmazonS3/latest/userguide/object-lock-configure.html)
- [S3 retention and legal/event holds](https://docs.aws.amazon.com/AmazonS3/latest/userguide/object-lock.html)
- [Native retention shape](https://docs.aws.amazon.com/AmazonS3/latest/API/API_ObjectLockRetention.html)
- [Native event hold bounds](https://docs.aws.amazon.com/AmazonS3/latest/API/API_EventHoldDuration.html)
- [Event hold release and governance bypass](https://docs.aws.amazon.com/AmazonS3/latest/userguide/object-lock-managing.html)
- Pinned AWS S3 SDK v1.113.1 and smithy-go v1.28.1 serialization.
