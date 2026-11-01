# ADR 20261031104048 · App error write budget

- **Status:** proposed
- **Date:** 2026-10-31
- **Amends:** ADR-096 (automatic error grouping: one `app_errors` upsert and
  one `app_error_requests` row per erroring request)
- **Related:** bug hunt 2026-10-28 H41

This record has a date-and-time identifier because numbered ADRs collide
across parallel branches.

## Context

gatewayd-internal records every 4xx and 5xx response of an app it has
resolved, scanner 404s and failed-auth 401s from anonymous traffic
included. Each one became two control-plane Postgres writes: an upsert of
the error group's `app_errors` row and an `app_error_requests` row. The
gateway's 4,096-row ring only bounded its memory. The publisher flushed as
soon as 256 rows queued, so the write rate followed the error rate up to
apid's capacity, and one hot error group serialized on one row lock.
`app_error_requests` kept a row per erroring request for the retention
window, and the per-plan `AppErrorsMaxRequestRowsPerFingerprint` cap was
declared but never read.

## Decision

1. **The gateway folds repeats.** Until the publisher drains it, a repeat of
   an app's error adds to the count of the newest queued record for that
   error. Up to `api.AppErrorsRequestSamplesPerFlush` (3) of an error's
   requests per flush get their own record and request row.
2. **Each app has a request-row budget.** A gateway sends at most
   `api.AppErrorsRequestRowsPerAppPerMinute` (60) request rows per app per
   minute. Past it, the first record for an error in a flush only counts
   requests (`request_row_omitted`).
3. **The wire carries the count.** `IncrementAppErrorRequest` gains `count`
   (0 from an older gateway means 1) and `request_row_omitted`.
   `IncrementAppError` adds the count to `count` and `request_count`.
4. **apid enforces the plan cap.** A request row is not stored once its
   error group holds the account plan's
   `AppErrorsMaxRequestRowsPerFingerprint` rows. The plan is read the way
   HTTP issue ingest already reads it.

## Consequences

- Control-plane writes from one app's errors are bounded by its error
  groups per flush plus 60 request rows a minute per gateway, whatever the
  error rate. A flood of 2,000 identical 404s costs three records.
- Error counts stay exact. The drill-down shows samples, not every request.
- Release order: apid (control plane) before the gateways (edge). An older
  apid ignores the new fields, so during the skew a folded record counts as
  one request.
- A count-only record also skips HTTP issue ingest; issues still see the
  sampled requests.
