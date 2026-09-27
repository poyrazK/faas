# ADR-288: Service dependency reliability controls and fleet signals

- **Status:** accepted
- **Date:** 2026-09-27
- **Related:** ADR-168, ADR-196, ADR-197, ADR-201, ADR-269

## Context

The node-local service proxy already resolves declared dependencies, wakes
parked services, retries stale transports, and tracks endpoint health. A
half-open breaker probe stayed reserved during a long Upgrade session. Gateway
maps retained retired app and instance keys indefinitely, and the open-target
gauge was never updated. Callers could not set a deadline or retry policy for
one binding, operators had no unsampled caller-to-target latency/error series,
and retry admission was local to one gateway process.

## Decision

Add a caller-owned `service_reliability` map, keyed by the declared target
slug, to standalone app create/PATCH and project Compose
`x-gregale-service-reliability`. Each policy may set `timeout_ms`,
`max_attempts`, `min_remaining_ms`, `retry_budget_percent`, and
`allow_non_idempotent`. Validate names against bindings and cap values in
`pkg/api/limits.go`. Omitted fields retain the existing gateway defaults;
`max_attempts: 1` disables proxy retries. PATCH omission retains the map,
while `null` or `{}` clears it. A project reapply preserves the stored map when
the extension is absent and removes entries for removed bindings.

Start a binding timeout after authorization, covering routing, wake, guest
forwarding, and replay. The inbound deadline still wins. The Upgrade
handshake is covered, while an established raw session detaches from the HTTP
request budget. Settle an endpoint's half-open probe when its 101 handshake
is sent; release an unanswered probe and record a stale transport as failure.
Sweep endpoint leases, pick cursors, and breaker entries after ten minutes of
inactivity. Publish the count of currently open endpoint breakers per target
app, deleting an idle app's series after its last breaker is pruned.

Publish `gateway_service_dependency_edge_calls_total` and
`gateway_service_dependency_duration_seconds`, labelled by trusted caller
and target app UUID and final outcome. Count each eligible internal HTTP call
once, including routing and wake failures. Authorization failures are not
dependency failures. Raw Upgrade sessions are outside the HTTP duration
histogram; their transport outcome remains represented in breaker and service
call counters.

Optionally use Redis as the authoritative ten-second retry budget through
`FAAS_GATEWAY_RETRY_BUDGET_REDIS_URL`. Original-request observations and retry
admission use atomic scripts shared by every gateway pointed at that Redis.
If Redis fails after startup, deny the replay rather than reverting to local
allowances. A gateway configured for Redis fails startup when it cannot connect.
Without the setting, the existing process-local budget remains available for
single-node installations; operators must configure a shared endpoint on all
gateways before claiming a fleet-wide cap.

For Ansible-managed hosts, provide one fleet Vault variable
`gatewayd_retry_budget_redis_url` and enable
`gatewayd_retry_budget_required`. The role projects the URL through a
root-only systemd credential and sets
`FAAS_GATEWAY_RETRY_BUDGET_REDIS_URL_FILE`; the direct URL environment
variable remains for older deployments and cannot be combined with the file.
Per-gateway mode and credential-free backend identity metrics allow operators
to confirm every gateway reaches the same Redis endpoint. Backend operation
errors are counted, and alerts cover mixed modes, endpoint mismatch, and
Redis failures. The Redis service itself is an operator prerequisite.

## Consequences

- Callers can bound the full dependency hop and tune retries without changing
  the target app or the public edge policy.
- Operators can inspect per-edge error rates and latency without relying on
  sampled traces. UUID labels constrain identity spoofing, though the number
  of series still grows with actively communicating app pairs.
- A shared retry budget introduces Redis as a required availability dependency
  only for deployments that opt into fleet-wide enforcement. A Redis outage
  reduces retries but does not fail an original request.
- Idle breaker cleanup is lazy: it runs on a later service request, at most
  once per minute. A quiet gateway can retain keys until traffic resumes.
