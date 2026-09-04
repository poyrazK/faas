# FaasGatewayQueueBacklogGrowing

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`
(alert `FaasQueueBacklogGrowingApp` in the `faas_alert_preset_signals`
group).
Metric: `gateway_queue_depth{app, account_id}` — gauge of pending
wake requests held by `gatewayd-internal` (cap 512/30s per the
CLAUDE.md gotcha). As of PR-D the metric carries an
`account_id` label admitted via `accountLabelSet` (cap=10k,
overflow=`__other__`); rows past the cap roll up under `__other__`
and contribute to `FaasAlertPresetAnyFiringAccount` under that
bucket.
ADR: ADR-123 (alert_presets catalog). Catalog preset:
`queue_backlog_growing`, window 15m, threshold 50.
Severity: warn (customer-facing catalog row).

> **Not** to be confused with `FaasBuildQueueBacklog` (line ~880 of
> `faas.rules.yml`), which covers the **builderd** build queue.
> This alert is specifically for the **gatewayd-internal wake
> queue** — the hold point where the gateway is awaiting cold
> boot or snapshot restore to complete before proxying.

## Symptom

The wake-queue depth for a customer's app has been above 50 for
15m. The webhook tells the customer's monitoring that wake
requests are queuing — they're not failing, just slowing down.

The alert's labels include `account_id` (or `__other__` when the
account overflows the bounded-admission set). It DOES contribute
to the `FaasAlertPresetAnyFiringAccount` correlation rule — see
`docs/runbooks/FaasAlertPresetAnyFiringAccount.md` for the
account-level rollup.

Common causes:

1. **Cold-boot snapshot restore repeatedly failing** — ADR-005
   snapshot cache miss. Each failed restore re-queues the request
   while the gateway waits for a cold boot to land.
2. **Customer app wake latency exceeded the queue cap** — the
   customer's app takes >512 ms to wake (typical: ~350 ms), but
   the queue cap is 30s, so any backlog should drain. Persistent
   depth >50 means new requests are arriving faster than wakes
   complete.
3. **Downstream Postgres contention** — `pg_stat_activity` shows
   long-running queries blocking schedd's instance-state reads.

## Verify

```bash
# Per-app queue depth (top 10)
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=topk(10%2C+gateway_queue_depth)'

# Cold-boot fallback rate (companion signal — high ratio means snapshot restore is failing)
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=rate(meterd_cold_boot_total%5B5m%5D)+%2F+rate(meterd_wake_total%5B5m%5D)'

# Wake latency (P95 over 5m)
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=histogram_quantile(0.95%2C+sum(rate(gateway_wake_latency_seconds_bucket%5B5m%5D))+by+(le))'
```

## Silence

```bash
amtool silence add \
  --matchers='alertname=FaasQueueBacklogGrowingApp' \
  --duration=30m \
  --comment='investigating <app> snapshot restore spike'
```

## Recover

The alert clears when `gateway_queue_depth{app, account_id}`
drops below 50 for the `for:` window (15m). Operator action is
required when
the queue persists — usually means a customer-side issue (large
app archive, slow startup hook) or a snapshot-cache hit-rate
problem (cross-check `FaasColdBootFallbackHigh`).
