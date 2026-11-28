# ADR-045 — Customer-configurable alert rules (issue #396)

Status: accepted on the ADOPT branch (issue #396 closure).
Deciders: platform owner; reviewed under issue #396.

## Context

Customers today can be alerted about **money** but not about
**breakage**. The existing customer-alerting surfaces cover spend
(#263 spend alerts, #262 quota warnings, #264 MTD projection).
Operator alerting (#275, #278) watches the platform, not the
customer's app. The web console's Alerts page today simply states no
rules can be configured.

The data dependency that blocked this is now closed: #273 shipped
`GET /v1/apps/{slug}/metrics` over a closed `range` vocabulary, and
`/v1/invocations` carries terminal state + `last_error` for cron /
queue / delayed-task runs. What's missing is the rule store, the
evaluator, and delivery.

## Decision

### Delivery channel

Webhook only. Email is deferred behind #246, which is the
sustained-outage ticket for the mail transport (today a fresh deploy
defaults to `LogSender` and silently drops mail, and no caller
retries on `mail.ErrTransient`). Criterion 5 of #396 says "email …
or webhook" — webhook alone satisfies it. A second channel can land
once the mail path is hardened.

### Evaluator location

meterd. Added as a sixth loop modelled on the quota tick. meterd
gains Prometheus reach (`FAAS_PROMETHEUS_URL`); today meterd has
zero outbound HTTP. The shared PromQL builders live in a new
`pkg/appmetrics` so the apid metrics endpoint and the alert
evaluator can't drift.

### Condition model

Two-source, both constrained to the same closed window vocabulary
(`5m,15m,1h,6h,24h,7d,15d` from the metrics endpoint, defined in
migration `00023`):

| `metric`            | Source        | Evaluator path                  |
|---------------------|---------------|---------------------------------|
| `error_rate_pct`    | Prometheus    | `pkg/appmetrics` PromQL         |
| `latency_p50_ms`    | Prometheus    | `pkg/appmetrics` PromQL         |
| `latency_p95_ms`    | Prometheus    | `pkg/appmetrics` PromQL         |
| `latency_p99_ms`    | Prometheus    | `pkg/appmetrics` PromQL         |
| `cold_start_pct`    | Prometheus    | `pkg/appmetrics` PromQL         |
| `request_count`     | Prometheus    | `pkg/appmetrics` PromQL         |
| `failed_invocations`| Postgres      | `CountFailedInvocationsSince`   |

The closed vocabulary is what satisfies criterion 3: the evaluator
cannot ask Prometheus for data outside `prom_retention_days:15`,
because the window itself cannot be set to anything else.

### Account-scoping + nullable `app_id`

Rules are account-scoped on the FK root. `app_id` is NULLABLE on
purpose: NULL means **account-wide**. The CHECK constraint
`alert_rules_failure_source_xor_chk` ties `failure_source` to
`metric='failed_invocations'` so an account-wide rule still pins a
single source dimension when its metric demands one.

### Cool-down (criterion 4)

Two complementary primitives:

1. **State transition** — a rule only fires on `ok → firing`.
   Subsequent breaching ticks do nothing. `firing → ok` happens on a
   healthy evaluation.
2. **Idempotency-key UNIQUE on `alert_deliveries`** — even across a
   flapping `ok → firing → ok → firing` cycle, the
   `idempotency_key` (rule_id + ':' + floor(epoch/cooldown_seconds))
   bucket suppresses a second delivery inside `cooldown_minutes`.

`ClaimAlertFire` is the atomic compare-and-advance primitive. It
mirrors `LoadAndStampLastQuotaWarning` (the CTE captures the OLD
stamp *before* the UPDATE; a naive `returning col = $2` is trivially
true post-update and was the regression CI caught in PR #69 — see
`pkg-state-usage-monthly-tz-compare.md`).

### Degraded-source rule (criterion 7)

Never fire when the metric source is degraded. Zeroed fields on a
degraded read would make `error_rate_pct < 5` trivially true and
fire every "below threshold" rule on the fleet. The gate is a single
string-prefix check (`Source` starts with `"degraded: "`); the
evaluator increments `meterd_alert_skipped_degraded_total` and moves
on.

### SSRF defence

Customer-supplied webhook URLs are reused through `pkg/oci/egress.go`.
It already denies RFC1918, CGN, loopback, link-local (IMDS), ULA,
multicast, and explicitly closes the DNS-rebinding hole by refusing
both hostnames that *resolve* into a denied range and direct IPs in
a denied range. The check runs **twice**: at rule-create validation
(fast 400 with `code: alert_webhook_denied`) and again at dial time
via the `EgressDialContext` transport hook, because DNS can change
in between.

### Secret handling

`webhook_secret` is write-only. Sealed with `secretbox.SealOne`
(age/X25519, existing host key — NOT a new env KEK). Reads return a
masked constant. Never logged (§11). Re-sealing is part of the
`PATCH` surface as an optional pointer field; a `nil` skips the
reseal.

### Scope reuse

Reuse `ScopesReadSurface` / `ScopesDeployWriteSurface` rather than
adding `alerts:read` / `alerts:write`. Scopes are an API-key
back-compat surface; crons set the precedent.

### Limits

`AlertRuleLimitPerApp` / `AlertRuleLimitPerAccount` sit beside the
cron limits in `pkg/api/limits.go`. Fail-closed accessors. Per-plan
defaults: Free **0/0** (gated to 402), Hobby 3/10, Pro 10/30, Scale
25/100.

### Console (criterion 9)

The production console is the separate `faas-frontend` repo (PR #160
deleted `website/`); a tracking issue is filed there. The in-repo Go
dashboard page (`pkg/dashboard/templates/app_alerts.html`) ships in
PR 4 as the operator-visible artifact.

## Consequences

- **PR split**: four PRs (PR 1 stores, PR 2 metrics+webhook lib,
  PR 3 API surface, PR 4 evaluator+console+e2e). The four together
  close #396; the latter three cannot land without PR 1.
- **meterd gains an outbound HTTP dependency** (Prometheus + webhook
  delivery). Operators who run meterd without network egress for
  these paths are out of scope — the metadata URL is a new
  deployment-time knob.
- **Cross-app `failed_invocations` queries** now happen every
  `FAAS_ALERT_INTERVAL` (default 60s). The `invocations_app_pending_idx`
  partial index serves most of them; the cross-app aggregate scan is
  the worst case for a noisy customer and lives only on the
  per-tick sweep.
- **CWE-918 (SSRF) avoided in three layers**: validator fast-fails
  obvious cases, the OCI dialer double-checks DNS at dial time, and
  the meterd retry loop always re-dials against the freshest DNS
  answer.

## Amendment 1 (2026-11-28): failure filters and failure details

- **Filters.** A `job_failures` rule can name one job (`job_name`): it then
  counts only the failed runs of the account's job with that name. A
  `failed_invocations` rule with `failure_source = cron` can name one cron of
  its app (`cron_id`). Jobs are named because the name is unique in the
  account and is how the CLI addresses a job; crons have no name, and their id
  survives deploys because deployment crons are upserted by id. Neither is a
  foreign key: a rule on a deleted job or cron counts zero rather than
  disappearing. apid checks that the job exists and the cron belongs to the
  app; `alert_rules_job_name_chk` and `alert_rules_cron_id_chk` keep the shape.
  A cron-filtered rule needs the store's `FailureAlertDetailer`; on
  a store without it the rule degrades instead of counting every cron.
- **Details.** A firing `boot_failures`, `job_failures` or `failed_invocations`
  notification to a channel lists up to 5 of the newest failures it counted:
  boot time, deployment and node; job, run, failed tasks and the first failed
  task's error; invocation source, path, cron, attempts and last error. Error
  text is collapsed to one line and cut to 200 bytes, and each note to 500
  bytes like route notes. The details go to the customer's own channels only,
  the same as the log lines alerts already carry. A read failure sends the
  notification without them.

## Follow-ups (not this ADR)

- #246 — wire real mail transport + retry; then add email as a
  second channel.
- `faas-frontend` issue for the console Alerts page.
- `POST /v1/alert-rules/{id}/test` — synthetic delivery for
  first-time setup.
