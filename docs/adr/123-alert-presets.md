# ADR-123 — One-click alert presets (issue #1233)

- **Status**: proposed
- **Date**: 2026-08-21
- **Closes**: #1233 (and the audit gap "customers must author a (metric,
  comparison, threshold, window_spec) quadruple to ship a basic alert")
- **Builds on**: ADR-045 (customer alert rules), ADR-034 (SSRF egress
  defence), ADR-091 (cors_presets catalog pattern), ADR-120 (per-app
  domain_doctor)

## Context

ADR-045 ships the customer-facing alert engine (`alert_rules` +
`alert_deliveries`, webhook delivery, dedup, secret handling). Today every
rule has to be hand-authored: a customer posts `(metric, comparison,
threshold, window_spec, webhook_url, webhook_secret)` to
`POST /v1/apps/{slug}/alerts` and the dashboard panel's empty-state copy
spells out the requirement: "Create one via the public API at
`POST /v1/apps/{slug}/alerts`."

This is correct for an engineer but wrong for everyone else. A non-engineer
opening the dashboard should be able to enable a sensible default alert in
one click. The eight conditions the audit identified as universally
useful:

| Preset | Metric source |
|---|---|
| API is down | per-app reachability probe (new) |
| Error rate exceeds 2% | existing `error_rate_pct` |
| p95 exceeds one second | existing `latency_p95_ms` |
| Cold starts exceed 10% | existing `cold_start_pct` |
| Spend exceeds €20 | MTD billing aggregator (new) |
| Deployment failed | `deployments.status='failed'` scan (new) |
| Domain certificate is expiring | per-app cert-expiry walker (new) |
| Queue backlog is increasing | existing `gateway_queue_depth{app}` |

The closed vocabulary at `pkg/api/alerts.go::AllowedAlertRuleMetrics`
covers 7 metrics; the preset list needs 8 (5 reachable today via existing
PromQL path, 3 require new signals). The closed vocabulary at
`pkg/state/types.go::AlertMetric*` must mirror it byte-for-byte.

## Decision

### Catalog pattern — mirror cors_presets

`alert_presets` is a fixed 8-row table seeded by migration `00348`. Rows
are owned by the system (meterd + apid boot roles are the only writers);
customers have SELECT-only. Each row carries:

```
(name, display_name, description, category,
 metric, comparison, threshold, window_spec,
 default_cooldown_minutes, enabled_in_catalog, minimum_plan)
```

`name` is the stable catalog key (`api_down`, `p95_latency_1s`, ...).
`enabled_in_catalog` toggles a row between "clickable" and "coming soon"
without removing it from the catalog — lets the dashboard render the full
list. `minimum_plan` is the closed-set plan tier the customer must hold to
see the preset (mirrors the alert-rules plan gate at
`cmd/apid/handlers_alerts.go:102-105`).

Cardinality is bounded: 8 rows, no per-tenant data. The migration
`00347_alert_presets.sql` enforces the closed-set CHECK constraints
directly in SQL (defence in depth) and pins the metric vocabulary to
exactly the 8 strings the evaluator will learn.

### Enablement model — instantiate, no FK

When a customer clicks "Enable" on a preset, the apid handler
(`enableAlertPreset` in `cmd/apid/handlers_alert_presets.go`) calls the
**same** `CreateAlertRuleIfUnderQuota` path the customer-facing
`createAlertRule` handler uses. The preset is just a typed payload
generator — it pre-fills `(metric, comparison, threshold, window_spec)`
and asks the customer only for the webhook URL + secret (and an optional
cooldown override).

There is no `alert_rules.preset_id` foreign key. After instantiation the
rule is a normal alert rule the customer manages through the existing
CRUD surface (PATCH / DELETE / rotate-secret). The audit row records
`alert_preset.enabled` with `{preset_name, app_slug, rule_id}` so the
operator can see which catalog entries drive real adoption.

**Why not a FK?** A FK would couple a customer's live alert to a system
catalog entry. If we ever rename or rotate a preset's threshold, every
derived rule would silently change. Instantiate keeps the customer in
control after enablement, which matches the audit's "predictable bills,
no surprises" principle (CLAUDE.md §4.7).

