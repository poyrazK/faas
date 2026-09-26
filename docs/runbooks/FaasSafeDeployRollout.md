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

Before staging, run the deterministic decision-to-recovery drill:

```bash
go test ./cmd/apid -run '^TestCircuitBreakerFaultDrill$' -count=1
```

It injects each health outcome through the real canary progression policy and
authenticated APID loopback client, then verifies rollback/hold/advance state,
audit outcome, and the 100% traffic invariant. It uses `MemStore` and a
controlled observation; it does not replace the staging checks below for the
Prometheus adapter, PostgreSQL transaction, or live metrics collection.

Before enabling the circuit breaker for routine production deploys, also run
these staging checks against an app with a known-good predecessor:

- **Low traffic:** leave the candidate at its first traffic stage until that
  stage duration expires without enough candidate and stable request samples.
  Confirm it stays at the same traffic share and
  `canary_progression_circuit_breaker_total{event="hold_insufficient_samples"}`
  increases. Send enough requests to both revisions; after the next stage
  boundary, confirm progression resumes.
- **Bad candidate:** deploy a revision that returns controlled 5xx responses
  on a test route. Once the candidate has enough samples, confirm it is
  aborted, its exact predecessor returns to 100%, and the audit reason names
  the 5xx regression. Check
  `canary_progression_circuit_breaker_total{event="abort_5xx"}`.
- **CPU regression:** use a controlled CPU-heavy candidate and send at least
  20 metered requests to both revisions. Confirm the rollout aborts only when
  candidate CPU per request is at least 3x the predecessor and at least 10ms
  higher, then verify the exact predecessor returns to 100% and
  `canary_progression_circuit_breaker_total{event="abort_cpu_per_request"}`
  increases.
- **Managed dependency regression:** make a controlled test dependency return
  5xx for the candidate while the predecessor remains healthy, or use a native
  gRPC test target that returns a nonzero, missing, or malformed `grpc-status`
  trailer with HTTP 200.
  After at least 10 authorized service-proxy attempts (including route/wake
  outcomes) and two errors, verify the candidate aborts when its error rate is
  at least 3x the predecessor and 10 percentage points higher
  (with a 20% floor), and check
  `canary_progression_circuit_breaker_total{event="abort_dependency_errors"}`.
  If only the candidate calls a newly introduced dependency, the absolute
  threshold is 20% errors after the same sample floor. This signal covers
  Gregale-managed service-proxy calls, not arbitrary external HTTP clients.
- **Missing OOM telemetry:** in an isolated test environment, make the OOM
  metric family unavailable. Confirm promotion holds and
  `canary_progression_circuit_breaker_total{event="hold_signal_unavailable"}`
  increases rather than treating the missing signal as zero OOMs.

The remaining abort event labels are `abort_p95_latency`,
`abort_cold_boot_p95`, and `abort_oom`; hold labels include
`hold_observation_unavailable` and `hold_recovery_failed`.
Never induce a workload OOM on a shared staging service; the unit tests cover
that decision branch. Verify the serving traffic total remains 100 after every
abort or advance.

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
