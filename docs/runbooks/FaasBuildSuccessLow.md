# FaasBuildSuccessLow

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `builderd_ops_total{op="build",code!="user_error"}` vs total.
Spec: §12 (build success 99%, non-user_error).
Severity: warn.

## Symptom

Build success rate excluding `user_error` has been below 99% for 15 m.
`user_error` is excluded by design: an app that fails to build because
of the customer's own code is not a platform failure (spec §12 + ADR-030).

A breach here means the platform itself is failing builds — most
commonly a runner image that doesn't match a pinned dependency
(`node22` vs `python312` mismatch is the canonical case).

## Verify

```bash
curl -fsS http://127.0.0.1:9105/metrics | grep builderd_ops_total
curl -fsS 'http://127.0.0.1:9090/api/v1/query?query=sum(rate(builderd_ops_total{op="build",code!="user_error"}[5m]))/sum(rate(builderd_ops_total{op="build"}[5m]))'
```

## Check

```bash
journalctl -u builderd --since '-15m' --no-pager | grep -iE 'runner|fail|error'
ls /var/log/faas/builderd/ | tail
```

A spike in `code="runner_error"` is the canonical sign — the runner
image isn't matching the spec'd dependency. The fix is a runner image
rebuild, not a per-tenant debug.

## Silence

```bash
amtool silence add \
  --matchers='alertname=FaasBuildSuccessLow' \
  --duration=1h \
  --comment='runner image rebuild scheduled'
```

## Recover

The build VM boundary (ADR-003) is the resource cap and the sandbox.
A failing runner is contained — no tenant data leaks. Recovery is a
rebuild of the runner image and a `systemctl restart builderd`.
