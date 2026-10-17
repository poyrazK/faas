# ADR: Shared Node and Python preparation monitoring

- Status: Accepted
- Date: 2026-10-17

## Context

Node already ships an opt-in preparation monitoring example. Python now exposes
the same Prometheus metric names, labels and histogram contract. Both can use the
same operational views and observation guards, with SDK-specific host and recovery
instructions.

## Decision

Move the canonical monitoring assets to `examples/event-acknowledgment-monitoring`.
Keep one set of seven recording rules, nine warning alerts and twelve dashboard
panels. Preserve every expression, threshold, hold time, label, recording alias,
dashboard query/panel identity and observation guard. Update language/setup text
and bump the dashboard version to 2 while keeping its UID. Retain a pointer README
at the old Node location and document source-path migration for copied assets.

Preserve the original SDK-neutral scrape config and job name. Add an alternative
Python config referencing the same rule file with a distinct job name. Require one
scrape job per collector, one shared rule group per evaluation domain and distinct
target identities/listener ports for mixed hosts. Python scraping uses the same
process, collector and event-loop thread as preparation, with HTTP-only ASGI
lifespan handling disabled. Shared metric names do not permit job-record conversion.

Expand the common runbook with explicit Node/Python API mappings and Python
connection, protected-job, inspection and uncertain-COMMIT recovery guidance.
Keep retained receipt/claim/intent evidence independent of metric windows and
preserve explicit job ownership, cancellation joins and live admission fences.

Update existing CI paths to retain rule/config, dashboard JSON and alert-metadata
coverage, and add static config lint for the Python example. Keep all existing
checks and hosted test commands intact.

## Consequences

Operators explicitly install and tune the shared assets in their own monitoring
environment. Nothing deploys a service, dashboard, notification route, listener or
fleet provisioning change. Existing installed rule files keep their behavior.
No SDK code, processing protocol, metrics, schema, migration or automatic recovery
action changes. The pack continues to preserve worker scope, minimum-volume and
clipping guards, missing-data alerts and the limits of extrapolated observations.
See the [shared setup guide](../../examples/event-acknowledgment-monitoring/README.md).

## Validation

Use Prometheus rule/config lint, dashboard JSON and embedded PromQL parsing,
metadata/link checks, static source comparisons and ADR/documentation checks.
Add no tests, change no assertions and run no local tests. Hosted alert evaluation,
notification routing, Grafana rendering and live Python scraping remain pending
before production use.
