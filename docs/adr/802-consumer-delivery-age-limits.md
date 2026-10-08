# ADR-802: Consumer delivery age limits

- **Status:** accepted
- **Date:** 2026-10-08
- **Decision:** Capture optional application consumer delivery age limits with routing policies and expire stale pending recipients before invocation admission.
- **Why:** Active retry budgets exclude pause, capacity, circuit cooldowns, and worker downtime. Consumers need a separate bound on how old newly admitted work can be.

## Acceptance-time deadline

`max_delivery_age_ms` is an optional routing policy field, from zero to 30 days
in whole milliseconds. Zero and omitted values disable expiry. Manifests use
`retry.max_delivery_age`; CLI configuration uses `subscription-retry-set
--max-delivery-age`. Existing retry APIs and deployment reconciliation own this
configuration. Publication captures it with each candidate. Backfill captures
the current target policy at job creation, and applies it to original event
acceptance time. Producer timestamps cannot extend or shorten the deadline.

Recipient adoption and backfill materialization persist an indexed routing
deadline. Claim selection considers an expired deadline even if backoff,
capacity, manual pause, circuit cooldown, or another ordering lane blocks
normal routing. An active routing lease remains fenced until completion or
lease recovery. Scheduler expiry precedes circuit reservation and retry wait
checks. Whole-receipt scheduling wakes at the earlier retry time or deadline.
Admission checks the captured deadline under its routing locks immediately
before mutation. Existing admitted invocation or cancellation proofs win over
expiry; execution already admitted follows its existing lifecycle.

Expiry records terminal `failed` progress, failure code and retry stop reason
`delivery_expired`, and immutable routing history. It consumes no additional
routing attempt or retry duration. Settling an expired recipient does not
change sibling delivery progress. Pauses remain configured. Workflow and
object notification recipients keep their existing contracts.

## Ordering and recovery

Strictly ordered subscriptions reject a positive delivery age. Validation runs
in manifests, policy updates, and binding updates. Both updates serialize on
the subscription row, and database triggers enforce the same invariant.
Ordered work-policy expiry restrictions remain intact.

Individual replay rejects an already expired captured deadline unless
`allow_expired` is explicit. The override is persisted for that replay generation,
preserved through retry and admission waits, and audited. It does not alter
subscription configuration, sibling snapshots, deterministic invocation identity,
manual pause, or circuit state. Starting another replay requires a fresh explicit
override if the original deadline has passed.

Historical backfill accepts its own explicit `allow_expired`, captured in its
job recipient snapshot. Replay preview marks matching envelopes exceeding the
current subscription limit and reports their deadline and an expired count.
Preview is advisory; live routing rechecks time. Bulk retry and recovery do not
implicitly override delivery age. Their selections exclude stale failures;
recovery items that expire after selection become skipped with reason expired.

Receipt diagnostics show the deadline and override. Consumer health reports
expired deliveries within its existing bounded history window. Expiry remains
part of terminal failures, but is excluded from circuit breaker failure samples
and releases a reserved probe as a neutral outcome. Age policy and snapshots
retain the existing clone and retention contracts; the recipient deadline is
operational state excluded from environment cloning.

Apply the append-only migration and upgrade every routing worker before enabling
age limits. Migration rollback removes age enforcement and deadline storage;
retained history keeps the delivery_expired reason so diagnosis is preserved.
