# ADR-20261115171000: Lifecycle-aware Task recovery guidance

Status: accepted

## Context

Read-only Task recovery planning predates the owner handover, atomic restore,
quarantine and retirement journals. The original executor state remains
`reconcile_required` after recovery. Classifying only that field can suggest
resuming a permanently fenced executor or repeating a committed recovery effect.

## Decision

Validate handover and phase metadata using shared domain checks before recovery
inspection observes native resources. Add optional lifecycle and next-step
summaries to the existing version-one inspection/plan response and strict MCP
schemas. Preserve original execution state, deployment checkpoints and fresh
native facts. Lifecycle classification records historical progress, not service
health. No REST/storage/Task schema, quota or execution authority changes.

Always block resume after handover or executor exit. Block already-admitted or
completed phase mutations. Include quarantine among planned actions; native
quarantine requires a retained restore, and native retirement requires observed
quarantine. Unconsumed expired/revoked qualifications require owner reconciliation.
Pending quarantine/retirement observations remain available after expiry/revocation
through the existing trusted adapter and original private bindings. A native
zero-live observation cannot attest Task registration expiry. Changed candidate
identity/generation during pending retirement requires owner reconciliation.

An unstable journal or future recorded timestamp gives inspection-only retry
guidance. Reject malformed identity, revision, qualification, retained-result or
unknown-state metadata. Completed retirement recommends a fresh release review;
it never takes over or renews the old claim. The report stays read-only with
`ready_to_apply=false` and `owner_authorization_required=true`; its hashes identify
an observation and are never consumed as execution authority.

## Consequences

Both MCP protocols and the CLI expose the same domain report without local
credentials, files, customer-code execution or Task-database access. New fields
are additive; operations without lifecycle metadata retain legacy guidance.
This milestone changes observation and planning only. It neither performs native
lifecycle effects nor supplies dedicated native/staging acceptance for the parent
retirement work. Live staging remains skipped.
