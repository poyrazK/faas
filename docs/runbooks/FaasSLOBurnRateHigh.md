# FaasSLOBurnRateHigh

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml` and the
`slo_burn_rate` alert preset.
Metric: `gateway_requests_total` non-2xx requests versus total requests.
SLO: ADR-082 API availability 99.5% (0.5% error budget).
Severity: warn.

## Symptom

The app exceeded both Google SRE burn-rate windows:

- more than 14.4× the error budget over 1 hour;
- more than 6× the error budget over 6 hours.

The customer-facing `slo_burn_rate` preset uses the same thresholds and
delivers the effective value to the configured webhook.

## Verify

```bash
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=faas_slo_burn_rate_1h:by_app'
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=faas_slo_burn_rate_6h:by_app'
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=gateway_requests_total{app="<app-id>",code!~"2.."}' | jq .
```

Confirm that the app has meaningful traffic in both windows. Quiet apps should
not be treated as breached because no traffic means no consumed error budget.

## Check

```bash
journalctl -u faas-gatewayd-public faas-gatewayd-internal --since '-30m' --no-pager | grep -iE '5xx|panic|timeout|queue'
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=sum(rate(gateway_requests_total{app="<app-id>",code!~"2.."}[1h]))'
```

Break down the failing status class and correlate the first increase with a
deployment, wake-queue saturation, or a downstream dependency. The `app` label
is the gateway identity; the meterd alert delivery contains the customer
account context.

## Recover

Roll back the deployment if the error-rate increase started immediately after
release. Otherwise fix or restore the failing dependency and confirm both
burn-rate recordings fall below their thresholds before closing the incident.
