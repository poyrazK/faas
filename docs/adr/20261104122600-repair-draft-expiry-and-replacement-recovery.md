# ADR-20261104122600: Repair draft expiry and replacement recovery

- Status: accepted for internal operator qualification
- Date: 2026-11-04
- Extends: [replacement plans](2026101702-repair-replacement.md) and [outcome retention](20261104111500-repair-concurrency-and-outcome-retention.md)

## Problem

An abandoned hosted repair draft stays pending after its fixed source expiry,
holding account operation capacity indefinitely without explicit reconciliation.
A replacement already saved in the application database can also become
unrecoverable when its expiry passes and its account loses plan eligibility or
fills its pending quota. Finally, completing an old replacement releases the
parent's retention fence with an already elapsed result window, preventing a
retry after a lost completion response.

## Decision

An unapproved `prepared` repair with an elapsed source expiry and no active
rehearsal no longer consumes pending-operation quota. Admission and diagnostics
use the same expiry rule. Each admission uses one observation time for the
quota calculation, parent slot transfer and child construction.

The existing minute-based apid operation maintenance loop marks these drafts
failed in bounded batches, including when artifact storage is unconfigured.
The operation retention helper uses the same expiry path. PostgreSQL locks each
selected operation and skips rows held by foreground work. Both the operation
projection and durable native task table must show no active rehearsal. The
sweep emits a failure event once and retains
ordinary result/event deadlines and the fixed source expiry. Other phases,
saved approvals, native tasks and unresolved outcome reservations are excluded.

Reserving a replacement from an expired draft preserves its failed operation
state while pinning the reserved identity. It cannot manufacture a pending slot
to transfer to a fresh child. Fresh children still require current plan
eligibility and pending capacity.

After validating the exact reserved parent, digest, binding, source and
selection, recover an expired child as a historical failed operation even when
the current account cannot admit new work. Its stored parent plan limits supply
the result/event retention window. No approval, execution authority or extended
source expiry is granted. Parent completion and child creation remain atomic.

Successful replacement completion starts the parent's ordinary result/event
retention windows again using its stored limits. Retries return the same child
without extending either result window. Settled records remain eligible for
normal pruning afterward. These are control-plane changes and require no new
application-database schema upgrade.

## Validation

Memory and PostgreSQL tests fill the account quota with expired drafts and admit
new work before the sweep catches up. They verify expiry events, protected
phases and native tasks, and exercise replacement reservation at full quota.
Late replacement recovery is tested after plan downgrade and with full pending
capacity, including rejection of fresh executable children, exact replay,
preserved source expiry, renewed parent retention and eventual normal pruning.
