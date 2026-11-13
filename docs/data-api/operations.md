# Operating a Data API

For resumable storage, background imports/exports, policy/RPC CI checks and
scheduled hook delivery while parked, see [recovery and background workflows](workflows.md).

Open the app's Data API dashboard and select **Observe health and recent requests**.
This can wake a parked app. The page shows current readiness, runtime version,
active/queued requests and connection-budget observations. An unknown probe is
not evidence of a database outage.

Recent HTTP counts, p95 edge duration and request-ID lookup cover one bounded
retained-log page from the last hour. They are historical samples, not billing
counters, SQL execution time or evidence of current readiness. Use the CLI for
deeper phase, query-shape and SQL timing diagnostics:

```sh
gregale data-api health NAME
gregale data-api diagnostics NAME --request-id REQUEST_UUID
gregale data-api deploy NAME  # upgrade the runtime after review
```

## Shared request budgets

Local rate limits remain useful for bursts. To share a tenant's request budget
across every replica, enable an optional fixed-window policy:

```sh
gregale data-api configure NAME --shared-budget requests=1000,window=60s,claim=tenant_id
gregale data-api configure NAME --shared-budget off
```

Omit `claim` for a per-subject budget. The named claim must be a nonempty string
in the verified application JWT; client headers cannot select another tenant.
The runtime sends a digest and owner policy under a dedicated workload assertion.
The control plane derives app/account identity from that assertion and atomically
spends credit in PostgreSQL. At most 4,096 budget keys are retained per app;
expired keys are pruned in bounded batches.

This mode covers signed-in HTTP requests, including metadata. Anonymous traffic,
server-key traffic, live reads and the existing approximate monthly caps keep
their separate policies. Requests and windows are bounded to 1,000,000 requests
and 1–3,600 seconds. Fixed windows can admit a burst on each side of a boundary.

Exhaustion returns `429 data_api_rate_limited` with `Retry-After`. Missing tenant
identity returns `403 data_api_budget_identity_required`. Coordinator failure
returns `503 data_api_shared_budget_unavailable` before customer SQL. No shared
credits are cached in snapshots and no request is automatically replayed. A
lost admission response may consume credit without executing SQL.

Changing the policy starts a new counter namespace. During a rolling policy
change, replicas with different policies use separate counters; complete the
fresh restart before relying on the new fleet-wide limit.

The feature needs the shared-budget migration and updated control plane before
the runtime is deployed. It enables usage reporting to obtain the platform
origin. The CLI requires `--shared-budget off` before reporting can be disabled;
manually removing the origin while a shared policy remains makes startup fail.
This is an owner-selected guardrail, not a billing quota or enforcement against
an owner who modifies their runtime. See [connection-capacity observations](../runbooks/DataAPIConnectionCapacity.md).
