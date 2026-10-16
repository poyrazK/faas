# Operator alert recurrence diagnostics

Status: Accepted, 2026-10-16. Extends retained alert episode analytics.

## Decision

Add GET `/v1/admin/obs/incidents/platform_alert/recurrence` and
`gregalectl obs recurrence --since RFC3339 --json` for observed repeat
activations. Operators can identify repeatedly returning alert series and
compare their observed episode durations and recovery intervals.

Use the inclusive retained activation cohort `[since, as_of]`, default 24 hours,
maximum 30 days. Group by both immutable rule-definition key and exact series
hash. A rule edit or another resource never becomes a repeat in the same group.
Within each group, n episodes yield n-1 repeat activations and adjacent
comparisons. Counts describe retained observations, not lifetime recurrence,
false positives, notification volume or proven continuous monitoring. Cohorts
exclude older active episodes and predecessors outside the window. Retention
can remove predecessors; historical collection gaps cannot be reconstructed
from the existing singleton collection metadata. Keep those limits explicit.

Durations measure activation to confirmed resolution observation. Partition
all episodes into valid samples, unresolved/not observed and invalid clocks.
An unresolved episode contributes no completed-duration sample. Compare each
activation with its immediate retained predecessor in the same group. A
confirmed predecessor resolution at or before the next activation produces a
recovery-gap sample, including a legitimate zero. Missing recovery, resolution
observed after the next activation, and resolution clocks outside activation
to as_of are disjoint excluded classifications. Overlapping observations do
not prove overlapping real outages. Never skip an uncertain predecessor to
manufacture a gap. Report minimum and nearest-rank median of valid whole-second
samples, with counts and null statistics when samples are absent. Full totals
aggregate samples, not averages of group statistics.

Read collection, retention and at most 1001 episode rows under one read-only
repeatable-read PostgreSQL snapshot or one memory mutex and a five-second
request deadline. Use SQLC and the existing activation-time index. A cohort
above 1000 episodes returns a bounded 400 with a request to narrow since;
never silently sample it. Read no triage receipts. No schema, writer, alert
policy, notification routing or suppression change is needed.

Complete totals precede a ranked preview of at most 200 series. Sort by repeat
count descending, latest activation descending, rule key then series ID. Each
series includes its first/latest cohort activation and at most five recent
immutable episode references, newest first, with canonical evidence paths.
Both preview levels expose truncation. Safe stored rule/resource metadata is
included; actors, workflow ownership and raw source payloads are omitted.
Before the first successful collection totals are null with an empty preview.
A known quiet retained cohort has zero counts and null timing statistics.
Current source failures or disabled collection preserve retained counts and
confirmed resolution while leaving unresolved episode status unknown.

Preserve operator read scope, allowlist and MFA. Accept only one nonempty since
parameter; reject unknown, duplicate and malformed parameters, empty values
and dangling question marks. Generic 503 errors redact storage details. CLI
reads use the existing bounded typed JSON helper, redact upstream error bodies,
and reject inconsistent metric partitions and unknown/zero confusion. Keep
OpenAPI, its embedded copy, both private-route maps, DTO coverage and the Node
admin client synchronized. Frontend and rollout remain separately owned.

## Validation

Exercise memory and PostgreSQL inclusive cohort boundaries, future activation
exclusion, immutable recurrence/rule identity, missing rules, disabled source,
resolution-pointer isolation, cancellation, exact and exceeded read limits,
and shared store conformance. Verify domain evidence partitions, confirmed
zero gaps, overlap and invalid clocks, nearest-rank statistics, whole-cohort
totals, deterministic preview order, truncation and unknown versus quiet reads.
Check API authorization/MFA, read-only behavior, parameter validation, bounded
and redacted failures; verify CLI request/JSON/human output and invalid evidence
rejection. Run SQLC, OpenAPI/Node generation checks, focused regressions and lint.
