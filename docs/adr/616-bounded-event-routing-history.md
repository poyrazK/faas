# ADR-616: Bounded event routing history

- **Status:** accepted
- **Date:** 2026-10-05
- **Decision:** Coalesce repeated capacity waits, bound routing detail per
  retained receipt recipient, and publish explicit history coverage.
- **Why:** ADR-614 can leave a consumer waiting indefinitely. ADR-615 charges
  envelopes and snapshots, while routing observations previously grew on every
  capacity check until the entire receipt was deleted.

## Detail and summary

`pkg/api/limits.go` owns the common ceilings: 128 detail rows and 64 KiB of
logical detail bytes per `(outbox_id, subscription_id)`, error text at 1,024
UTF-8 bytes, failure code at 128 UTF-8 bytes, and thirty-day detail retention.
These are control-plane safeguards shared by every plan, not an execution grant.
Logical bytes charge the UTF-8 subscription, action, state, failure code, error,
and capacity scope, plus a fixed 128-byte allowance per row. They do not measure
physical database size, indexes, compression, or checkpoint/summary storage.

One summary row per captured recipient records observed outcomes, the highest
cumulative capacity-deferral checkpoint, coalesced and compacted counts, wait
first/last times and last scope, and pointers to recent recovery evidence. The
summary has the receipt's cascade lifetime, including indefinitely pending work.
Its size does not grow with polls or replay generations. Repeated consecutive
`fanout_attempt` waits with the same scope update only that summary. A changed
scope, real failure, enqueue/filter outcome, or operator replay records a new
immutable detail row. Counters describe recorded observations, not execution
attempts or distinct worker claims. Repeated checkpoint writes can be observed
more than once. The execution ledger remains governed by ADR-611.

Admission/checkpoint writers already hold the parent receipt lock. They update
summary, insert detail when necessary, compact, and schedule retention in the
same transaction. The admission path still checks its wall-clock lease after
history writes. Stale claims and uncertain committed admission retries cannot
advance this history independently of their checkpoint. MemStore mirrors this
under its mutex. Legacy whole-receipt replay now uses the existing transactional
replay helper, preserving cumulative deferrals rather than dropping them.

## Compaction and recovery

Compaction orders the latest outcome, latest real failure, and latest operator
replay first, then remaining detail newest-ID first. It keeps records within
both ceilings and removes unprotected detail at or before the age cutoff. The
three recovery anchors are exempt from age expiry until superseded or receipt
removal; they remain subject to the hard byte and row ceilings. New bounded
error/code detail and existing UUID recipient IDs let these anchors fit easily.
Exceptionally oversized legacy evidence may be removed by the byte ceiling.
This is retention of useful recovery context, not an exhaustive audit ledger.

The scheduler also runs independent detail retention in batches of at most
50 recipients every ten seconds. It locks parent receipts with `SKIP LOCKED`,
matching routing/replay lock order, then compacts due summaries. An indexed next
prune timestamp avoids scanning all pending receipts or all history on each poll.
A skipped receipt stays due. A receipt with only protected evidence has no next
prune deadline. Pruning changes no payload, recipient snapshot, claim, checkpoint,
retry/failure budget, generation, invocation or event identity.

The additive migration seeds summary counts from retained detail and cumulative
capacity counters from checkpoints. Earlier missing transitions or capacity wait
timestamps cannot be recovered. Seeded summaries are immediately due, allowing
legacy oversized histories to converge in bounded background batches; the next
detail write also enforces their ceilings. Migration does not synchronously prune
all histories. Apply the migration before upgrading history writers and scheduler;
rely on enforcement only after both upgrades. Old binaries and direct operator SQL
can bypass it. Recipient adoption stays opt-in.

## API and cursors

The existing app/event/subscription-scoped routing history endpoint adds:

- `coverage=bounded_recorded_outcomes` and current per-recipient `summaries`.
- Observed, cumulative-deferral, coalesced, compacted and retained-detail counts,
  retained logical bytes, wait first/last times and last recorded capacity scope.
- Highest removed detail ID and latest removed occurrence time. These describe
  gaps, not a contiguous missing prefix: protected older rows can remain.
- Detail capacity scope/count and `details_truncated` for clipped UTF-8 text.

Existing detail IDs and cursor format remain immutable. Pagination uses `id <
before`, including when that cursor's row has been pruned. It may return fewer
or no older rows after compaction and never restarts at the first page. Summaries
are current observations independent of the detail cursor, not a frozen snapshot
of the page. Unknown identities/recipients return empty detail and summaries;
app/account authorization remains the existing API boundary. Responses use
`Cache-Control: no-store`. Both OpenAPI sources and shared Go DTOs are additive
for rolling deployment compatibility.

## Qualification and adversarial review

Memory/PostgreSQL qualification holds a recipient through 20,000 recorded waits,
checks one immutable detail plus accurate cumulative counters, rejects a stale
claim without changing summaries, allows a healthy sibling to progress, and
recovers the blocked recipient. Replay-cycle qualification exceeds detail budgets,
checks UTF-8 truncation and preserved failure/replay evidence, prunes detail while
the receipt is pending, uses an existing cursor after pruning, and recovers before
normal receipt retention cascades both detail and summary. PostgreSQL verifies
that maintenance skips a receipt locked by another transaction. Reapplying the
migration around retained unclassified failure/replay rows checks backfilled
recovery anchors and cumulative deferrals. Existing capacity, atomic admission,
replay and API scope tests remain regression gates.

The adversarial pass considered alternating capacity scopes, repeated operator
replays, multi-byte error text, legacy oversized evidence, concurrent maintenance
and routing, expired/wrong claim tokens, and cursors pointing to removed rows.
Alternating waits/replays are capped on each detail write; same-scope polls only
change fixed-size counters. Parent-first transactional writes prevent summary /
checkpoint divergence and avoid a reversed maintenance lock order. Retention does
not clear failure budgets or discard pending accepted work. Historical timestamps
and counts that were never recorded remain explicitly unavailable.
