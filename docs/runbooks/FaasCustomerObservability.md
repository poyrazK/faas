# Customer observability pipelines

This runbook covers the background work behind customers' synthetic checks,
SLO budgets, log search and alert notifications (ADR-20261024222102). Each
pipeline fails without breaking anything a request waits on, so these alerts
are the only sign that customers are reading stale or missing data. Releasing
or withholding a preview is done with platform flags
([platform-flags.md](../ops/platform-flags.md)), never by restarting a daemon
with a different environment.

## Signals

| Metric | Meaning |
| --- | --- |
| `meterd_observability_loop_enabled{loop}` | 1 while `synthetic_checks` (ADR-748) or `slo_rollup` (ADR-747) runs. `slo_rollup` also needs `FAAS_PROMETHEUS_URL` on meterd. |
| `meterd_observability_loop_last_pass_timestamp_seconds{loop}` | Last pass that finished without an error; 0 until the first. |
| `meterd_observability_loop_passes_total{loop,outcome}` | Passes by `ok` or `error`. |
| `meterd_observability_loop_items_total{loop,outcome}` | Synthetic runs (`ok`, `failed` = the customer's check failed, `record_error`) and SLO hours (`recorded`, `failed`). |
| `apid_log_projection_events_total{source,outcome}` | Build and deploy events apid projected into `log_events` (`inserted`, `failed`). |
| `apid_runtime_log_events_total{outcome}` | vmmd's runtime lines at apid: `inserted`, `rejected`, `rate_limited`, `not_released` (the account does not have log search), `db_error`. |
| `apid_log_search_ingest_enabled` | 1 while `FAAS_LOG_SEARCH_ENABLED` lets apid project events and accept runtime lines. |
| `vmmd_runtime_log_events_total{outcome}` | vmmd's side: `over_budget`, `queue_full`, `paused`, `forward_failed`, and apid's answers. `forward_failed` and `paused` are expected while `apid_log_search_ingest_enabled` is 0. |
| `meterd_alert_channel_deliveries_total{kind,outcome}` | Slack, PagerDuty and email deliveries: `delivered`, `failed`, `unseal_failed`. |

No label carries an account, app, check or channel ID.

## FaasObservabilityLoopStalled

The loop runs but has not finished an error-free pass within its budget
(`synthetic_checks` 10 minutes, `slo_rollup` 30 minutes).

1. Read meterd's log for `meterd: synthetic checks` or `meterd: SLO budget
   rollup` warnings. They carry the error of every failed pass.
2. A database error points at meterd's pool or PostgreSQL itself; check the
   host PostgreSQL alerts first.
3. For `slo_rollup`, query Prometheus from the meterd host with the URL in
   `FAAS_PROMETHEUS_URL`. A rollup that cannot reach Prometheus records nothing
   and backfills up to 14 days of missed hours once it can.
4. A loop that is stuck without errors (`meterd_observability_loop_passes_total`
   stops counting) needs a meterd restart.

## FaasObservabilityLoopDisabled

meterd started without the loop. For `slo_rollup` this almost always means
`FAAS_PROMETHEUS_URL` is unset in meterd's environment; set it and restart
meterd. Customers' SLOs show "no data" until then, and the rollup backfills
up to 14 days of missed hours on start. `synthetic_checks` is disabled only by a store
that predates ADR-748, which means meterd runs against the wrong binary or
database.

## FaasObservabilityLoopItemsFailing

Passes complete but items fail. `record_error` on `synthetic_checks` means
probe results could not be written to `synthetic_check_runs`; check database
health and disk. `failed` on `slo_rollup` means Prometheus queries for single
hours failed; check Prometheus load and query timeouts. Customers' own failing
checks are counted as `failed` on `synthetic_checks` and never page.

## FaasLogLedgerWritesFailing

apid cannot insert into `log_events`.

1. Check the `log_events` partitions: meterd reconciles the current and next
   two months on startup and hourly (ADR-213), and logs and exports a failure
   metric when it cannot.
2. Check the database for lock waits and disk. The projector keeps its cursor
   and retries, and `source_event_id` makes every retry converge. vmmd does
   not resend a batch apid refused: those runtime lines stay in the instance
   ring and the runtime archive but are not searchable.
3. To shed load while investigating, turn the `log_search` capability flag off
   (`gregalectl flags disable --key log_search --reason "<incident>" --yes`).
   Search, log alerts and runtime-line inserts stop for every account until it
   is back on; the build and deploy projection keeps running while
   `FAAS_LOG_SEARCH_ENABLED` is set.

## FaasRuntimeLogForwardingFailing

vmmd cannot hand runtime lines to apid while apid accepts them. Check vmmd's
guest-trace apid target (the same listener that carries guest spans), that
apid's `LogEvents` service is registered (`FAAS_OTEL_SPANS_WRITER_ENABLED` is
not `false`), and the mTLS certificates on a split box. Lines that fail are
not indexed; the instance ring and the runtime archive still hold them.

## FaasAlertChannelDeliveriesFailing

Either meterd could not unseal a channel's credentials (`unseal_failed`), or
every delivery of one kind failed for 30 minutes.

- `unseal_failed`: meterd could not open a channel's sealed destination. If it
  fails for many channels, meterd's age identity (`FAAS_HOST_AGE_IDENTITY_PATH`)
  does not match the key apid sealed with. If it fails for one channel, that
  channel's sealed value is damaged and the customer must recreate it; never
  re-seal customer secrets by hand.
- `email`: check the mail relay and maild's alerts.
- `slack` or `pagerduty`: check egress from the control plane to
  `hooks.slack.com`, `events.pagerduty.com` or `events.eu.pagerduty.com`.

One customer's misconfigured destination does not fire this alert while other
deliveries of the same kind succeed; the customer sees the failure on the
channel's status in the dashboard.
