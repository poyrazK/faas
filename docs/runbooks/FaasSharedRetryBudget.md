# FaasSharedRetryBudget

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`, group
`retry_safety`.

Metrics: `gateway_retry_budget_shared` and
`gateway_retry_budget_backend_operations_total{operation,result}`, plus
`gateway_retry_budget_backend_info{backend_id}`.

Severity: warn. Family: `retry_safety`.

## Symptom

`FaasRetryBudgetMixedModes` means the gateway fleet has both process-local
and Redis-backed retry allowances. The fleet-wide cap does not hold until
every serving gateway uses the same Redis endpoint.

`FaasRetryBudgetBackendErrors` means Redis commands are failing after
startup. A gateway declines retries on those errors; original requests
continue through their ordinary path.

`FaasRetryBudgetBackendMismatch` means gateways reached different Redis
transport endpoints or logical databases. The `backend_id` is a short hash
of address, DB, and TLS mode; it contains no Redis password.

## Verify

Query Prometheus after every gateway has been upgraded and scraped:

```promql
count(up{job="gatewayd-internal"} == 1)
count(gateway_retry_budget_shared{job="gatewayd-internal"} == 1)
count(count by (backend_id) (gateway_retry_budget_backend_info{job="gatewayd-internal"}))
sum by (instance, operation, result) (rate(gateway_retry_budget_backend_operations_total{job="gatewayd-internal",result="error"}[5m]))
```

The two counts must match, and the first count must cover every serving
gateway. The endpoint identity count must be 1. Check the configured URL at
the source (the shared Ansible Vault
variable) rather than printing the credential on a host or into a ticket.
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
every target exports a backend identity, all gateways report one common Redis
identity, and no Redis operation errors occurred in the last ten minutes.
`production-release-acceptance.sh` runs this gate when
`SHARED_RETRY_BUDGET_REQUIRED=true`; it defaults the expected gateway count to
`ACTIVE_NODE_COUNT` and requires `PROMETHEUS_URL` in that mode.

CI also runs the two-process canary through the real `gatewayd-internal`
service path. To run it locally with the E2E PostgreSQL fixture available:

```sh
go test -p 1 -vet=off ./cmd/e2e -run '^TestE2E_ServiceRetryBudget_TwoGatewayCanary$' -count=1
```

The canary proves that two originals across independent gateways admit one
shared replay, checks that both processes expose the same backend identity,
then shuts Redis down and proves both original calls still run while neither
gateway retries.

## Recover

1. Confirm the Redis endpoint and private network are healthy. Inspect
   `journalctl -u faas-gatewayd-internal` and the Redis service without
   displaying the URL or password.
2. Confirm `gatewayd_retry_budget_required=true` and one common
   `gatewayd_retry_budget_redis_url` in the fleet inventory or Vault. Reapply
   `deploy/ansible/bootstrap.yml` to gateway nodes. The role rejects a
   missing URL when shared mode is required.
3. Wait for every `gateway_retry_budget_shared` gauge to read 1 and for
   backend errors to stop. Run the automated rollout gate and two-gateway
   canary above before marking production acceptance complete.

For a deliberate rollback, set `gatewayd_retry_budget_required=false` and
clear `gatewayd_retry_budget_redis_url` for all gateways, then converge and
verify that every mode gauge reads 0. This restores per-process budgets;
the fleet-wide cap no longer applies.
