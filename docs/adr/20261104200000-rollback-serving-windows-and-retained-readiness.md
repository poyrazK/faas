# ADR-20261104200000 · Preserve rollback serving history and retained readiness

- **Status:** accepted
- **Date:** 2026-11-04
- **Amends:** [ADR-1448](1448-releases-that-change-the-database.md),
  [ADR-601](601-binding-checked-historical-rollback.md),
  [ADR-20261104140000](20261104140000-rollback-execution-fences-and-outcomes.md),
  [ADR-20261104160000](20261104160000-graph-and-service-rollback-safety.md).

## Serving windows

Each newly published graph records the beginning of its uninterrupted graph
routing interval. Replacing a graph carries that timestamp forward. Deactivating
it checks migrations after the fallback member last actually served, using the
later of this interval boundary and any retained graph serving history for that
member. Republishing identical members cannot manufacture a new safe boundary.
Retirement records its serving end under the app lock and extends member pins
to the same stored graph expiry, preserving the full compatibility window.

An additive nullable `project_release_sets.fallback_hidden_at` column stores the
interval. Older writers leave unknown history as NULL; current rollback readers
use the target's creation time as a conservative bound in that case. Existing
heads cannot prove a fresh boundary merely by being republished. This may refuse
an older fallback when its compatibility with past migrations cannot be proved;
operators must choose compatible code or explicitly review an artifact recovery.

Release tasks record a start no earlier than their actual store admission under
the app lock. PostgreSQL supplies its own clock after lock acquisition; MemStore
uses its clock under the mutex. A scheduler observation captured before waiting
cannot cause a later migration to disappear from rollback history. Caller times
in the future remain conservative, preserving existing clock/test contracts.
Other command kinds retain their existing timestamp semantics.

## Retained checked targets

A zero-traffic deployment that is still live and has serving history or an
existing retained alias keeps its artifact readiness when selected for a
checked rollback. It remains live and the operation starts ready. Previously
superseded and unretained never-served dark targets still go through preparation
and smoke readiness. Artifact retention is checked in either case. Current API contracts,
bindings, release history, exact current selection and service capacity remain
mandatory at routing. Services start a fresh handoff/readiness clock.

This preserves existing revision URLs while qualification is pending. A failed
operation resets its rollout marker but does not supersede a live target held by
an unexpired revision pin, a retained graph or durable work. Terminal intent is
saved before retention is evaluated, so it cannot retain itself indefinitely.
The prior artifact completion proof stays available for another retained retry.

## Historical plain outcomes

Before preparation resets a target's completion timestamp, both stores finish
any pending plain receipt whose success that timestamp proves. Each completion
keeps its original time, nonnegative recovery duration and committed routing
outcome audit. Recording another request also preserves a still-visible success
before superseding unresolved requests. Outcome writers take the parent app lock
before receipt/deployment locks, matching preparation and preventing lock cycles.

## Validation and rollout

Regression tests exercise repeated graph publication, checked deactivation with
binding enforcement, delayed scheduler timestamps, retained revision resolution,
failed checked cleanup and repeated requests before outcome reconciliation on
MemStore and PostgreSQL. Normal rollback controls remain covered. Apply the
additive migration before the updated control plane and scheduler. No public API
fields, quotas or VM protocol changes are required.
