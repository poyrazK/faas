# ADR-454: Observed route health gates for canary progression

- Status: Accepted
- Date: 2026-10-02
- Related: ADR-093, ADR-122, ADR-127, ADR-270, ADR-450

## Context

An aggregate deployment error rate can hide a failure on a critical route.
Configured route policy checks do not evaluate application responses. The
existing debugger telemetry identifies deployment, normalized method/path,
status and collapsed request counts. It cannot establish a trustworthy
capture denominator for requests lost before persistence.

## Decision

Add an independent, opt-in app route health configuration with report/enforce
mode, a revision compare-and-swap, and at most 20 distinct exact normalized
telemetry method/path selectors. Store intent through APID. Default is report
mode with no selected routes; no prior rollout changes behavior. Enforcement
requires traffic split and debugger telemetry entitlements. Customers can return
to report mode after a plan downgrade. Explicit routes arrays prevent accidental
selector removal from an omitted field. Cap request bodies at 16 KiB and paths
at 240 UTF-8 bytes. Bounds and evaluation constants live in pkg/api/limits.go.

Compare the exact candidate with the sole other live deployment serving traffic
in the same scope. Missing or ambiguous stable identity, inactive candidate,
unavailable telemetry, insufficient samples, or unavailable entitlement produces
unknown evidence. Report reads use a repeatable snapshot. MemStore retains its
Postgres-only telemetry posture and reports unknown rather than inventing data.

Use two consecutive closed UTC minute windows ending behind a 30-second ingestion
allowance. Both windows must start after the current stage and latest configuration
update. Each route needs at least 20 represented requests on each deployment in
each window. SQL aggregates exact labels and weighted count, without top-N
truncation, raw paths, route metric overflow buckets or request payloads.

A window regresses when candidate 5xx count is at least two, observed 5xx rate
is at least 5%, at least three times stable, and at least five percentage points
higher than stable. Two regressing windows produce a regressed route. Two healthy
windows produce healthy evidence; mixed windows remain unknown/unsettled. Any
regressed route makes the report regressed; otherwise any unknown route makes
it unknown. This is a bounded comparison of stored observations, not a statistical
significance test or proof of full traffic health. All reports explicitly state
observed_only coverage. The ingestion allowance does not promise delivery of
late or dropped telemetry.

Enforce mode pauses progression unless every selected route is healthy. Evaluate
inside APID's existing app/deployment locked traffic transaction for manual and
worker advances; configuration updates use the same parent lock order. Legacy
advance/promote recovery cannot bypass the guard. Abort and stable rollback remain
available. A blocked transition changes no traffic or traffic audit. Successful
advances retain only decision metadata in the traffic audit; detailed route counts
and deployment/commit identities are exposed through the report API and CLI.
The progression worker treats this conflict as an expected hold and retries on
later ticks. Existing aggregate circuit breaker and configured-policy gates keep
their independent behavior, including existing aggregate abort policy.

## Consequences

High-volume healthy routes cannot mask a selected critical route's 5xx increase.
Customers can inspect evidence before opting in and use report status in CI.
Low-traffic critical routes can intentionally hold a rollout until enough requests
arrive. Telemetry loss remains unquantifiable; the feature does not certify a
complete capture, enforce latency, run synthetic requests, or automatically abort
on this new route-specific comparison. Read reports are live observations, not
retained historical decisions, and advancing or editing intent establishes a
fresh evidence anchor.
