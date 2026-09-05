# FaasSafeDeployRollout

Safe Deploy is deliberately disabled when either service-account token is
absent. `meterd` refuses to boot if only one token is present, because that
would enable the rollout state machine without the APID action client (or the
reverse) and could leave a canary in a partially automated state.

## Staging activation

Provision both APID-issued service-account tokens in the meterd secret file:

```text
FAAS_CANARY_PROGRESSION_TOKEN=<service-account-token>
FAAS_SAFEDEPLOY_TOKEN=<service-account-token>
```

The file is `/etc/faas/secrets/meterd/billing.env` on a deployed control-plane
host. Keep the file mode and ownership managed by the normal secrets/deploy
workflow; do not put either token in a unit file, TOML file, dashboard, or
command line.

Restart only meterd in staging and confirm the journal contains no
`safe-deploy token pair incomplete` error. Confirm that the following metrics
are present before creating a test rollout:

```bash
curl -fsS http://127.0.0.1:9091/metrics \
  | rg 'canary_progression|safedeploy_orchestrator|deployment_audit_emitted'
```

Create a Pro/Scale canary deployment using the `1-10-50-100` preset. Verify
the rollout advances on the expected stage boundaries and that the live
traffic sum remains 100 after every step:

```sql
SELECT id, app_id, traffic_percent, canary_preset, canary_step,
       canary_total_steps, rollout_state
FROM deployments
WHERE app_id = '<app-id>' AND status = 'live'
ORDER BY created_at DESC;
```

Verify the audit chain contains rollout and traffic events:

```sql
SELECT deployment_id, kind, actor, received_at, data
FROM deployment_audit
WHERE deployment_id = '<deployment-id>'
ORDER BY received_at ASC, id ASC;
```

Trigger a staging alert against the canary and verify that the action is
fail-safe and atomic:

- `demote` changes the canary to `aborted`, removes its traffic, and
  redistributes the remaining live revisions to a total of 100.
- `promote` completes the rollout through the atomic recovery endpoint.
- `rollback` creates the rule-correlated rollback audit event and uses the
  rollout-scoped idempotency key.

## Production rollout

Promote the exact tested secret/configuration through the normal deployment
path. Start with one canary application, then expand by account or fleet
slice. Watch these signals for at least one full rollout window:

- `safedeploy_in_flight_rollouts`
- `safedeploy_orchestrator_stuck_detected_total`
- `safedeploy_orchestrator_audit_emit_failed_total`
- `deployment_audit_emitted_total{outcome="failed"}`

The default stage and orchestrator cadence is 30 seconds. The default stuck
threshold is 30 minutes; tune `FAAS_SAFEDEPLOY_STUCK_AFTER` only after staging
has established the expected rollout duration.

## Kill switch and recovery

Remove both Safe Deploy tokens from the meterd secret file and restart meterd.
This stops automatic progression and alert actions; it does not change the
traffic already assigned to a deployment. Recover an in-flight rollout
manually after inspecting its audit trail:

```bash
gregale rollouts recover <slug> --action abort \
  --reason 'Safe Deploy automation disabled during incident'
```

Use `--action promote` only after validating the canary. Re-enable both tokens
only after the incident is closed and the staging path has been rechecked.
