# ADR-1162: Idempotent bulk lineage cancellation receipts

Status: Accepted
Date: 2026-10-13
Related: ADR-1155, ADR-1161

## Decision

Extend cancellation's existing request with optional nonzero command_id and pass
it through the same atomic control-receipt path as pause/resume. The shared job
lock serializes scheduler progress, cancellation and receipt insertion. Ownership,
retention and original created_at remain authoritative before receipt lookup.
Same UUID/action/creation guard returns the immutable original receipt alongside
current job observations; reuse for another action returns the existing command
conflict. Cancellation preserves committed evaluations and marks only remaining
items cancelled, without changing the evaluated count or original expiry.

Preserve cancellation's existing terminal no-op behavior. A new command UUID on a
completed or cancelled job records that successful acknowledgment without changing
state or updated_at. Its receipt result_status is completed or cancelled. Earlier
pause/resume receipts remain replayable after cancellation, without reapplying the
recorded action. New pause/resume commands still reject terminal jobs.

Cancellation shares the 128-receipt cap and sequence with pause/resume. At the cap,
new UUID-keyed cancellation returns 429 and commits neither state nor receipt.
Known command repeats and cancellation without command_id remain available.
The CLI exposes optional --command-id; generated SDKs accept it and decode cancel
receipts through the existing control-history endpoint. No new route or columns
are required, and legacy request/response shapes remain compatible.

An append-only migration broadens the receipt action CHECK to include cancel.
Apply it before enabling UUID-keyed cancellation on API writers. Downgrade restores
the narrower CHECK and intentionally fails while any cancel receipt remains;
stop UUID-keyed cancellations and expire/prune their parent jobs first. Older SDKs
with closed action/state enums need upgrading before reading this history.
