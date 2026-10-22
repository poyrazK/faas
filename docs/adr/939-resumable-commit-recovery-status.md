# ADR-939: Resumable Commit recovery status

Date: 2026-10-11
Status: Accepted; production qualification pending
Related: ADR-938

## Context

Recovery status limits receipt observations to fifty items per call. Repeating
a fresh status read restarts at the first item. Large saved apply results need
bounded continuation without rereading successful observations or losing their
timestamps.

## Decision

Add CLI --resume and equivalent Go ResumeCommitRecoveryStatus and Node/Python
resume options. A new status result records the subscriber filter and each
item's original publication descriptor and reviewed plan item as a selection
snapshot. Resume requires the same source/filter, ordered result item coverage,
publication identity and acceptance instant, reviewed selection/action and
saved apply attempt. Preserve precise acceptance instants and routing guards.
Reject missing snapshots and mismatches before receipt reads. Older status
files must be regenerated with a fresh status call before resuming.

Copy caller inputs and preserve completed, active, failed, unresolved and
unsupported-recipient observations, including their evidence and timestamps.
Read not_observed items first, in original result order, then retry read_error
items. The existing 1..50 limit applies to reads attempted in this call; copied
observations do not consume that budget. Clear old partial evidence before a
retry, so missing or replaced evidence cannot inherit a stale success.

Recompute counts, remaining and complete from the resulting rows, ignoring
saved aggregate values. Remaining counts not_observed and read_error rows.
Complete means there are no remaining rows and validation/reads were not
interrupted; it does not mean current delivery completion or resolved request
attribution. Receipt-page limits can still leave unresolved observations.
Timeout/read errors retain partial output and untouched observations.

Source ownership and saved publication identities are revalidated on every
call. Node/Python keep their existing bounded one-page-per-publication
validation refresh; those reads do not replace copied observations. Go
validates source ownership and selection directly. Existing per-item page
budgets, overall timeouts and 64 MiB limits for each CLI input file also apply
to resume. Saved action and history URLs are never followed, and replay is
never submitted.

## Consequences

Large result sets can be inspected over multiple bounded calls. Persistent
read failures cannot starve unread items. Preserved observations remain
historical samples, even if live evidence changed or expired since they were
read. To reread those observations, start a fresh status call without resume.
Unknown saved attempts stay unknown. Selection snapshots detect accidental
mixing of inputs; local files remain editable observations, not signed proof.
Live interruption/retention failure scenarios still require qualification.