### Closed-set metric extension — 5 new metrics

The closed set grows from 7 to 12 metrics:

```
existing: error_rate_pct, latency_p50_ms, latency_p95_ms, latency_p99_ms,
          cold_start_pct, request_count, failed_invocations
new:      api_up, account_spend_eur, deployment_failed,
          cert_expiry_seconds, queue_depth
```

Three surfaces must learn them in lockstep:

1. `pkg/api/alerts.go::AllowedAlertRuleMetrics` (line 66-74)
2. `pkg/state/types.go::AlertMetric*` constants (line 1561-1569)
3. `migrations/00349_alert_rules_extend_metrics_chk.sql` (DROP + ADD
   `alert_rules_metric_chk`)

The evaluator's `observe` dispatch (`pkg/alerts/evaluator.go::observe`
lines 473-531) gains 5 new cases:

- **`api_up`** — Postgres path (`IsAPIReachable`) via new Store method
  `WasInvokedSuccessfullySince(ctx, accountID, appID, since) bool`.
- **`account_spend_eur`** — Postgres path via `MTDSpendEurCents(ctx,
  accountID) int64` summing `account_spend_snapshot.eur_cents` for the
  current UTC month.
- **`deployment_failed`** — Postgres path mirroring `CountFailedInvocationsSince`
  via `CountFailedDeploymentsSince(ctx, accountID, appID, since) int`.
- **`cert_expiry_seconds`** — PromQL path; the gauge
  `apid_tenant_surface_cert_expiry_seconds{account_id, app_id, hostname}`
  is fed by the meterd_tenant_surface_cert_expiry refresher
  goroutine in meterd (the metric name keeps the legacy `apid_`
  prefix for backward-compat with deployed alert rules; the
  underlying table is meterd-owned per the CLAUDE.md ownership
  rule and lives in `migrations/00351_meterd_…`).
- **`queue_depth`** — PromQL path; the gauge `gateway_queue_depth{app}`
  already exists at `pkg/gateway/metrics.go:667` — we only need to
  surface it in `appmetrics.Fetch`.

The `enabled_in_catalog=false` rows for the 5 new-metric presets
(`api_down`, `spend_eur_20`, `deploy_failed`, `cert_expiring_14d`,
`queue_backlog_growing`) flip to `true` once their backing signals are
plumbed end-to-end (i.e. when `meterd_api_reachable{...}` exists, when
the spend aggregator is writing, etc.). The catalog ships with the 5
rows visible-but-disabled so the dashboard renders the full roadmap on
day one.

### Single PR — but split commits within

A single PR keeps the 5-signal plumbing atomic with the catalog. Splitting
per-signal would create 5 PRs that each depend on each other; a customer
can't use `error_rate_2pct` without the catalog, can't see the catalog
without the apid route, can't enable without the instantiate helper. The
risk of "merge the catalog but not the signal" is much worse than the
risk of one large PR. Atomic commits within the PR (per the user's
PR-cluster pattern in MEMORY.md) keep each commit reviewable in ~10 min:

1. migrations (5 files) — done in commit 1
2. ADR-123 — commit 2
3. pkg/api + pkg/state closed-vocab extensions — commit 3
4. pkg/state queries (pgstore + memstore) — commit 4
5. pkg/alerts/evaluator 5-case extension — commit 5
6. pkg/appmetrics + pkg/wire/metrics — commit 6
7. cmd/meterd wiring (refresher + aggregator) — commit 7
8. cmd/apid handlers + routes + openapi.yaml — commit 8
9. cmd/gregale CLI subcommands — commit 9
10. pkg/dashboard template — commit 10
11. tests + e2e — commits 11+

### Limits

No new per-plan cap. Instantiating a preset consumes one
`AlertRuleLimitPerApp` / `AlertRuleLimitPerAccount` slot — the existing
Free = 0/0 gate already prevents Free customers from enabling any
preset (since `AlertRuleLimitPerApp == 0` for Free is the
`plan_alert_rules_not_allowed` rejection).

