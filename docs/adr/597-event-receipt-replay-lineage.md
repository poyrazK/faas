# ADR-597: Trusted handler replay lineage in event receipts

- **Status:** implemented; operational qualification remains pending
- **Date:** 2026-10-05
- **Decision:** Persist immediate-parent and root identity on new generic
  invocation replays, and expose retained replay outcomes alongside the original
  execution in event receipts. Add account-scoped paginated replay history.
- **Why:** ADR-596 can show a failed original invocation after its separately
  created replay succeeds. Operators need to verify each consumer's recovery
  from the same event identity without losing the original failure evidence.

## Admission and retention

The authenticated generic replay handler supplies only the immediate original
invocation ID. The sqlc enqueue statement locks the retained failed/dead-letter
parent and derives the root ID and root creation time from it. Parent and child
must share account, app, captured deployment scope and platform tenant, and the
app must still belong to that account. MemStore mirrors admission under its lock.
Caller-supplied roots, payloads and guest headers cannot establish lineage.

The existing `replayed_from_invocation_id` column now records the parent. An
additive migration introduces `replay_root_invocation_id`, `replay_root_created_at`
and an account/root/created-at/ID index. A CHECK rejects self-parent/root linkage
and malformed root metadata. There is no foreign key to execution records:
independent retention can remove parents while descendants still exist. Root
creation must be at or after the event's durable acceptance, preventing an older
delivery's lineage from attaching to a reused source/event/subscription identity.
Original invocation and cancellation lookups also require creation at or after
this acceptance, so stale execution evidence cannot suggest replaying old work.
Legacy rows are not backfilled from caller-controlled headers.

## Read contract

The recipient's original `execution` and `execution_unavailable` semantics are
unchanged. Optional `recovery` reports `retained_replay_count`, `latest_replay`
and an authenticated `history_url`. Latest is ordered by creation time, then ID.
The original failure remains visible when retained. CLI inspection prints
`recovered` when the latest retained replay completed. A newer failed or active
replay is displayed as such even if an older replay succeeded. This is execution
evidence, not an exactly-once or side-effect guarantee.

Selective handler recovery actions target the latest retained replay when one
exists; active and completed latest replays have no suggested action. Existing
write authorization, idempotency and POST state checks remain authoritative.
Routing replay and in-place dead-letter replay retain their existing semantics.
This decision does not broaden generic replay eligibility for keyed/bound work.
[ADR-598](598-safe-keyed-invocation-replay.md) subsequently adds a dedicated
lane-preserving recovery path for failed unbound keyed work.

`GET /v1/events/receipt/replays?source=SOURCE&id=ID&subscription_id=SUB` requires
`apps:read` or `admin`, normal authentication, MFA and rate limiting. It returns
only trusted descendants of this captured consumer, newest first, with default
100 and maximum 200 rows. `gregale events inspect ... --subscription SUB` reads
the same history, and the Go client exposes `GetEventReceiptReplays`.

Opaque `err1.` cursors bind account, source, event ID, subscription, outbox ID,
and the creation-time/ID position. They need no retained anchor row. Recipient
and replay cursors are separate; malformed, mismatched or replaced-receipt cursors
return 400. Unknown, pruned, uncaptured or foreign targets return 404. Current
app ownership is checked on every read. PostgreSQL reads metadata and replay
evidence in a read-only repeatable-read transaction. Memory reads validate
acceptance identity again under the replay read lock. Pages may reflect changing
execution outcomes and independent pruning between requests.

Counts represent retained execution rows and can decrease. Surviving descendants
remain visible after original/intermediate execution records expire. If all
replays expire, missing recovery evidence cannot prove no replay ever occurred.
This slice does not extend event or invocation retention or enable ADR-595's
recipient-routing adoption flag.

## Qualification

Tests cover mixed independent consumers, repeated replay requests, subsequent
replays, scope and ownership isolation, latest recovery actions, pagination
during replay, missing execution anchors and roots, retention identity reuse,
Go client encoding, CLI text/JSON, and OpenAPI/DTO/route parity. Full CI and
staging qualification through actual handler dispatch remain deployment gates.

Local verification on 2026-10-05 used Go 1.25.13 and PostgreSQL 16.15 on macOS
arm64. Full portable suites passed for `pkg/state`, `pkg/api`, `cmd/apid` and
`cmd/gregale`; focused scheduler event/fanout regressions also passed. Real
PostgreSQL tests passed for event receipts/replay lineage, invocation admission,
lookup and due-work selection. Repository-pinned golangci-lint 2.4.0 reported
zero issues across those four changed packages. SQL regeneration, embedded
OpenAPI synchronization, route/DTO parity, OpenAPI validation, generated CLI
reference and documentation links passed. OpenAPI's existing warnings remain.

Verification initially hit shared disk exhaustion and removal of temporary
build volumes. The final state, scheduler and lint runs used a persistent private
cache; the completed API/CLI and PostgreSQL runs were preserved. Repository-wide
Linux CI and staging dispatch/restart qualification remain unverified locally.
