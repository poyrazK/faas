# ADR: Opt-in monitoring for prepared Node event workers

- Status: Accepted
- Date: 2026-10-17

## Context

Prepared Node workers expose process-local observations as Prometheus text.
Operators need usable views and alerts that preserve uncertainty, separate local
commits from callback delivery, and distinguish idle traffic from lost metrics.
Application-owned workers are not platform daemons provisioned by fleet Ansible.

## Decision

Ship an opt-in application monitoring example: a scrape configuration, seven
recording rules, nine warning alerts, a Grafana dashboard and a recovery runbook.
Require the explicit target label `gregale_event_preparation="true"`, the default
formatter prefix, one collector per target and unique job/instance pairs. Keep
worker identity in recording results rather than averaging across replicas.

Derive ten-minute increases and p95 estimates only for a currently reachable
target with a present unclipped collector and no observed clipping in that
window. Require at least twenty estimated observations before publishing a failure ratio
or either p95. Observe unknown commits separately from failed shutdowns and local
receipt commits. Use independently tunable warning thresholds and hold times.

Monitor clipping, failed scrapes, reachable targets missing the expected metric,
and absence of all opted-in targets separately. Leave missing or low-volume
derived series missing; zero is not a substitute for unavailable evidence.
The rules assume a single Prometheus evaluation domain; central aggregation needs
additional source identity in all grouping/matching and corresponding dashboard
selectors. A shared runbook covers every alert with links to protected receipt,
claim and intent inspection before any explicit recovery action.

## Consequences

Applications explicitly install the example in their own Prometheus/Grafana
environment, configure protected scrape transport and choose Alertmanager routing.
Nothing enables fleet provisioning, deploys a dashboard, sends notifications or
changes processing ownership. Thresholds are examples, not a platform delivery
SLO. Dashboard panels consume the pack's recording rules so guards and thresholds
are evaluated consistently.

Increases are extrapolated observations and p95 values are histogram estimates.
Crashes, missed scrapes, first-scrape baselines and incomplete lifecycles still
lose observations. Uncertain-commit alerts do not mean the transaction remains
unresolved; clearing an alert does not establish recovery. Target removal needs
an external inventory to detect individual missing workers. No metric contains
jobs, tokens, business payloads or per-consumer identities.

No new metrics, SDK behavior, schemas, routes, migrations or automatic recovery
actions are introduced. Existing static CI checks include the example rule/config
pair, dashboard JSON and alert metadata without changing tests. See the
[setup and tuning contract](../../examples/event-acknowledgment-monitoring/README.md).

## Validation

Use Prometheus configuration/rule lint, JSON/YAML and embedded PromQL parsing,
metadata/link validation and static documentation checks. No tests or assertions
are added or changed and no local tests are run. Hosted alert evaluation and
Grafana rendering qualification remain required before production use.
