# ADR-20261117070839: Owner review of retained hosted Task source

Status: Accepted
Amends: ADR-20261116183545, ADR-20261115082509

## Decision

Extend the existing APID-owned encrypted hosted source journal with source review
decisions. Reuse the native Task review transition rules rather than creating
execution authority. Keep the hosted digest domain, immutable account/requester,
source and app/policy pins, and original fifteen-minute non-renewable deadline.

Only an authenticated personal owner or a fresh unscoped non-agent personal
administrator may PUT `/v1/task-tool-source-intents/{id}/decision`. The owner
explicitly names both `intent_sha256` and `review_sha256` and chooses approve,
reject or revoke. Reload cached role/project/revocation facts for each request;
lock API-key owners again in the native transaction. MCP-login and agent keys
cannot make decisions. Source approval requires enabled native retention.

On approval, hold the account and source row locks, check retained source and
its fixed expiry, then lock and recheck the original requester and ordered native
app/configuration/serving snapshots. Actual database errors remain errors, not
readiness observations. Missing or changed facts permanently stale the review.
Expired or absent source cannot gain approval. Exact approval repeats retain
the original decision timestamp; rejection/revocation never renew the deadline.
Cancellation does not require live requester/app facts; already stale or expired
reviews remain terminal. Rejecting an approved review uses revocation instead.

Store only review status and decision time in the existing JSONB payload. Source
bytes, ciphertext and namespace never change as a result of a decision. No new
table, issued migration change, clone capability or identity reuse is needed.
Older receipts without a review status remain pending until source expiry. GET
projects metadata only and reports the last recorded observation; POST check
permanently records observed native drift. Native journal idempotency excludes
generic HTTP response replay so every check and decision reloads authority.

The CLI adds `mcp server tasks source-review get|check|approve|reject|revoke`.
Owner decisions require both digests and need no local source directory or
customer-code execution. The hosted MCP catalog adds a consent-gated check,
without approval/rejection/revocation tools. Its receipts remain metadata only
and always have `ready_to_apply=false`, `bindings_verified=false` and
`owner_authorization_required=true`, including after source approval. Credential
binding, native readiness qualification, build/deploy, Task migration and traffic
changes remain separate future owner-controlled flows. Existing local Task
release/recovery execution cannot consume this hosted source review.

## Validation

Exercise immutable digest/source binding, fixed expiry, exact and concurrent
decision replay, owner/requester and account separation, cached key revocation,
agent/MCP-login refusal, permanent stale states and terminal cancellation in
memory/PostgreSQL handlers. Verify restart recovery and that decisions do not
alter ciphertext. Cover expired/missing-source and malformed/authority-bearing
receipts, both hosted MCP protocols, the owner CLI without Node/source files,
schema parity, SDK coverage/regeneration and static gates. Preserve all existing
publication and Task assertions. Live staging and native KVM acceptance are not
part of this source-review slice.
