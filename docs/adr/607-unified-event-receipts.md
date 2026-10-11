# ADR-607: Unified event receipt inspection

- **Status:** implemented; operational rollout qualification remains pending
- **Date:** 2026-10-04
- **Decision:** Add an account-scoped, read-only receipt API and CLI over the
  existing acceptance snapshot, routing checkpoints, invocation ledger, and
  cancellation receipts. Publish returns its stable relative receipt URL and
  original durable acceptance timestamp.
- **Why:** App delivery history contains invocations and terminal routing
  failures, but omits accepted candidates still waiting to route and candidates
  filtered during routing. Operators need to understand the entire event before
  choosing which failed consumer to recover.

## Read contract

`GET /v1/events/receipt?source=SOURCE&id=ID` requires `apps:read` or `admin`,
with the normal authentication, account, MFA, and rate-limit protections.
`gregale events inspect --source SOURCE --id ID` exposes the same evidence;
`--json` preserves the complete response, including selective recovery URLs.
Source and ID jointly identify the event within the authenticated account.
Unknown, foreign, and pruned identities return 404.

The read uses a PostgreSQL read-only repeatable-read transaction across metadata,
recipient pagination, invocation lookup, and cancellation lookup. It creates no
work and does not lease, retry, or replay anything. Memory storage implements
those semantics under one lock. SQL remains sqlc-generated; no new migration is
needed. Neither payloads nor filters, work-key digests, or lease tokens are exposed.

Pages follow immutable acceptance-snapshot positions, with a default of 100 and
maximum of 200 recipients. The opaque cursor binds account, source, event ID,
and outbox receipt ID. Routing progress and replay cannot reorder a page; counts
always cover the whole snapshot. Outcomes can change between requests, so pages
are not a frozen historical view. Malformed or mismatched cursors return 400;
a cursor for an identity reused after retention cannot silently resume the old
receipt. Legacy receipts without snapshots explicitly report unknown membership.

## Outcome and recovery semantics

Each captured recipient separates routing from handler execution. Routing
reports pending, processing, filtered, enqueued, or failed, cumulative attempts,
and available retry/error evidence. Independent routing also exposes the current
generation, budget attempts, and lease expiry. Whole-event legacy routing exposes
its retained checkpoints; a pending checkpoint can coexist with an active parent
worker. Replay counts and the routing history URL preserve recovery evidence.

`enqueued` means routing was handled. Usually this creates an invocation; a
captured work-policy `cancel_pending` operation instead creates a durable
cancellation receipt. The response preserves this distinction and the actual
superseded, cancelled, expired, dead-letter, failed, or completed invocation
state. Missing execution evidence after enqueue is `record_unavailable`, not a
claim about handler success. Original invocations may expire independently of
routing receipts. The settled timestamp covers routing, including filtered and
failed recipients; it does not cover handler completion. Its retention boundary
is thirty days later; unfinished routing has no settled retention boundary.

The original invocation ID uses the existing JSON-array UUID derivation from
account, source, event ID, and captured recipient ID. The original envelope's
account spelling is preserved in lookup, exactly as in both scheduler routes.
Routing recovery and in-place dead-letter replay therefore remain visible here.
Generic invocation replay creates a new invocation without trusted parent
lineage in the current Store model; those rows remain available in app delivery
history. This change does not infer lineage from caller-controlled headers.
[ADR-608](597-event-receipt-replay-lineage.md) subsequently adds trusted parent
and root lineage for new generic replays, including recovery summaries and
paginated history on this receipt surface.

The response supplies applicable selective POST actions: recipient routing replay,
in-place dead-letter replay, and generic failed-handler replay for plain unbound
work. Dead-letter actions address the retained unified DLQ record, including
later handler replays; purged records have no recovery action. It does not offer
generic replay for keyed or queue-bound work because that
endpoint does not preserve their scheduling lane. Active sibling routes do not
prevent independent recipient recovery; legacy replay still waits for parent
settlement. Actions remain subject to the existing write scopes and current
state checks at POST time. Reads do not grant write authority. Captured target IDs
remain historical evidence; current app names and execution metadata are omitted
when the app belongs to another account. Deleted or unavailable targets have no
recovery action.

## Selective CLI recovery

`gregale events recover --source SOURCE --id ID --subscription SUB` selects one
retained captured or backfilled recipient using bounded receipt pages. It uses
the advertised routing, plain handler, keyed handler, or unified dead-letter
action through the existing Go client replay methods. Handler actions target
the latest eligible retained replay when one exists. The CLI validates known
methods, endpoints, and selected identity before making a selective POST;
unsupported or ambiguous actions fail without a mutation.

`--dry-run` performs only receipt reads and reports availability. It exits zero
for an inspected recipient with no available action; actual recovery exits
nonzero in that case. `--json` reports identity, dry-run flag, status, action,
and the accepted replay result or unavailable reason. Successful consumers,
active deliveries/replays, cancellations, unavailable evidence, and already
admitted workflows are explained without inferring a replay from execution state.

The endpoint retains write-scope, ownership, deadline, lease, and replay
identity checks. An action can become stale between GET and POST; the CLI
reports rejection without attempting a different replay path. Acceptance of
recovery does not assert execution completion or exactly-once side effects.

## Independent workflow routing recovery

ADR-648 extends recipient routing ownership to captured workflows and mixed
receipts. The same `routing_replay` action recovers a failed workflow recipient
without waiting for pending siblings. `gregale events recover` selects this
action by the captured recipient ID. Run and step APIs own recovery after
workflow admission; routing replay preserves one run per accepted recipient.

## Qualification

Selective CLI recovery tests cover all four action kinds, dry-run isolation,
recipient pagination, unavailable evidence, unsupported/ambiguous actions, and
stale POST rejection. CLI event, help, completion, and generated-reference
checks pass with the race detector. The PostgreSQL publish-to-handler recovery
integration is described in [ADR-647](647-independent-event-routing-default.md);
its HTTP consumer and VM bridge are fixtures, so native guest and fleet staging
qualification remain required.

Memory and real-PostgreSQL tests exercise mixed routing/execution outcomes,
whole-snapshot counts, pagination during replay, scoped lookup, cancellation and
supersession, retention and identity reuse. API and Go client/CLI tests cover
encoded identities, stable publish acceptance, read scopes, selective recovery,
and text/JSON inspection. Existing fanout recovery tests protect invocation
identity and independent routing behavior. Repository contract gates cover SQL
regeneration, OpenAPI/DTO parity, embedded spec synchronization, and docs links.

This slice does not enable the ADR-606 adoption flag. Staging qualification of
publish through actual handler execution, failure, dead-letter recovery, restart,
and operator concurrency remains necessary before enabling adoption in production.

Local verification on 2026-10-05 used Go 1.25.13, PostgreSQL 16.15, and the
repository-pinned golangci-lint 2.4.0 on macOS arm64. Receipt state tests passed
in memory and PostgreSQL, including active leases, filtered outcomes, unavailable
execution records, routing replay, keyed cancellation/supersession, tenant
isolation, pagination, and retention. API, Go client, CLI, publish, and
OpenAPI-to-DTO parity tests passed. Existing event routing and object event
regressions passed. Focused lint across the changed packages reported zero
issues; SQL regeneration, OpenAPI validation, embedded spec synchronization,
and documentation links passed. Concurrent builds exhausted shared disk space
and invalidated the shared build cache during early attempts; successful runs
used an isolated cache and temporary storage. Repository-wide Linux CI and
staging execution/recovery acceptance remain required.