The catalog itself has an informational `AlertPresetCatalogLimitPerAccount
= 8` so the limits table stays in sync with the migration; it's never
enforced in code (the catalog has 8 rows and is system-owned).

## Consequences

**Positive:**

- A non-engineer can ship a useful alert in one click. Closes the audit
  gap "users must understand Prometheus semantics to ship a basic alert."
- The 5 new metrics are gated through the same dedup + webhook +
  audit path the existing 7 metrics use. No new failure modes.
- The catalog is a typed surface; future presets (TLS handshake latency,
  rate-limit headroom, ...) are just new seed inserts + evaluator cases.
- ADR-123 follows the cors_presets precedent exactly (slot 00347, hand-
  written pgstore, inline set_updated_at trigger). PR review is parallel to
  ADR-091's shape.

**Negative:**

- Single PR is large (~50 files, several thousand lines, 11 atomic
  commits). Mitigation: each commit is small, the slot is reserved
  pre-flight, the cross-PR slot gate walks `refs/pull/*/head` to
  confirm 00347 is still safe.
- The 5 new signals add Prometheus cardinality (`api_up` and
  `account_spend_eur` are per-account gauges). Mitigation: bounded by
  per-plan app caps (Hobby 5 / Pro 25 / Scale 100) and the
  `meterd_account_spend_eur{account_id}` cardinality is bounded by the
  account count, not the app count.
- The `cert_expiry_seconds` refresher is a new meterd goroutine.
  Mitigation: it follows the `pkg/gateway/cert_expiry.go` pattern
  exactly (single SELECT + bulk UPDATE + walker-status gauge).

## Follow-ups

- **Operator recording rules**: ship Prometheus recording rules in
  `deploy/ansible/roles/prometheus/files/faas.rules.yml` for the 5 new
  metrics so customer-facing alerts don't all hit raw PromQL. Tracked
  under issue #1234.
- **Email delivery**: ADR-045 deferred email behind #246. When email
  ships, the preset catalog gains an optional `notification_email`
  field on the instantiate request body. The catalog itself does not
  change.
- **Dashboard panel polish**: the preset grid currently renders all 8
  cards. A future iteration adds per-card "test alert" buttons (sends
  a no-op webhook with `test: true` in the payload).
- **Alert explanation rendering**: ship a one-paragraph "what this
  alert means" copy for each preset row, mirroring the
  `error-explanations` Cluster A precedent
  (`pkg/dashboard/views/render.go:274/311`).
- **Per-preset metric override**: a customer who enables `error_rate_2pct`
  but wants 5% instead of 2% should be able to slide the threshold
  before clicking Enable. Out of scope for this PR; tracked under
  #1235.
- **`account_id` label on `gateway_queue_depth` (PR-D, 2026-08-30)**:
  closes the documented correlation gap — pre-PR-D the
  `FaasAlertPresetAnyFiringAccount` correlation rolled up only 4 of 5
  signals because `gateway_queue_depth` carried `app` only. PR-D adds
  `account_id` admitted via `accountLabelSet` (cap=10k,
  overflow=`__other__`); the queue predicate joins the correlation's
  `count by (account_id) (…or…) >= 1` expression. Recording rule
  renamed `faas_queue_backlog_growing_over_15m:by_app` →
  `:by_app_account`. Synthetic promtool coverage in
  `pkg/promqlrules/testdata/alert_preset_signals.test.yml` cases 5/7.

## Critical files (reviewer path)

Top-down:
- `migrations/00347_alert_presets.sql` + `00348_alert_presets_seed.sql`
- `pkg/api/alerts.go` (closed vocab extension at line 66-74)
- `pkg/state/types.go` (AlertMetric* constants at line 1561-1569)
- `pkg/state/pgstore.go` (hand-written catalog queries, mirrors
  `corsPresetSelectCols` pattern at line 7047-7060)
- `pkg/alerts/evaluator.go::observe` (5 new cases at line 473-531)
- `cmd/apid/handlers_alert_presets.go` (instantiate-from-preset)
- `cmd/gregale/commands_alert_presets.go` (CLI surface)
- `pkg/apid/openapi.yaml` (API contract)
- `pkg/dashboard/templates/app_detail.html` (preset grid)