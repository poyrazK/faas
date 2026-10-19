# Operator rule evaluation diagnostics

Status: Accepted, 2026-10-16. Extends collection health and incident bundles.

## Decision

GET `/v1/admin/obs/rule-health` and `gregalectl obs rule-health --json` expose
current alert-rule evaluation diagnostics. A failed or stale rule can reject
the collector's complete snapshot; operators need to identify current blockers
without seeing raw queries, evaluation errors or private source paths. This
reader does not change collection validation, writes, recovery or triage.

Use the existing Prometheus `/api/v1/rules?type=alert` client and its complete
envelope checks, 8 MiB response bound and 4096 group/rule/alert bounds. The read
has a shared three-second deadline and accepts no query parameters. HTTP or
decode failure, warnings, missing arrays and malformed timestamps produce
unavailable state with null totals. Unconfigured Prometheus produces disabled
state with null totals. A complete empty snapshot has known zero loaded rules
and unavailable state; silence in a missing rule inventory cannot be green.

Assess every loaded alert rule, including watchdog and informational rules
that the collector validates before filtering retained episodes. Report bounded
rule/group identities, safe source-file basename, last evaluation, elapsed whole
seconds, last reported activation state, closed issue codes, raw-error presence,
and a validated repository runbook link when present. Age is null for absent or
future clocks, including future clocks within the existing 30-second tolerance.
Evaluation freshness uses the existing inclusive two-minute boundary. Negative
durations, invalid identities, missing definitions/arrays, invalid active-alert
observations and inconsistent rule state remain invalid. Failed evaluation,
missing/unknown evaluation, stale evidence and future clocks are distinct.

All issues are retained, with classification priority invalid, invalid_clock,
failed, stale, unavailable, healthy. Counts are null for every unhealthy rule;
an inactive, freshly evaluated rule can supply known zero pending/firing counts.
ReportedState is last reported state and cannot establish current recovery.
Duplicate group or rule identities are explicit invalid diagnostics. Invalid
empty groups have their own group item instead of disappearing from the read.
Valid rule hashes match the collector's definition-derived rule keys. These
identifiers are diagnostics, not episode IDs or mutation references.

Totals classify the complete bounded snapshot before limiting the preview.
The six rule-state counts partition Rules; InvalidGroups separately counts
malformed or ambiguous source groups. Return at most 200 items, sorted by the
classification priority above and then group/rule hash. TotalItems and
Truncated make omissions explicit; this is a preview without pagination.
Healthy requires at least one loaded rule and every assessed rule/group healthy.
Any diagnostic yields degraded; an invalid empty group is degraded even without
rules. This read covers currently loaded rules, not an expected deployment
inventory, underlying signal coverage or notification delivery. A current
blocker is not a receipt proving why an earlier collection failed.

## Privacy, contracts and bundles

Queries, raw errors, labels, raw annotations and absolute paths never enter the
DTO. Only bounded ASCII identities are exposed; unsafe identity text is omitted
while opaque hashes remain usable. Group names and basenames are capped at 128
bytes, rule names use the collector's 128-byte identifier policy, and runbook
links use the existing repository URL policy plus a 512-byte ASCII bound.
This bounds the 200-item response below the existing 1 MiB bundle-section cap.
Errors use closed generic reasons without upstream response bodies or URLs.

Routes retain operator scope, allowlist and MFA, including existing read-only
legacy admin-token compatibility. Reads create no audit or collection events.
The private OpenAPI contract, exclusion maps and generated Node models/client
stay synchronized. No new SQL, schema migration or runtime configuration exists.

Collection health advertises the exact `/v1/admin/obs/rule-health` route via
DiagnosticsPath. The bundle client recognizes only that exact constant and
performs a fourth explicit read under its existing shared timeout, section-size
bound and 4 MiB artifact cap. It never follows an arbitrary advertised URL.
Older or unrecognized advertisements keep the three-read bundle and explicit
unknown rule-health coverage. Unrecognized route text is omitted from the
artifact rather than exporting an arbitrary URL. Advertised reads must return a valid typed
contract; transport/contract failures fail collection instead of inventing data.
Valid disabled/unavailable/degraded observations are included as such.

The optional rule_health section and its state/truncation coverage are additive
to bundle v1. Sections remain independent snapshots. Raw unknown response fields
are discarded during typed decoding. The extension adds no frontend, rollout,
PR or deployment work; changes are committed locally.

## Validation

Compare per-rule healthy/rejected assessments with collector validation across
quiet, pending/firing, failed, unknown/missing evaluations, boundary/future/stale
clocks, invalid definitions and active-alert state. Test duplicate identities,
invalid empty groups, complete totals with a truncated unhealthy-first preview,
worst-case response size and private metadata exclusion. Verify envelope failures
and cancellation, API authorization and read-only history, CLI unknown/zero
handling and redacted errors, exact-route bundle advertisement, legacy coverage,
fourth-read timeout/failure, OpenAPI/Node parity, policies and configured lint.
