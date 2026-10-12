# ADR-801: Retain regional minimum-instance billing intent

Status: accepted · 2026-10-11

## Context

ADR-798 requires complete original regional compute observations. ADR-799/800
retain actual instance and task intervals, but the ordinary meter reads today's
app RAM, scaling schedules and deployment minimums. A changed or collected row
cannot reconstruct an earlier promised floor, and a late regional minute must
not silently become zero synthetic usage.

## Decision

Retain app RAM, minimums, scaling floor policy and app status, and deployment
minimums, status, scope and original creation ordering as separate intent
intervals. Capture each relevant committed change and deletion under the same
regional app quota journal lock as issuance and runtime history. Reparenting
locks both roots in canonical order. Read the change clock after those locks.
Unrelated metadata changes do not reset the interval. Keep schedules and their
timezones intact; evaluation uses the billed instant and existing max-of-floor
semantics. An app deletion records the end of its intent without erasing actual
runtime billing evidence. A deployment ending or being collected remains visible
for its original minute rather than being replaced by today's latest deployment.

Hold app and deployment writers while installing the journal. Seed only the
state known at that installation time and retain the boundary; pre-installation
floor coverage is unavailable. History has no collected-row foreign keys.
Refuse direct edits, deletion, truncation and populated downgrade. Source-table
truncation also refuses because it would bypass intent interval capture. The portable
store records only committed map writes; proposal checks and rolled-back bulk
creation or activity mutations leave no history.

## Integration boundary

This is retained customer intent, not a native residency measurement, entitlement
or complete usage minute. The regional producer must combine it with actual
original-resource intervals, resolve deployment/environment eligibility and
allocation attribution, prevent late writes to sealed history, and retain signed
usage for replay. Environment-owned runtime configuration and admission history
must also be reconciled before floor observations are complete. No producer may
infer a zero floor from a missing page or substitute current customer rows.
Existing execution fences and native KVM acceptance requirements remain.
