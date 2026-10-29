# ADR: Event routing failures enter the dead-letter queue before their receipt is pruned

Date: 2026-10-26
Status: Accepted
Amends: [ADR-179](179-unified-dead-letter-ledger.md) (a new ledger source)
Related: [ADR 20261023215834](20261023215834-event-plan-caps.md) (settled receipt windows), [ADR 20261026010000](20261026010000-event-reacceptance-delivery-identity.md) (re-publish after prune)

## Context

A GA blocker from the 2026-10-25 event audit. When an event recipient runs out
of routing attempts, its deadline expires or its target is gone, the failure
is recorded only on the event's receipt (`event_fanout_outbox`) and in the
scheduler's logs. A failed recipient counts as settled, so the receipt is
pruned with the plan's settled window (1, 3, 7 or 14 days) unless a recovery
or backfill job holds it. After that nothing shows the event was ever lost:
the unified dead-letter queue takes invocations, trigger records, webhook
deliveries, job runs and workflow runs, not routing failures, although the
docs send customers to `gregale dlq` for terminal failures and promise dead
letters for at least 30 days.

## Decision

1. **A new ledger source, `event_route`.** One `dead_letter_events` row per
   failed recipient of a pruned receipt:
   - `account_id` is the receipt's account; `app_id` the recipient's app while
     it still exists in that account, otherwise NULL (the account view still
     lists it), matching the failed-delivery listing;
   - `source_id` is `md5(outbox_id || '/' || subscription_id)::uuid`, so a
     second capture of the same recipient is `ON CONFLICT DO NOTHING`;
   - `origin` is `event_subscription`, or `event_workflow` for a workflow
     recipient;
   - `event_payload` is the published envelope; `headers` is `{}`;
   - `error_kind` is the recipient's `failure_code` (`unknown` when an older
     receipt has none); `error_detail` carries the subscription, event source,
     id and type, acceptance time, last error, retry stop reason and the
     `retryable` signal; `retry_count` is the routing attempts;
   - `first_failed_at` and `last_failed_at` are the failure time, so the
     ledger's 30-day purge counts from the failure, as for other sources.
2. **Captured when the receipt is deleted.** A `BEFORE DELETE` trigger on
   `event_fanout_outbox`, `WHEN (OLD.last_error IS NOT NULL)`, writes the rows,
   so every schedd version that prunes captures them and receipts without a
   failure pay nothing. It writes nothing while the account is gone or
   pending deletion (account purge and its cascades).
3. **Not replayable.** The receipt is gone and no invocation exists, so there
   is nothing to redrive. Replay answers `409 dead_letter_not_replayable`;
   replay-all (app, account and dashboard) passes these rows by, so they never
   hold its page; the dashboard offers only Discard. Delete, purge and the
   30-day retention apply as for other sources. While the receipt is
   retained, `gregale events replay` and `events recover` keep recovering the
   failure selectively; after that, re-publishing the payload delivers the
   event again (ADR 20261026010000), to every current matching subscription.
4. **Two releases.** The previous release refuses an unknown source on replay
   (`unsupported dead-letter source`, a 500) and its replay-all aborts on the
   first such row. This release lets the ledger hold `event_route` rows
   (widened source check), reads, refuses and skips them, and adds the value
   to the API's `source` enum; nothing writes one yet. The next release adds
   the capture trigger and the in-memory store's equivalent, and the customer
   docs.

## Consequences

- A routing failure stays inspectable for 30 days from the failure on every
  plan, and its envelope can be re-published, instead of vanishing after
  1–14 days.
- Ledger rows are not event storage, so they do not count against the plan's
  retained events or bytes.
- Pruning a receipt with failures inserts one row per failed recipient in the
  prune's transaction; receipts without failures skip the trigger.

## Rejected alternatives

- **Pin failed receipts until they are handled.** They count against event
  storage, so one broken subscription could push the account into publish
  429s.
- **Capture at failure time.** It adds work to the routing hot path, and a
  failure recovered while its receipt is retained would leave a stale entry;
  the receipt already shows it during the window.
- **Make the entry replayable by re-accepting the event for one
  subscription.** It needs a targeted publication path (ordering lanes,
  storage accounting, delivery identity); a follow-up if customers need it.
