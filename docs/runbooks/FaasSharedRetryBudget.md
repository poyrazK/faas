# FaasSharedRetryBudget

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`, group
`retry_safety`.

Metrics: `gateway_retry_budget_shared` and
`gateway_retry_budget_backend_operations_total{operation,result}`, plus
`gateway_retry_budget_backend_info{backend_id}`.

Severity: warn. Family: `retry_safety`.

## Symptom

`FaasRetryBudgetMixedModes` means the gateway fleet has both process-local
and shared retry allowances. The fleet-wide cap requires every serving
gateway to use the same shared backend. Central mode defaults to Postgres
(ADR-570); explicit Redis credentials override that selection.

`FaasRetryBudgetBackendErrors` means shared store operations are failing. A
gateway declines retries, including when the original observation failed.
Original requests still run if their independent admission checks succeed;
a failure of Postgres rate admission returns 503.

`FaasRetryBudgetBackendMismatch` means gateways selected different shared
backends or endpoints/databases. The `backend_id` is a short credential-free
endpoint hash.

## Verify

Query Prometheus after every gateway has been upgraded and scraped:

```promql
count(up{job="gatewayd-internal"} == 1)
count(gateway_retry_budget_shared{job="gatewayd-internal"} == 1)
count(count by (backend_id) (gateway_retry_budget_backend_info{job="gatewayd-internal"}))
sum by (instance, operation, result) (rate(gateway_retry_budget_backend_operations_total{job="gatewayd-internal",result="error"}[5m]))
```

The two counts must match, and the first count must cover every serving
gateway. The endpoint identity count must be 1. Check ratelimit.mode and the
Postgres database or configured Redis override. For Redis, check the shared
Ansible Vault variable without printing the credential into a ticket.
The role renders that value to a root-only systemd credential. Direct URL
environment configuration is supported for older deployments, but it must
not coexist with the credential path.

Run the automated rollout gate with a Prometheus URL reachable by the runner
and the expected number of serving gateway targets:

```sh
PROMETHEUS_URL=http://127.0.0.1:9090 \
EXPECTED_GATEWAY_COUNT=3 \
deploy/scripts/verify-shared-retry-budget.sh
```

It fails unless every expected target is up, every target reports shared mode,
every target exports a backend identity, all gateways report one common shared
identity, and no shared-backend operation errors occurred in the last ten minutes.
`production-release-acceptance.sh` runs this gate when
`SHARED_RETRY_BUDGET_REQUIRED=true`; it defaults the expected gateway count to
`ACTIVE_NODE_COUNT` and requires `PROMETHEUS_URL` in that mode.

CI also runs the two-process canary through the real `gatewayd-internal`
service path. To run it locally with the E2E PostgreSQL fixture available:

```sh
go test -p 1 -vet=off ./cmd/e2e -run '^TestE2E_ServiceRetryBudget_' -count=1
```

The canaries cover the Postgres default and the explicit Redis override.
They require Linux for daemon capability checks and use a VMMD fixture.
Each proves that two originals across independent gateways admit one
shared replay, checks that both processes expose the same backend identity,
then makes the selected retry store unavailable and checks that both original
calls run once while neither gateway retries. These are pending locally on
Linux; they do not replace native VM or leak acceptance.

## Recover

1. Confirm the selected Postgres or Redis endpoint and private network are
   healthy. Inspect `journalctl -u faas-gatewayd-internal` without displaying
   connection credentials.
2. For an explicit Redis selection, confirm `gatewayd_retry_budget_required=true` and one common
   `gatewayd_retry_budget_redis_url` in the fleet inventory or Vault. Reapply
   `deploy/ansible/bootstrap.yml` to gateway nodes. The role rejects a
   missing URL when shared mode is required.
3. Wait for every `gateway_retry_budget_shared` gauge to read 1 and for
   backend errors to stop. Run the automated rollout gate and two-gateway
   canary above before marking production acceptance complete.

Removing the Redis override in central mode switches to Postgres; it does
not select local counters. A backend switch starts a different budget window.
Drain traffic or disable retries across all gateways for at least the ten-second
window, converge all gateways to one backend, then restore retries and verify
the gate. An explicit switch to local mode removes the fleet cap and must be
reported as such. See `docs/ops/shared-traffic-counters.md` for accounting and
local evidence; native and deployed rollout acceptance remain separate.
