# ADR-1235: Spec §12 operator alerts ship at warn until paging is triaged

- **Status:** accepted; severities amended by ADR-1236 (`FaasEdgeRuleAuthCompileError` pages for jwt/ip/waf)
- **Date:** 2026-10-13
- **Decision:** Implement the §12 operator alerts that had no rule, all at `severity: warn` and `public_status: internal`, including the rows the spec marks page.
- **Why:** The rows were written, and several had runbooks, but nothing evaluated them. The fleet already carries 96 page alerts for one operator, so adding five more pages before that set is triaged would make paging worse.

## Context

Spec §12's metric table states an alert threshold for each operator signal.
A scan of the shipped rule files on 2026-10-13 found 26 promised alerts with no
rule at all: edge-rule apply, compile and JWT failures; the response-cache
bypass, store-skip, stale-if-error and L1 signals; safe-deploy stuck, abort,
audit-emit, missing-timestamp and audit-GC failures; the four standby
write-relay signals; stale route serves; the notify-hub reconnect, drop and
disabled signals; the pool-cap warning; dropped and panicked schedd loop work;
remote snapshot materialization; instance hot loops; and slow instance-stats
collection. Three runbooks (`FaasEdgeRuleApplyHigh`, `FaasEdgeRuleCompileError`,
`FaasEdgeRuleJWTFailures`) described their alert as live.

The spec marks five of these page: edge-rule compile errors, a stuck canary,
failing audit emits, the write-relay outcomes, and panicked loop work.

Two names in the table also do not match what the code exports. The write gate
runs in gatewayd-internal, whose OpsMetrics prefix is `gatewayd`, so its series
are `gatewayd_write_redirect_*`, not `gatewayd_internal_write_redirect_*`. The
safe-deploy counters are `<daemon>_safedeploy_*`, and only meterd writes them.

## Decision

1. The 26 alerts live in the `faas_spec12_operator_signals` rule group, each
   with a promtool fixture (`pkg/promqlrules/testdata/spec12_operator_signals.test.yml`)
   that fires it and a healthy-fleet case that keeps it quiet.
2. Every one is `severity: warn` and `public_status: internal`. Where the spec
   says page, the warn is a deliberate deviation recorded here. The paging-set
   triage decides which, if any, become page; promoting one is a severity label
   change plus a fixture update.
3. Rules read the exported names: `gatewayd_write_redirect_*` and
   `meterd_safedeploy_*` / `meterd_deployment_audit_*`. The §12 table is
   corrected to match.
4. `schedd_instance_rss_mb > plan × max_concurrency` and
   `schedd_instance_inflight_requests > max_concurrency × 2` are not
   implemented. Both thresholds depend on per-app plan limits that Prometheus
   does not have; they need a limit series (for example a per-app
   `max_concurrency` gauge) before a rule can express them.

## Consequences

- The signals are visible in Alertmanager's warn route and in Grafana, but do
  not page. A failure the spec meant to wake someone (a broken edge rule, a
  stuck canary) waits for the next warn review.
- New runbooks: `FaasRouteLookupStaleServed`, `FaasDBNotifyHub`,
  `FaasScheddLoopWork`, `FaasInstanceHotLoop`. The safe-deploy, write-relay and
  pool-starvation runbooks gained alert sections.
- `TestAlertMetricsAreEmitted` covers the new rules, except the ones that
  select `{__name__=~".+_db_notify_hub_*"}` / `_db_pool_*` by regex, which a
  static name check cannot resolve.
