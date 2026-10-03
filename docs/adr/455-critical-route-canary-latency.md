# ADR-455: Critical route p95 latency budgets during canaries

- Status: Accepted
- Date: 2026-10-02
- Related: ADR-093, ADR-122, ADR-127, ADR-454

## Context

A release can keep returning successful responses while checkout or login becomes
slow. Aggregate latency can conceal a critical route regression. ADR-454 already
provides an atomic canary gate with exact normalized route selectors, current-stage
observation windows, revision checks and an explicit observed-only coverage limit.
Request telemetry stores integer latency representatives with collapsed counts,
not a recoverable distribution of every individual request.

## Decision

Extend each saved selector with optional `max_p95_ms` and `check_latency` fields.
A positive integer budget enables an absolute candidate p95 check; candidate p95
strictly above the budget violates it. Accept 0 through 86,400,000 milliseconds;
zero or omission disables the budget. `check_latency: true` independently enables
a relative check requiring at least 1.5 times stable p95 and at least 100 ms of
additional latency. A budget does not imply a relative check. Existing selectors
remain 5xx-only. Keep all bounds and comparison constants in pkg/api/limits.go.

Latency decisions require at least 100 represented requests on each deployment
per selected route in each of the two windows. Preserve ADR-454's identities,
stage/configuration anchors, ingestion allowance, entitlements and enforcement
transaction. Editing a budget or latency flag changes configuration intent and
therefore increments the revision and establishes a fresh observation anchor.
No additional table or migration is needed; selectors are stored in existing JSONB.

Use sqlc to compute weighted p95 estimates without expanding collapsed rows.
Group matching observations by latency and deployment/window, calculate cumulative
weights, and interpolate between the representatives at the floor and ceiling of
rank `(requests - 1) * 0.95`. Filter ranking work to latency-enabled selectors.
Keep 5xx counts in the same bounded observation query; legacy routes incur no
percentile ranking work. The measurements include all observed statuses and the
gateway's end-to-end latency, including cold boot time. Nil percentile evidence
differs from a valid zero; do not invent a ratio when stable p95 is zero.

Evaluate error and latency signals independently within each window and confirm
each independently across both windows. A latency failure followed by an error
failure does not establish one sustained signal and remains unknown. A confirmed
violation takes precedence over unknown evidence from another selected signal.
Otherwise unknown evidence holds an enforced rollout. Report mode never blocks.

Expose selected options, minimum latency samples, candidate/stable p95 estimates,
delta, factor and independent signal verdicts through the report API, CLI and SDKs.
Add fields optionally to preserve SDK compatibility for legacy reports. Validate
CLI verdicts against both per-signal windows and their observed counts/percentiles.
Existing traffic audit metadata, worker holds, aborts and rollbacks remain governed
by ADR-454. No new automatic abort policy is introduced.

## Consequences

Customers can protect critical journeys from slower releases using absolute
budgets, relative comparisons or both, and inspect evidence before enforcement.
Low-volume routes may hold progression until enough observations arrive. Weighted
percentiles describe stored bucket representatives; they cannot recover within-
bucket variation, prove full capture, or certify a statistical latency SLO. Runtime
report history is still not retained. Existing debugger latency findings and the
aggregate canary circuit breaker continue to evaluate their own evidence.
