# ADR-792: Workflow and backfill event backlog discovery

- **Status:** accepted
- **Date:** 2026-10-08
- **Amends:** ADR-617, ADR-639, ADR-648

## Context

ADR-617 made waiting captured application recipients discoverable without
knowing their event IDs. ADR-648 added independent workflow recipient routing,
but workflow admissions stayed outside that projection. Durable historical
backfill also creates independent recipient rows on receipts that may already
be settled at the event level. Operators could inspect these rows only after
finding the event receipt by another route.

## Decision

Extend the metadata-only `event_routing_backlog` projection to include:

- Captured application subscriptions and workflow starts from the acceptance
  snapshot.
- Backfill recipients identified by their immutable receipt position, even
  when the event-level receipt has already settled or the backfill job has been
  pruned.

Each row records `consumer_kind` (`application` or `workflow`), `origin`
(`acceptance` or `backfill`), and the captured workflow name when present. The
API and CLI can filter on kind and origin. Recipient rows keep their existing
acceptance-time ordering and receipt/history links. Consumer summaries and
their independent cursor include consumer kind. Cursor decoding remains
compatible with prior consumer cursors that have no kind field.

The indexed projection is refreshed by the existing outbox and recipient
triggers. The read path continues to query only account-scoped projection rows
and scalar event/app metadata; it does not claim, replay, or return event
payloads. Workflow routing rows describe admission only. Workflow execution
recovery remains on workflow run and step APIs.

## Compatibility and limits

Coverage is now `captured_and_backfill_recipients`. Unattributed legacy receipt
counts retain their account-wide age-window meaning. Backfill origin remains
available after job pruning through `receipt_position`. Removed or transferred
apps do not expose their new owner's slug or make old receipts visible to that
owner. The existing five-second read deadline and independent pagination limits
continue to bound the API.

Historical backfill eligibility and duplicate policy are unchanged. A backlog
row is discovery evidence and does not itself request another delivery.
Application handler execution queues remain on the existing delivery surfaces.
