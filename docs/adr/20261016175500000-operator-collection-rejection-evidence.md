# Historical operator collection rejection evidence

## Status

Accepted. Extends prospective collection history without changing rule
acceptance, recovery, notification, lifecycle or triage policy.

## Context

A retained snapshot_rejected receipt identifies a collection gap but no rule.
The read-only rule-health endpoint observes currently loaded rules, which may
have recovered or changed before an operator investigates a past rejection.
Retrospectively joining current health to old failures would invent evidence.

## Decision

Assess the exact already-read complete bounded source when collection rejects
it. Retain only typed sanitized rule/group observations: definition-derived
hashes, safe names/basenames, closed issue codes, reported state, evaluation
time and whole-second age at attempted_at, evaluation-error presence and safe
repository runbooks. Never retain raw queries, error strings, labels, arbitrary
annotations, full paths, source addresses or arbitrary runbook destinations.
Loaded source totals cover every rule/group before an unhealthy-only preview,
ordered by issue priority then hashed group/item identity. Include at most ten
items and 16384 compact JSON bytes; the byte bound may shorten the preview.
Persist complete unhealthy item counts and explicit truncation. Counts for
pending/firing are null on unhealthy items, never inferred quiet zeros.

Use the existing rule-health assessor without changing collector acceptance.
Diagnostics describe all observed issues, not a proven root cause or a claim
that every issue triggered the first rejection. Known empty rules use
no_alert_rules_observed and known zero counts. Rejection without a matching
rule diagnostic uses collector_validation_failed. In particular, state-layer
ErrInvalidArgument is snapshot_rejected rather than a persistence outage;
ordinary database errors remain persistence_failed.

Add nullable diagnostics JSONB to the immutable outcome receipt. Accept it only
for failure/snapshot_rejected, as an object with the rejected_snapshot scope;
cap PostgreSQL's spaced JSONB text at 32768 bytes. Application validation enforces
the tighter compact bound, typed fields, partitions, closed issues/states,
safe metadata and evaluation ages before write and after read. Store accepted
failure, diagnostics and latest-attempt status atomically under the existing
collector lock/mutex. First accepted outcome at an equal timestamp still wins;
older attempts cannot attach evidence later. Memory storage deep-copies both
inputs and returned diagnostics. Reads use one repeatable-read transaction or
memory mutex. Physical receipt pruning removes diagnostics with it, under the
existing bounded 30-day retention; no separate growing journal is introduced.

No backfill. Legacy reasons, unreadable/malformed/warning source envelopes,
timeouts, non-validation persistence failures and unrepresentable diagnostics
remain unknown/null. An unrepresentable diagnostic projection falls back to the
existing broad failed-attempt receipt, preserving evidence of the attempt.
Failed writes or shutdown still leave missing evidence. The fresh bounded
failure-write context, ambiguous-commit guard and recovery rules are unchanged.

Opt in with details=true on GET /v1/admin/obs/collection-history or --details
on gregalectl obs collection-history. Accept each since/details key at most
once, with details exactly true/false. Preserve operator read scope, MFA,
allowlist and generic errors. The five-second read budget includes a separate
bounded query for at most 20 newest failed attempts inside [since, as_of],
excluding carry-in/future/out-of-retention receipts. Attach failure_details with
the complete failed-attempt count from the same bounded cadence cohort, preview
limit and truncation. Attempts stay separate from coalesced cadence intervals.
Missing diagnostics are explicitly unknown. Zero failures is known empty. The
default history, recurrence and response-time reads never fetch JSONB bodies.
With both bounds the diagnostic preview remains below the CLI section body cap.
The CLI validates sanitized evidence and chronology, rejects malformed replies
without printing upstream bodies, and reports requested-but-absent details as
unknown rather than manufacturing an empty preview. The field is optional for
older-server/default-view compatibility. Synchronize SQLC, canonical/embedded
OpenAPI and Node DTOs. Frontend and rollout remain separately owned.

## Validation

Verify source-time diagnostics survive recovery and a later healthy live read,
with no extra source read during history. Check privacy, empty/invalid sources,
all rule-health classifications, duplicates rejected by state, unreadable
envelopes, timeouts and fallback for unrepresentable dates. Exercise memory and
PostgreSQL atomic failure/rollback, restart, first-outcome guards, deep copies,
retention, carry-in exclusion and preview limits. Add shared conformance for
the new writer. Check endpoint authorization, strict queries, read-only behavior,
CLI text/JSON, malformed/missing evidence and generated-contract drift. Run
migration up/down/replay and constraints, focused regressions, policies and lint.
