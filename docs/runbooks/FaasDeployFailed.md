# FaasDeployFailed

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`
(alert `FaasDeployFailedAccount` in the `faas_alert_preset_signals`
group).
Metric: `meterd_deployment_failed_total{account_id, app_id}` —
counter, bumped by `cmd/meterd/deployment_failure_sweep.go` when a
deployment transitions to `failed`. Recording rule uses
`increase(...[1h]) > 0`.
ADR: ADR-123 (alert_presets catalog). Catalog preset:
`deploy_failed`, window 1h, threshold 0 (any fail).
Severity: warn (customer-facing catalog row).

## Symptom

A customer's deployment transition to `failed` was observed in
the rolling 1h window. The `increase()` form of the recording
rule means the alert **re-fires only when a NEW failed deploy
happens** — not on every sweep. This is the customer-facing
counterpart of the platform-tier `FaasStuckActivating` (which
fires on deployments stuck in `activating`/`pending` beyond their
2-min target).

Common deployment failure modes (per `cmd/apid/
deployment_pipeline.go`):

1. **Builder VM OOM** — the customer's app archive exceeds the
   plan-tier limit (Free/Hobby/Pro/Scale: 100/250/250/250 MB? —
   see `pkg/api/limits.go` `app_archive_max_bytes`) or the build
   process itself OOMs the 2-vCPU / 2 GB builder VM. Cross-check
   `builderd_build_memory_peak_bytes`.
2. **Snapshot restore failure** — the customer's parked snapshot
   is stale (Firecracker version pinned per ADR-005). Should
   fall through to a cold boot per the spec — investigate if it
   doesn't.
3. **App archive rejected by the classifier** — tar magic bytes
   don't match, no `package.json` / `pyproject.toml` at the root,
   or a Go binary that won't load in the guest.

## Verify

```bash
# What's the latest failure rate for this (account, app)?
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=increase(meterd_deployment_failed_total{account_id%3D%22<acct>%22%2C+app_id%3D%22<app>%22}%5B1h%5D)'

# Any deployments stuck in activating/pending?
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=meterd_deployment_status_count{account_id%3D%22<acct>%22%2C+app_id%3D%22<app>%22%2C+status%3D~%22pending%7Cactivating%22}'

# The actual deployment audit log (SQL):
sudo -u postgres psql -d faas -c "
  SELECT id, status, error_class, created_at
  FROM deployments
  WHERE account_id = '<acct>' AND app_id = '<app>'
  ORDER BY created_at DESC
  LIMIT 5;
"
```

## Silence

```bash
amtool silence add \
  --matchers='alertname=FaasDeployFailedAccount' \
  --duration=1h \
  --comment='customer <acct> retrying; builder VM pool temporarily exhausted'
```

## Recover

The alert clears when no new failures are observed in the 1h
window. A failed deployment does NOT auto-rollback the previous
good deployment — the customer stays on their last-good version
per ADR-005 / spec §5.4. Operator action is required only to
unblock the customer's next retry (e.g., raise builder VM
limits, fix classifier regex).
