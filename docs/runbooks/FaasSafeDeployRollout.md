# FaasSafeDeployRollout

Safe Deploy is deliberately disabled when either internal service token is
absent. `meterd` refuses to boot if only one token is present, because that
would enable only half of the control loop. APID also refuses the pair if its
operator listener is not bound to loopback. These are **not** API keys or
customer-account tokens: one account-bound key cannot advance canaries for
other tenants.

## Staging activation

Generate two distinct random tokens of at least 32 bytes each. Provision the
same pair in both `/etc/faas/sealed.env` (APID) and
`/etc/faas/secrets/meterd/billing.env` (meterd):

```text
FAAS_CANARY_PROGRESSION_TOKEN=<random-service-secret-1>
FAAS_SAFEDEPLOY_TOKEN=<random-service-secret-2>
```

Keep file modes and ownership managed by the normal secrets/deploy workflow;
do not put either token in a unit file, TOML file, dashboard, issue, or command
line. `FAAS_APID_INTERNAL_BASE_URL` defaults to `http://127.0.0.1:9101` and
may only name a loopback HTTP origin. The three Safe Deploy mutation routes
are mounted only on APID's loopback operator listener, never its public API
listener; the canary token can only advance a step, and the action token can
only recover or request rollback. Both routes resolve the actual deployment's
account before applying the normal plan/state gates and audit write.

Restart APID first in staging, confirm public readiness, then restart meterd.
Confirm neither journal contains a Safe Deploy token/listener error. Test at
least two different customer accounts: each canary must reach its terminal
stage through the service loop, while a customer bearer from account A must
still receive 404 for account B's deployment. Confirm that the following
metrics are present before creating a test rollout:

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

Promotion is also health-gated. While an enabled, app-scoped alert with an
explicit `rollback` or `demote` action is in `firing`, the canary progression
tick holds the current traffic step. Webhook-only alerts remain notification-
only and do not pause a rollout. A read failure is fail-closed (the canary is
held until the alert state can be read again). The fleet counter
`canary_progression_health_gate_blocked_total` records these holds.

GitHub-connected deployments project the rollout state as well as the build
state. A canary remains `in_progress` in the GitHub Check Run and Deployment
timeline until `rollout_state=complete`; an aborted rollout is reported as a
failed check with the abort reason and stage links. Rollout-step changes enqueue
the same durable check outbox used by build transitions, so a stopped `githubd`
process catches up after restart.

## Production rollout

Promote the exact tested secret/configuration through the normal deployment
path. Start with one canary application, then expand by account or fleet
slice. Watch these signals for at least one full rollout window:

- `safedeploy_in_flight_rollouts`
- `safedeploy_orchestrator_stuck_detected_total`
- `safedeploy_orchestrator_audit_emit_failed_total`
- `canary_progression_health_gate_blocked_total`
- `deployment_audit_emitted_total{outcome="failed"}`
- `safedeploy_orchestrator_auto_aborted_total`
- `safedeploy_orchestrator_auto_abort_failed_total`

The default stage and orchestrator cadence is 30 seconds. The default stuck
threshold is 30 minutes. Once a rollout exceeds that threshold, meterd makes
one idempotent APID `abort` request so traffic is redistributed to the last
known-good revision and the deployment audit records the automatic action.
Tune `FAAS_SAFEDEPLOY_STUCK_AFTER` only after staging has established the
expected rollout duration.

## Kill switch and recovery

Remove both Safe Deploy tokens from the meterd secret file and restart meterd.
After meterd has stopped, remove the pair from APID's sealed environment and
restart APID to unmount the operator mutations as well.
This stops automatic progression and alert actions; it does not change the
traffic already assigned to a deployment. Recover an in-flight rollout
manually after inspecting its audit trail:

```bash
gregale rollouts recover <slug> --action abort \
  --reason 'Safe Deploy automation disabled during incident'
```

Use `--action promote` only after validating the canary. Re-enable both tokens
only after the incident is closed and the staging path has been rechecked.
