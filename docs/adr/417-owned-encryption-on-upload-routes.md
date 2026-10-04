# ADR-417: Owned encryption on upload routes

Date: 2026-10-04
Status: Accepted

## Context

Application upload routes still write without a customer encryption contract.
Bucket defaults need every owned write entry point to capture and verify the
selected encryption identity. Route receipts already have immutable snapshots
and recovery, but route policy and the streaming edge do not use them.

## Decision

Allow an optional owned encryption selection on route create/update. Resolve
and persist its immutable private enrollment snapshot against the owned ready
bucket. Expose only the public selection in route and receipt responses.
Encrypted routes require a tracked encryption-capable provider and matching
bucket write authority. Caller headers cannot override the route's selection.

New tracked receipts atomically compare their captured snapshot with current
route policy; a changed policy rejects stale admission. Database insertion
checks prevent older route writers from dropping encryption or using legacy
untracked receipts. Accepted writes keep their frozen identity when route policy
changes or is removed. Idempotent replay returns the saved receipt without a
new key probe, write or capacity charge.

Check key state and enrollment before the native mutation. The encrypted write
recorder meters and commits the dispatch fence after validation and key checks.
Exact encryption acknowledgment is required for settlement. Lost or ambiguous
acknowledgments retain the dispatched receipt; restart recovery verifies the
saved proof without replaying the body, including with ingress or KMS disabled.

## Consequences

Route encryption uses the existing write/recovery ledger and conservative
accounting. Legacy providers cannot accept encrypted routes. Bucket defaults,
GCS encryption/proof, direct-write provenance and other ledger gaps remain
separate increments. Rollback requires clearing encrypted route policies and
draining their pending receipts. No real provider environment is required.

## Acceptance

Local control API → authenticated upload route → native HTTP fixture tests pass
with memory/PostgreSQL, concurrent replay, policy changes, ownership, current
single-PUT limits, key failure, acknowledgment ambiguity and restart recovery.
State/database old-writer and rollback guards, focused races, related state/API
regressions and the full provider/gateway suites pass. The standalone Go SDK,
Node smoke/unit suite (109 tests plus 7 generator checks), and Python suite
(108 tests) pass. Schema/SQLC and Node/Python generation match across 3,842
files; OpenAPI lint, SDK coverage, repository gates and zero-issue changed-line
Go lint pass. Evidence is recorded in the local qualification manifest;
no provider environment, deployment or push is part of this acceptance.
