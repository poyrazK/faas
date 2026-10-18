# ADR: Node parked acknowledgment-intent alert parity

- Status: Accepted
- Date: 2026-10-17

## Context

Go monitors can alert on retained exhausted callback intents independently of
automatic due/stalled recovery. Node health already counts parked work, but its
watches cannot signal that operator backlog. Node needs the same sustained
evidence and restart-safe notification behavior without changing disabled-policy
checkpoint serialization.

## Decision

Add optional `parkedThreshold` and metric `parked_intents` to ordinary and
checkpointed Node health watches. Accept integer counts zero through the health
scan limit; zero/omission disables it, and a parked-only selection is valid.
Copy normalized scalars before the first await and freeze the effective policy.
Route this metric to the existing health `parked` count. Reuse complete, fresh,
advancing evidence and sustained confirmation for opening/recovery. Failed,
invalid/truncated, stale, repeated/regressing or gapped evidence cannot confirm
even an above-threshold lower bound and leaves retained incidents unknown.

Use Node checkpoint version 2 exactly when parked alerts are enabled. Include
the positive threshold in the complete policy and append the enabled condition
after due/stalled. Validate version against the normalized policy, condition
order and retained notification lineage. With parked alerts disabled, omit
`parkedThreshold` from normalized policy and retain version 1, existing field
order, due/stalled behavior and acknowledgment format. Older validators reject
version 2; no automatic format translation occurs. Resume requires identical
effective owner/policy. A changed selection needs a new session after fenced
reconciliation and retention of the old incidents/checkpoint/pending entries.

Keep the existing Go-independent Node kind, incident IDs, safe sequences and
notification shape. Reuse the bounded outbox, reserved resolution capacity,
startup replay before health reads, exact all-or-nothing acknowledgments,
detached progress/final snapshots and cooperative cancellation. No new state
machine, timer proof serialization, notification transport or storage table is
introduced. An operator budget resume or supersession can reduce parked count;
a recovered alert does not prove callback confirmation or business success.

## Consequences

Upgrade every host that may resume or acknowledge a version 2 checkpoint before
enabling the threshold. Applications still own fenced atomic persistence,
idempotent notification delivery, nondestructive acknowledgment receipts and
bounded driver/callback operations. Monitoring remains a read-only application
metadata operation, independent of platform callback execution, claim grants,
retry-policy format versions and SDK schema upgrades. Go/Node alert behavior
matches within each SDK's native checkpoint format; Python watch parity follows.

## Validation

Node generation produced no API drift, and the TypeScript build and example
syntax check passed. AST source comparisons confirmed 22 existing declarations
are unchanged, with checkpoint lifecycle/capacity/replay/cancellation and retained
validation unchanged beyond metric routing/version selection. Static policy
inspection confirmed legacy field order and an empty spread when parked alerts
are disabled. Health SQL/classification, intent/consumer schema contracts and
root exports remain byte-identical. Fifteen packaged JS/declaration/README files
match the build, including the new threshold, metric and version declarations.
ADR uniqueness, documentation routes, all nine added local links/anchors and
whitespace checks passed. No tests/assertions were added or changed, and no local
tests or SDK helper smoke calls ran. Hosted
qualification remains pending for parked-only/mixed conditions, threshold
reversals, incomplete/stale/out-of-order evidence, startup replay, exact
acknowledgments, capacity, version skew, operator resume/supersession, fenced
storage and cancellation/notification uncertainty.
