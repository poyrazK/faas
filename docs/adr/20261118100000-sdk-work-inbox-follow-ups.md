# ADR: Resolve current, scope-aware inbox follow-ups

Status: Accepted (SDK preview)

## Context

Inbox entries provide stable receipt references and lifecycle hints. An agent
that has discovered an entry still needs to choose an operation, construct its
arguments, and account for changes since discovery.

## Decision

Offer a typed SDK resolver and an opt-in toolkit operation
`work_inbox_follow_up`. Resolve the reference with an exact, owner-scoped
receipt lookup and a fresh authorized inspection. Honor any supplied business
reference. Reuse existing receipt keys, business authority, and toolkit
publication and scope checks. Require the registered destination to accept the
expected request or action ID.

Return a human review link, an explicit tool call with exact arguments, or a
no-action outcome. Select the operation based on current state. Require its
scopes before advertising a call and require status scopes before returning a
review link. The caller invokes the destination in a separate authenticated
request. The existing operation owns authorization, approval, and idempotency.

## Evidence

Shared cases cover exact receipt resolution, scope and owner checks, unsafe
links, and stale state. The CSV journey discovers work on both MCP hosts,
opens a human review, resolves an uncertain refund, and reads its current
result after reconciliation. The provider fixture records one submission.
