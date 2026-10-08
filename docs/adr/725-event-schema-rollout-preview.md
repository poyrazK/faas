# ADR-725: Read-only event schema rollout preview

- **Status:** accepted
- **Date:** 2026-10-08
- **Decision:** Expose bounded consumer version coverage and payload validation before an event schema rollout.
- **Why:** Immutable schema registration and per-consumer version selection need a way to inspect rollout gaps without publishing events or registering a proposal.

## Request and validation

`POST /v1/event-schemas:preview-rollout` requires the existing read surface
(`apps:read` or `admin`), MFA, and an authenticated account. It accepts a
concrete source/type pair and version. An optional `schema` supplies a proposed
Draft 2020-12 definition; omission reads that account's registered version.
Missing registered versions return 404. Invalid definitions, external references,
oversized requests, and invalid ranges return 400. The endpoint has a 15-second
request timeout and a 2 MiB body limit. Schema compilation uses the same validator
as publication; proposals are never registered.

The request can contain up to 20 event data samples of at most 64 KiB each.
Results include validity and the first failing field path and bounded reason.
Payload values are not returned. A schema digest identifies the checked bytes.
There is no compatibility proof or automatic rollout decision.

## Consumer coverage

Observe up to 1,000 enabled ordinary application subscriptions whose source/type
patterns match. Report subscription/app identity, captured configuration as
observed now, whether the proposed version is accepted, and whether a content
filter is present. Empty version selection accepts all versions. Version
acceptance does not evaluate content filters, expiry, live controls, or target
health and does not guarantee delivery. Workflow and object notification
subscriptions are outside this application consumer coverage.

A bounded continuation probe sets `consumers_truncated` when additional
consumers exist. Counts then describe only the returned observations and are
lower bounds. Configuration may change during or after the observation.

## Retained sampling

Supplying both `from` and `until` requests retained sampling by platform acceptance
time in `[from, min(until, observed_at))`. Scan the newest 1,000 account receipts
in that range, including unrelated sources and types. Within those candidates,
read at most 100 matching source/type payloads (or the lower `retained_limit`).
Bound each envelope transfer to 64 KiB plus an overflow sentinel and stop
validation at a 4 MiB aggregate byte budget. PostgreSQL uses the account acceptance
index, sqlc queries, and a read-only repeatable-read transaction for the candidate
and payload reads.

Validate the original event **data** against the proposed schema regardless of
its original schema version. Separate oversized, malformed, invalid, or
inconsistent envelopes into `unreadable_count`; they are never counted as valid
or as schema-invalid payloads. `examined_count` equals valid, invalid, and unreadable
results. Limit exhaustion sets `truncated`. `scanned_count` includes unrelated
account receipts and can exceed `examined_count`.

`history_complete` is always false: retention, pruning, scan bounds, and recent
sampling mean this cannot certify historical compatibility. An empty matching
sample says nothing about compatibility. Retained receipts and deliveries are
not changed, pinned, replayed, or admitted. No database migration is needed.
