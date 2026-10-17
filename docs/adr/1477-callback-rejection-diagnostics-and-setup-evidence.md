# ADR-1477: Callback rejection diagnostics and setup evidence

Date: 2026-10-15

Status: Accepted

## Context

Accepted hint receipts omit ignored and rejected requests. An empty queue cannot
show whether a signing secret is wrong or the provider webhook has delivered an
event. Diagnostics must distinguish local configuration, observed delivery and
successful lifecycle synchronization.

## Decision

Record one metadata-only diagnostic row per Linear app connection. Scope writes
using the credential record's app, owner and revision, never rejected body fields.
Require the connection to remain live, connected and webhook-enabled. Fixed
counters track invalid signatures, wrong workspace/team scope, stale unknown
deliveries, malformed payload/delivery headers and conflicting reuse of delivery
IDs. Counters saturate at the database integer maximum, include retries and
unauthenticated attempts, and carry no payload, hash, provider ID, secret or arbitrary
error string. Record failures best-effort without changing the rejection response;
these counters are operational observations, not an audit ledger. Oversized bodies,
missing configurations and ignored event types are outside the counters.

Fresh, authenticated supported Issue events record last verified time even when
no active policy tracks the ticket. Known expired retries are still acknowledged
but neither renew verification nor increment stale rejection counts. Fresh success
acknowledgments require durable diagnostic recording; transient failures remain
retryable. Delivery receipts and policy processing retain ADR-1476 semantics.

Configuration replacement hides old diagnostics immediately and resets counters
and verification on the first event for its new revision. Stale writers cannot
overwrite a newer diagnostic revision. Disconnect hides all evidence. Rejection
counters cover the app connection, not a particular linked ticket. Rows cascade
with connection deletion; rollback refuses to discard retained diagnostics.

The owner dashboard distinguishes not configured, configured awaiting delivery,
a verified delivery within 24 hours, expired evidence, and evidence ahead of the
server clock. The 24-hour recency bound lives in `pkg/api/limits.go`. Show fixed
recovery instructions for each rejection class. Verification is evidence of a
received signed Issue event, not a provider registration query, complete delivery
coverage, or synchronization success. No provider write, callback replay or policy
re-enable is triggered by viewing diagnostics.

GitHub's shared ingress cannot safely attribute an invalid signature to an app.
Do not trust a failed request's installation/repository claim to produce owner
counters. Use the current owned installation/repository's retained verified ticket
receipts for setup evidence and direct owners to GitHub App delivery logs for
rejections. Linear's app-specific endpoint provides a trustworthy configuration
scope for counting attempts, without implying the sender's authenticity.

Read setup and receipt aggregates in the existing repeatable-read, scoped health
transaction. Restrict receipt scans by indexed scope key as well as current ticket
and connection identity. Public API/SDK DTOs remain credential-free and unchanged.

## Validation

Decoder tests cover rejection classification while preserving `ErrInvalidCallback`
compatibility. Recency tests cover missing evidence, exact expiry, future timestamps
and disabled configuration. PostgreSQL acceptance checks all counters, duplicate
late retries, cross-owner and old-revision writes, replacement, fresh ignored events,
disconnect and GitHub evidence. Dashboard tests check provider-specific recovery
copy and verification boundaries. Provider calls remain mocked.
