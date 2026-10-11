# ADR-941: Bounded Commit recovery monitoring

Date: 2026-10-11
Status: Accepted; production qualification pending
Related: ADR-938, ADR-939, ADR-940

## Context

Saved recovery observations can be resumed and refreshed, but following a
queued recovery execution currently requires repeated manual calls.

## Decision

Add CLI recovery-status --watch and equivalent Go WatchCommitRecoveryStatus,
Node watchCommitRecoveryStatus and Python awatch_commit_recovery_status helpers.
Reuse guarded read-only status calls and fixed copied plan/apply inputs.
Go requires a context deadline; CLI and Node/Python default to two minutes
and cap their overall timeout at 24 hours. Poll intervals default to one second
and range from 100 milliseconds to one minute. Existing per-pass read and
recipient-page bounds remain unchanged.

A watch with no saved status begins an observation pass. A supplied refresh
starts a fresh pass with its comparison baseline. A partial resume continues
unread rows first; an already covered resume starts a fresh poll. Subsequent
partial passes resume; covered passes refresh. Wait one interval between calls,
including continuation calls, to bound sustained read frequency. Each refresh
uses the last pass as its baseline. There is no atomic whole-selection sample.

Stop only after observation coverage is complete and every queued attempt has
matches_acknowledged_execution attribution and a retained terminal execution
state: completed, failed, cancelled, expired or superseded. Rejected, skipped
and not_attempted attempts have no acknowledged execution to monitor and remain
explicit in the output. Unknown and read_error attempts never settle a watch.
Unresolved evidence, unsupported recipients and unmatched queued executions
cannot prove a queued attempt terminal. Provide the settled predicate in SDKs;
settled describes monitoring termination, not recovery success.

Overall timeout and cancellation include reads and interval waits. Return the
last sample if interrupted between calls; in-flight read failures return the
partial status supplied by the existing reader. Stop on read/validation errors
rather than concealing them behind automatic retries. ADR-944 adds explicit
opt-in bounded transient read retries. Callers can resume the
attached partial result. Progress callbacks receive detached snapshots after
each successful bounded call; Node/Python callback errors attach the last
result. CLI prints changed row summaries and counts to stderr while final
JSON goes to stdout, also on timeout/interruption. Failed delivery, unknown
attempts, rejected/not-attempted submissions and incomplete status produce a
nonzero CLI exit. Skipped attempts retain existing status semantics.

## Consequences

Operators can follow independently acknowledged recovery executions without
submitting additional replay. Unknown request outcomes stay unknown. No saved
URL is followed. Local snapshots remain editable historical observations;
settlement and delivery success are distinct. Live timeout, cancellation,
retention and multi-pass transitions require production qualification. No local
tests are added or run, and compliance registries are unchanged.
