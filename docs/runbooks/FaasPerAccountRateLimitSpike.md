# FaasPerAccountRateLimitSpike

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `gateway_per_account_rate_limited_total{account_id, plan}` (gatewayd-internal `/metrics`).
Spec: §4.1 gatewayd-internal rate-limit contract + ADR-040 (issue #292).
Severity: warn.

## Symptom

Per-account gateway rejections exceed 100/min fleet-total over the
rolling 5-minute window. The `account_id` label is the customer's
uuid (joined from `apps.account_id` in `pgRouter.toApp`); the
`plan` label is one of `free`, `hobby`, `pro`, `scale`.

`account_id="__other__"` is the **bounded admission overflow
bucket** — pre-instantiated at zero so the exposition has stable
rows from boot. A non-zero value on `__other__` means the per-app
limiter, the `fakeBackend` unit tests, or a future label matcher
is in play; investigate before paging.

The PR-#292 threat model is a **botnet rotating across a single
customer's many apps**. Each app individually stays under
`RateLimitRPS`, but the sum easily blows past the account's plan
budget. This alert is the coordination signal — a single
misbehaving customer peaks well below 100/min fleet-total, so the
threshold is tuned to fire on aggregate abuse, not on a noisy
single-tenant spike.

## Verify

```bash
# Confirm the counter is incrementing on this box.
curl -fsS http://127.0.0.1:9090/metrics | grep gateway_per_account_rate_limited_total
# Fleet sum — the same expression the alert evaluates.
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=sum(rate(gateway_per_account_rate_limited_total[5m]))'
# Plan breakdown — cross-check whether one plan row dominates.
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=sum by (plan) (rate(gateway_per_account_rate_limited_total[5m]))'
journalctl -u faas-gatewayd-internal --since '-15m' --no-pager | grep -i 'rate limit'
```

## Check

```promql
# Per-customer drill-down — which account is consuming the budget?
topk(20, sum by (account_id, plan) (rate(gateway_per_account_rate_limited_total[5m])))
```

Drill the offenders via the apid admin endpoint
(`/admin/accounts/<uuid>` — ticket-only access) to read the
account's plan, deployed-app count, and last-30-day traffic. The
canonical causes are:

- **Single-customer burst**: a misdeploy (infinite redirect, retry
  storm from a CI runner, queue-driven scrape loop). The customer
  wants the alert; recovery is on their side.
- **Coordinated abuse**: scanners rotating across an account's
  apps. The PR-#292 fix is the intended mitigation; let the 429s
  stand. Per-account freeze is tier-3 follow-up (ADR-040 Open
  follow-ups); today's only knob is SIGHUP which drops every
  bucket (see Recover below).
- **Config regression**: a plan row in `pkg/api/limits.go` dropped
  to zero (Free 50, Hobby 200, Pro 1000, Scale 5000 are the
  expected values — pin via
  `pkg/api/limits_test.go::TestPlanLimitsMatchSpec`). The
  `UnknownPlanFailsClosed` guard in `pkg/gateway/ratelimit.go`
  treats any unmapped plan as zero RPM, which would 429 every
  customer on that plan within seconds.

## Silence

```bash
amtool silence add \
  --matchers='alertname=FaasPerAccountRateLimitSpike' \
  --duration=2h \
  --comment='customer misdeploy identified — fix rolling out'
```

## Recover

```bash
# Drop every per-account bucket. Use after a config regression:
# SIGHUP re-reads pkg/api/limits.go and ForgetAll's both the per-app
# and per-account limiters in cmd/gatewayd-internal/main.go.
kill -HUP $(pidof faas-gatewayd-internal)
```

SIGHUP drops every bucket — the abuse victim's bucket resets too,
but at the 5-minute per-minute refill floor they re-stabilize
within a single tick. A per-account `ForgetAccount` admin
endpoint is reserved for tier-3 work (ADR-040 Open follow-ups);
until that lands, SIGHUP is the only knob.
