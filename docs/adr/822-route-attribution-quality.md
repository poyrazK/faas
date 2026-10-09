# ADR-822: Route attribution quality reporting

Status: Accepted

## Context

Advisory route CPU/request checks assume comparable instrumentation. Aggregate
collection coverage does not establish route labeling completeness. Missing
labels or changes in background work can alter route-associated CPU shares.

## Decision

Compute labeled and unattributed CPU shares from the entire merged CPU profile
before route filtering. Expose sampled CPU totals and percentages in API, SDK,
CLI and dashboard responses. No sampled CPU means unavailable percentages.
The fractions do not describe VM CPU or prove request instrumentation coverage.

Extend existing host-generated received-coverage records with versioned fixed
CPU counters: attributed, unlabeled, invalid_label, route_not_admitted and
encoding_limit. Preserve ingestion rejection reasons through private host
context between sanitization and backend encoding. Never retain rejected label
values, arbitrary guest labels or request URLs. Encoding failures preserve CPU
as unattributed. No additional backend series or metadata samples are needed.

The coverage reader accepts both legacy records and new records. Reconcile
optional diagnostic totals against merged CPU before reporting complete
diagnostics. Legacy or unmatched residual CPU has unknown discard reasons;
inconsistent metadata cannot override CPU-derived percentages. Counter values
are CPU seconds, not discarded label counts.

Warn on a lower candidate labeled share. A change of at least 20 percentage
points in either direction is substantial. If quality is unavailable or changes
substantially, advisory route checks become insufficient_data. Aggregate
assessment outcomes, retries and rollout behavior remain unchanged. This
threshold is centralized in API limits and recorded with the comparison.

Retain quality comparisons with assessments and canary history. Copy them with
completed canary assessments into saved investigations; subsequent telemetry or
profile expiry does not remove the historical summary. Bound stored reasons,
warnings, totals and percentages.

## Consequences

CPU mix, garbage collection, background work and unsupported collectors can
change attribution shares without an instrumentation regression. The guard is
conservative supplementary context, not a statistical confidence calculation.
Equal fractions cannot prove consistent labels for every route. Unlabeled CPU
cannot distinguish background work from unlabeled requests or unsupported
collectors. Host allowlist rejections include undeclared routes, route limits
and policy-read failures.

Upgrade apid before profiled: older coverage readers treat the extended record
format as unavailable coverage. New readers support both versions. Live backend
merge and browser acceptance remain operational validation work.
