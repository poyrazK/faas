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
may only name a loopback HTTP origin. The Safe Deploy mutation routes
are mounted only on APID's loopback operator listener, never its public API
listener. For mutations, the canary token authorizes only step advancement,
and the action token authorizes only recovery or rollback. Those routes resolve
the actual deployment's account before applying the normal plan/state gates
and audit write.

Apply the `safe_release_worker_lease` migration before upgrading either
daemon. New canary requests return `503 safe_release_unavailable` until both
meterd release ticks have succeeded, both APID operator credentials have
passed their loopback and database probes, and meterd renews the database
lease. They also return that code when a tick or probe fails, the lease expires,
or the database check fails. An APID or meterd restart may therefore pause new
canaries for roughly one tick interval. Check the lease before creating a test
rollout:

```sql
SELECT healthy_at, expires_at, expires_at > now() AS ready
FROM safe_release_worker_lease;
```

The probes are read-only; the staging recovery drill below still verifies the
atomic mutation path.

For a canary, imaged also requires a verified public hosting smoke result
before moving the live pointer. Configure `FAAS_API_HOSTING_SMOKE_URL` on
the compute host and verify the candidate's health path responds through the
public route. A missing or skipped verifier fails the candidate and leaves
the predecessor live.

Restart APID first in staging, confirm public readiness, then restart meterd.
Confirm neither journal contains a Safe Deploy token/listener error. Test at
least two different customer accounts: each canary must reach its terminal
stage through the service loop, while a customer bearer from account A must
still receive 404 for account B's deployment.

Confirm the following metrics are present before creating a test rollout:

```bash
curl -fsS http://127.0.0.1:9091/metrics \
  | rg 'canary_progression|safedeploy_orchestrator|deployment_audit_emitted|faas_safe_release_worker_lease|faas_safe_release_serving_canaries|faas_safe_release_emergency_abort'
```

The APID lease gauges should show `worker_lease_check_success=1`,
`worker_lease_ready=1`, a positive `worker_lease_seconds_until_expiry`, and a
recent `worker_lease_last_check_timestamp_seconds` before a canary starts.
`faas_safe_release_serving_canaries` counts live, in-flight canaries with
nonzero traffic at the latest successful APID sweep. It can briefly include a
canary that the same sweep has just aborted; check the next 15-second sample.

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
- **Worker loss during a serving canary:** start a canary with a known-good
  same-scope predecessor and hold it at a nonzero traffic share. Stop meterd
  after its last successful lease renewal. Confirm new canaries are rejected
  after the 30-second lease TTL and the active canary is aborted after the
  additional two-minute grace period plus one 15-second APID sweep. Confirm
  the gateway serves the predecessor at 100%, the candidate at 0%, and the
  deployment audit actor is `apid:safe_release_lease_expired`. Restart meterd
  and confirm it does not resume the aborted rollout. Confirm
  `FaasSafeReleaseServingCanaryAtRisk` fires while traffic is exposed and
  `FaasSafeReleaseEmergencyAbort` fires after recovery. Check that both
  resolve once the lease renews and the alert window passes. In a separate
  no-canary drill, stop meterd and confirm only
  `FaasSafeReleaseWorkerLeaseStale` fires after two minutes.

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
- `faas_safe_release_emergency_abort_total{outcome=~"aborted|failed|skipped|sweep_failed"}`
- `faas_safe_release_worker_lease_ready` and `faas_safe_release_worker_lease_check_success`
- `faas_safe_release_worker_lease_seconds_until_expiry` and `faas_safe_release_worker_lease_last_check_timestamp_seconds`
- `faas_safe_release_serving_canaries`

The default stage and orchestrator cadence is 30 seconds. The default stuck
threshold is 30 minutes. Once a rollout exceeds that threshold, meterd makes
one idempotent APID `abort` request so traffic is redistributed to the last
known-good revision and the deployment audit records the automatic action.
Tune `FAAS_SAFEDEPLOY_STUCK_AFTER` only after staging has established the
expected rollout duration.

APID independently checks active canaries every 15 seconds. If meterd's
database lease has been expired for two minutes, APID atomically aborts each
canary that is still serving traffic and restores its exact same-scope
predecessor. Check the `apid:safe_release_lease_expired` deployment-audit actor
and the emergency-abort metric after a worker-loss drill. A missing lease row,
database error, or absent live predecessor leaves the canary for operator
inspection and emits an error or skipped outcome.

The `faas_safe_release` Prometheus alert group checks each APID instance:

- `FaasSafeReleaseWorkerLeaseStale` warns after a readable lease is absent or
  expired for two minutes with no serving canary. Check meterd release ticks,
  internal APID probe results, and the lease row shown above. A planned Safe
  Deploy shutdown also expires the lease and needs an acknowledged alert.
- `FaasSafeReleaseServingCanaryAtRisk` pages when a canary is serving while
  the lease is expired, absent, or unreadable for 30 seconds. Inspect the
  rollout and its same-scope predecessor immediately; verify APID completes
  the abort after the additional two-minute lease grace period.
- `FaasSafeReleaseLeaseObservationStalled` warns if APID has not completed a
  lease read for over 90 seconds. Check APID's database connection and its
  background loop. The last readiness sample may be stale.
- `FaasSafeReleaseEmergencyAbort` pages on an automatic abort. Confirm the
  audit actor, the candidate's zero traffic, the predecessor's 100% traffic,
  and gateway routing before restarting rollouts.
- `FaasSafeReleaseEmergencyRecoveryFailed` pages when APID could not sweep or
  abort. Check APID logs and the deployment audit. Use the manual recovery
  command below if the canary still serves traffic.

`outcome="skipped"` also counts harmless races with another APID instance;
inspect a persistent serving-canary gauge or a recovery-failure page before
acting on that counter alone. A lease read error sets `check_success=0`, so a
serving canary pages even when expiry cannot be determined. A failed canary
listing leaves the serving gauge at its previous value; the recovery-failure
alert reports the failed sweep.

## Kill switch and recovery

Remove both Safe Deploy tokens from the meterd secret file and restart meterd.
After meterd has stopped, remove the pair from APID's sealed environment and
restart APID to unmount the operator mutations as well.
The lease expires within 30 seconds of the last renewal, so APID rejects new
canaries while this switch is active. Active canaries serving traffic are
automatically aborted after the additional two-minute grace period while APID
and Postgres remain available. Inspect the audit trail and recover manually if
the emergency abort cannot find a serving predecessor or if APID is down:

```bash
gregale rollouts recover <slug> --action abort \
  --reason 'Safe Deploy automation disabled during incident'
```

Use `--action promote` only after validating the canary. Re-enable both tokens
only after the incident is closed and the staging path has been rechecked.
