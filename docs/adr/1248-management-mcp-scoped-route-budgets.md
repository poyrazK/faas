# ADR-1248: Current environment route-budget observations

## Status

Accepted. Extends ADR-1245.

## Context

App monitor reports, pooled observations, customer cohorts and automatic actions
are app-wide/default-scope features. A scoped agent cannot infer its environment's
health from those reports. Missing or old observations must remain unknown.

## Decision

Explicitly scoped operational summaries evaluate the saved app route budgets
against one exact settled serving deployment in the selected environment, using
a read-only repeatable-read snapshot and the existing weighted route-health
queries. The shared evaluator retains its sample thresholds, ingestion allowance,
consecutive completed windows and deployment/configuration observation anchor.
Scope filtering occurs before selecting serving deployments. Named releases
must have a workload-spec binding to the current registered environment identity.

Scoped reads do not pool historical windows, expose customer cohorts, persist
reports/incidents, or invoke automatic rollback. Missing, ambiguous, sparse or
unsettled evidence remains unknown. The report declares `budget_source:
app_policy`; it inherits thresholds, rather than introducing per-environment
configuration. Default selector-free reads retain their existing behavior.

## Consequences

MCP can inspect per-route error and weighted p95 findings, window counts and
minimum samples with exact environment attribution. Budgets disabled in the app
remain disabled. Missing readers or unavailable telemetry stay explicitly
unavailable. Older named releases without current environment bindings cannot
establish monitored health. Native lifecycle qualification remains independent;
these passive reads never wake workloads or write customer intent.
