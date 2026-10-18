# Observe Data API database connection capacity

Connection-budget endpoints and `gregale data-api health` read a short-lived
capacity cache. The operator worker below refreshes it outside API request
handling. Its allocation input must come from independently qualified provider
or native evidence for the exact database, dataset and generation.

Apply the current control-plane migrations and build the worker:

```sh
go build -o /tmp/managed-postgres-capacity ./cmd/managed-postgres-capacity
```

Set `FAAS_MANAGED_POSTGRES_CONFIG` to the existing backend configuration and
`DATABASE_URL` through the normal secret mechanism. The configured provider
credential must be able to retrieve the existing direct maintenance credential.
The worker does not create credentials or widen the Data API role's grants.

Place the qualified allocation in an operator-controlled JSON file. The private
identity fields below must match the current catalog; use the actual source
report digest, values and timestamps. These example timestamps are illustrative.

```json
{
  "version": 1,
  "account_id": "11111111-1111-4111-8111-111111111111",
  "database_id": "22222222-2222-4222-8222-222222222222",
  "backend_id": "primary-a",
  "backend_fingerprint": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "provider_resource_id": "project-example",
  "data_resource_id": "project-example/br-example",
  "generation": 1,
  "usable_connections": 40,
  "source": "provider_api",
  "source_sha256": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
  "checked_at": "2026-10-17T09:00:00Z",
  "expires_at": "2026-10-17T09:05:00Z"
}
```

The file attests an independently established tenant allocation, including the
connection path and applicable role limits. `source` accepts `provider_api` or
`native_qualification`. A provider compute class or SQL `max_connections` value
alone is insufficient. The worker trusts the operator file and retains its
source digest privately; it does not authenticate the underlying report. Keep
the file outside customer-writable locations and replace it atomically when the
qualified source supplies fresh evidence. Do not merely renew old timestamps.

Run one observation:

```sh
/tmp/managed-postgres-capacity \
  --account 11111111-1111-4111-8111-111111111111 \
  --database 22222222-2222-4222-8222-222222222222 \
  --allocation /run/gregale/connection-allocation.json
```

Add `--watch` to repeat every 15 seconds until SIGINT/SIGTERM. Each iteration
rereads the file; refresh the qualified allocation before its expiry. Run one
worker per target database. Database leases reject overlapping observations.
The worker's control-plane pool has one connection, and each observation opens
one short-lived direct tenant connection under a ten-second total deadline.

The output contains state, a stable reason when unknown, and check/expiry times.
A one-shot unknown result exits unsuccessfully; watch mode continues retrying.
Missing/malformed/stale evidence invalidates the previous result without SQL.
Other reasons are `backend_unavailable`, `observer_unsupported`,
`sql_capacity_unavailable` and `observation_expired`. Provider/SQL error text is
not printed. Idle or transitioning compute remains unknown until it is active.

Inspect the result through the existing management endpoint or:

```sh
gregale data-api health APP --json
```

`capacity.state=verified` reports a qualified connection ceiling, its source,
check time and expiry. It expires within 30 seconds, sooner when the allocation
expires. It is not live headroom. `connection_budget.state` can still be
`unknown` because runtime pool configuration or other consumers are unverified;
the reasons remain explicit. `enforced=false` means scaling and rollout are
still unaffected by this report.

Stopping the worker lets the cache expire. A failed observation clears previous
success immediately, and database identity/generation changes invalidate proof
on read. The current worker supports the Neon adapter; native PostgreSQL SQL
observation is exercised locally, and live provider qualification is still
required before supplying production allocation evidence.
