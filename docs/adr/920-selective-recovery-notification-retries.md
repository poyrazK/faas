# ADR-920: Selective recovery notification retries

Date: 2026-10-09
Status: Accepted

## Context

Recovery health and notification reports distinguish admission and execution notification delivery failures. Operators currently copy webhook and delivery IDs into separate retry calls. Retrying an uncertain batch response must not capture new failures or repeat a retry after a receiver fails again.

## Decision

Add a read-only job-scoped receiver retry preview and a write-scoped, MFA-protected selective retry action. Targets explicitly name admission or execution, webhook ID, delivery ID, and expected replay generation. There is no implicit retry-all selection. Preview reserves nothing, and incomplete observations remain explicit. A retained exact source-event association can authorize one observed receiver even when historical selection completeness is unknown.

Require a canonical nonzero UUID request ID and one to 100 distinct delivery targets. Canonicalize target order by delivery ID. Lock the owned retained job, consult saved decisions first, and reject changed intent under the same ID. Revalidate each target against the job notification report, current owned enabled webhook, plan eligibility, exact source event, dead state, and generation. Successful, active, missing, disabled, or changed receivers produce independent skipped decisions. Queue eligible receivers by resetting the existing delivery row and incrementing its replay generation. Do not capture notifications, relay outbox entries, recover handlers, or create new receiver selections.

PostgreSQL records all receiver decisions and resets in one transaction, with job ownership locked, current plan and webhook rows share-locked, and delivery rows update-locked. Memory performs the same decision under its mutex and applies prepared mutations only after validation and context checks. Infrastructure errors roll back the batch rather than returning a partially committed selection.

Store at most 100 request receipts in a checked JSON object on the job. Receipts contain canonical intent, frozen response, decision time, and trusted actor identity; no secrets, URLs, payloads, or delivery error bodies. Both queued and skipped decisions remain immutable on repeated requests, even after later delivery failures, successful acknowledgement, plan changes, or webhook removal. To make a new decision, obtain current evidence and use a new request ID. Receipts expire with job pruning; pruned jobs return 404 and cannot recreate old intent. The API emits an audit event keyed by request ID and original decision time for every successful request, including receipt reads.

## Consequences

The CLI accepts an explicit JSON request file so a lost response can be recovered using the same file. Go, Node, and Python expose typed preview and retry methods. Queued means delivery pending, not receiver acknowledgement. Existing at-least-once and consumer deduplication requirements apply; stable delivery and source-event IDs are preserved.

Apply migration `20261009225025256_event_recovery_notification_retries.sql` before deploying API binaries. Operational clone metadata classifies the new column with the recovery job. Downgrade refuses to remove retained receipts; roll back binaries and wait for retained jobs with receipts to be pruned before downgrade.
