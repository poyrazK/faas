# ADR: Python parked acknowledgment-intent alert parity

- Status: Accepted
- Date: 2026-10-17

## Context

Go/Node recovery monitors can alert on retained parked callbacks separately from
automatic due/stalled work. Python health already counts that backlog but its
watches lack the corresponding condition. Python needs the same conservative
evidence and checkpoint notification behavior while preserving existing options
and disabled-selection serialization.

## Decision

Add keyword-only `parked_threshold` to frozen Python watch options, preserving
existing positional arguments in ordinary and inherited checkpoint options.
Accept native integer counts zero through the health scan limit; zero/omission
disables it, and a parked-only selection is valid. Snapshot the scalar alongside
other normalized settings before async work. Route metric `parked_intents` to the
existing bounded health `parked` count. Reuse fresh, complete, advancing evidence
and sustained confirmation for opening/recovery. Failed, invalid/truncated,
stale, repeated/regressing or gapped observations cannot confirm a condition,
even when an incomplete lower bound exceeds its threshold.

Use Python checkpoint version 2 exactly when parked alerts are enabled. Append
the enabled condition after due/stalled and retain the positive threshold in
saved policy. Centralize saved policy reconstruction/serialization: disabled
parked alerts omit the new field and preserve version 1 keys/order. Bind version
to the normalized selection; version 1 cannot enable parked alerts and version
2 cannot omit/disable them. Preserve strict native integer validation and reject
malformed saved policies. Older Python validators reject version 2. Resume
requires the same owner/effective policy; selection changes require a new session
after fenced reconciliation and retention of old incidents/checkpoint/pending
notifications, without rewriting a saved version or synthesizing resolutions.

Reuse the Python-specific kind, canonical microsecond timestamps, incident IDs,
safe sequences, bounded outbox, reserved resolutions, startup replay before
health reads and exact all-or-nothing acknowledgments. Retain detached callback,
error and cancellation snapshots, joined cooperative cleanup and fresh proof on
resume. No new state machine, transport, timer proof serialization or schema is
introduced. A parked resolution describes only a sustained lower count; operator
budget resume or supersession can cause it without callback confirmation.

## Consequences

All three SDKs expose equivalent parked alert conditions in their own checkpoint
formats. Upgrade every host that can resume or acknowledge a version 2 Python
checkpoint before enabling the option. Applications retain fenced atomic storage,
idempotent notification delivery, nondestructive acknowledgment receipts and
bounded acquisition/driver/callback lifetimes. Monitoring stays read-only and
does not grant claims, resume recovery, execute business work or send platform
callbacks. Monitoring versions remain independent of retry-policy versions.

## Validation

All six modified Python sources parse for Python 3.11, and Ruff passed. Isolated
generation excluding tests produced 6528 byte-identical Python/SQL/typing files.
All seventeen package module/marker/SQL sources match source bytes in both wheel
and sdist, with current README metadata. AST/source inspection confirmed 22
existing declarations, legacy field declarations/order, a disabled keyword-only
new option and retained validation/checkpoint control flow unchanged beyond
policy/version selection. Health SQL/classification, root exports and intent DDL
remain byte-identical. ADR uniqueness, documentation routes, local links/anchors
and whitespace checks passed. No tests or assertions were
added/changed, and no local tests or SDK helper smoke calls ran. Hosted
qualification remains pending for parked-only/mixed conditions,
threshold reversals, incomplete/stale/out-of-order evidence, restart replay,
exact acknowledgment ordering, capacity, version skew, operator resume/
supersession, fenced storage and cancellation/notification uncertainty.
