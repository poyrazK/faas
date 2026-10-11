# ADR-612: Durable deduplication for plain invocation replay

- **Status:** accepted
- **Date:** 2026-10-05
- **Decision:** Admit one durable recovery child per failed or dead-lettered
  unbound unkeyed invocation, shared by account and customer self-service replay.
- **Why:** HTTP request-key caching cannot coordinate different operators or
  survive the gap between invocation insertion and response recording. Customer
  replay also lacked stored parent/root lineage and captured deployment scope.
- **Consequences:** Repeated recovery returns the recorded child. Subsequent
  recovery targets that child's failed execution. Retention never authorizes a
  second child from a retained parent.

## Admission and ownership

The existing account and customer self-service replay routes use
`PlainInvocationReplayStore`. PostgreSQL locks the parent invocation and its
current owning app, reads its durable marker, and inserts the child and marker
in one transaction. A failure before commit rolls back both. MemStore mirrors
admission under its mutex. The API no longer wraps account replay in the HTTP
response cache: every request rechecks scopes, configured MFA, rate limits,
parent ownership and current app ownership. Customer tokens additionally
recheck the original tenant association before any durable lookup.

Only `failed` or `dead_letter` plain work with no work policy, queue binding or
named queue is eligible. Existing keyed and queue recovery requirements remain.
The child derives account, app, customer, environment, payload, method, path
and trusted parent/root identity from the locked parent. Execution state,
attempt counters and lease ownership start fresh. Account replay refreshes the
retry policy from the current app and plan; self-service preserves its original
retry policy. Both refresh trace/version headers and execution/result lifetimes.

Duplicate requests return the recorded child, including its successful,
failed, cancelled or active state. Headers and request keys cannot change its
identity or create another execution. Durable lookup precedes new-admission
checks, so an expired deployment pin or suspended customer does not erase an
already accepted replay. A new recovery still requires valid pins and an active
customer. Normal dispatch rechecks deployment pins. There is no global ordering
or exactly-once guarantee for application side effects.

## Retention, upgrades and receipts

`invocation_plain_replays` references its parent with cascading deletion. The
child ID has no foreign key, so child pruning leaves the parent's marker.
It records the child's creation time and validates account, app, customer,
environment and lineage before returning it. A reused ID cannot substitute
another invocation for the original child. Missing or mismatched children
return 409 `invocation_replay_unavailable`. Parent pruning removes its marker;
surviving failed descendants can recover using their stored root identity.

The additive migration adopts the latest retained direct child whose stored
ADR-608 lineage, owner, customer and environment match its parent. Historical
forks remain visible. It neither infers lineage from guest headers nor adopts
legacy customer replays without stored parent/root identity. Reapplying the
migration preserves an existing marker, including after child pruning.

Receipts preserve the original failure and latest retained recovery history.
They suppress `handler_replay` for a failed execution with an accepted child,
even after that child is pruned. Keyed actions retain ADR-609 semantics.
Routing replay and in-place dead-letter generation replay remain separate
operations with their existing contracts; this change deduplicates creation of
plain recovery children through the invocation replay routes.

All API writers must adopt this admission protocol before relying on the
one-child guarantee. Older binaries and internal low-level invocation insertion
can bypass it; the migration does not remove historical forks or impose a
unique parent constraint on the execution ledger.

## Qualification

Required coverage includes concurrent requests with distinct or absent keys,
account/customer coordination, captured identity and lineage, subsequent failed
child recovery, successful-child duplicate reads, expired deployment pins,
current app ownership, rollback between child and marker insertion, child and
parent retention, same-lineage child-ID reuse, and populated migration upgrades.
Receipt tests cover both latest retained and original parent suppression after
pruning. Generated schema/SQL and API documentation must match regeneration.

Local verification passed with Go 1.25.13 and PostgreSQL 16.15: concurrent
plain/keyed replay and receipt regressions; atomic rollback, current ownership,
parent/child pruning and child-ID reuse; populated upgrade and migration replay
safety; both HTTP routes, expired pins and mixed-consumer recovery; full portable
state and Go client suites; OpenAPI compliance and embedded-spec parity; fresh
schema parity, sqlc 1.31.1 regeneration, migration shape/uniqueness checks, docs
links and changed production-code lint with golangci-lint 2.4.0 (zero issues).
The embedded OpenAPI copy was also brought up to date with ADR-611's attempt
history surface.

Initial broad test builds hit local disk exhaustion during linking. Final
runs passed after space became available, with stripped test-binary debug data.
The populated-upgrade test caught the backfill predicate treating an unnamed
queue as NULL; the schema stores an empty string, and the corrected upgrade and
reapplication passed. Full Linux CI and staging delivery qualification remain
release gates. The independent recipient-routing feature flag stays off.
