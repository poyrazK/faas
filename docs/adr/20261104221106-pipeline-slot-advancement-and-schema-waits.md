# ADR-20261104221106: Pipeline slot advancement and schema waits

- **Status:** implemented for local/staging qualification; production disabled
- **Date:** 2026-11-04
- **Amends:** ADR-20261104211004, ADR-20261104134821

## Context

`pg_replication_slot_advance` can move a stopped slot's confirmed checkpoint past
an unconsumed update while its restart position still retains overlapping WAL.
The resumed subscriber skips that update and can apply newer writes. Retained
WAL therefore does not establish that the preceding stream was consumed.

An approved schema workflow awaiting its automatic Data API refresh reports
`blocked/schema_api_refresh_pending`. CLI waits treated this temporary state as
a terminal failure even though the worker could finish without another action.

## Decision

Continue trusting the same active sender, which prevents slot replacement and
explicit advancement while it holds the slot. After that sender changes, accept
only an unchanged confirmed checkpoint with WAL retained through that checkpoint.
An advanced checkpoint from another sender or a stopped slot requires the existing
gated full verification. A current subscriber origin cannot prove that an earlier
gap was consumed. Normal reconnects can also need verification when the last
sender's progress was not observed; PostgreSQL 16 provides no reliable distinction
between that progress and a manual advance.

Preserve the existing fresh proof epoch, atomic read gate, explicit resync on
mismatch, and successful verification without replacing subscription or tables.
No new source privileges, catalog migration, or recovery action is introduced.

CLI waits keep polling through `schema_api_refresh_pending`, including after
resume, with their existing deadline. Actual refresh failures and other current
terminal blocked states continue to fail immediately.

## Validation and rollout

Native PostgreSQL regressions advance an inactive slot while WAL still overlaps
the saved checkpoint. Missed updates require explicit resync and remain unreadable,
including when the new sender already applies newer writes before reconciliation.
An advance without selected-row changes obtains a new proof and preserves table
and subscription identities. Retained-slot recovery, schema proof completion,
stream lease fencing and pause remain covered.
Reader edits and pause retain manual verification intent. Uncertain reconnect
progress may reset its physical ranges only with a fresh stream epoch; subsequent
progress, explicit reruns and cancellation retain their existing ownership rules.

A native approved schema workflow pauses and resumes while refresh is pending,
then reaches live after refresh completes. The CLI regression observes that
pending state before a live observation and succeeds; failed refresh and bounded
wait regressions preserve terminal and timeout behavior. Production admission
remains disabled pending deployed qualification.
