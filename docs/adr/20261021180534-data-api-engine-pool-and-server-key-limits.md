# ADR: Data API engine pool size and server-key limits

Date: 2026-10-21
Status: Accepted
Related: [ADR-650](650-schema-generated-data-api.md), [ADR-1732](1732-data-api-user-concurrency-limits.md), [ADR 20261018170742](20261018170742-data-api-access-modes-and-clients.md), [ADR-1729](1729-data-api-connection-capacity-observations.md)

## Context

ADR-650 fixed each Data API instance at two engine connections, with HTTP
admission matching the pool and eight FIFO waiters. A busy API answers
`503 data_api_overloaded` once two slow requests run, before the platform adds
instances, and each instance can never use more of its database than two
sessions.

The fixed number also runs through the platform:
- Runtime connection reports must total exactly two, and their table's CHECKs
  enforce it.
- The connection budget plans two connections per resident and planned
  instance.

Data API bindings connect through the provider's transaction pooler (the
runtime already disables prepared statements for it). A larger per-instance
pool therefore adds client sessions to the pooler, not server connections to
PostgreSQL.

Server keys, the trusted credential for backends, bypass the per-user rate and
concurrency limits (ADR-1732). One runaway backend can therefore hold every
admission slot, and signed-in users and visitors queue behind it.

## Decision

### A configurable engine pool

- **The setting.** `DATA_API_POOL=N` sets the engine pool from 1 to
  `DataAPIMaxPoolConnections` (8). It defaults to
  `DataAPIDefaultPoolConnections` (2), today's behavior.
  `data-api configure --pool N` stores it.
- **Coordinated refresh** (ADR-1109) still reserves one connection for its
  control reader. It needs a pool of at least 2, and the engine gets `N - 1`.
- **Admission.** HTTP admission, per-user concurrency (ADR-1732) and pool
  observations follow the engine pool. The queue stays eight FIFO waiters
  with a one-second wait.
- **Limits above the pool.** A `DATA_API_USER_CONCURRENCY` above the engine
  pool acts as the pool instead of stopping startup, as ADR-1732 had it. The
  CLI cannot read sealed settings, so a pool change alone must never leave a
  runtime that refuses to start.
- **Not a plan quota.** The ceiling is a technical bound, not a plan
  quota: the financial model prices compute, not sessions. Database
  capacity stays the connection budget's subject.

### Connection reports and the budget follow the configured pool

- **Reports.** Runtime reports carry the configured engine and refresh
  connections, totalling 1 to 8. apid accepts any total in that range.
  - The observation table's CHECKs are widened online: the new ones are added
    `NOT VALID` under new names, validated, and then the old ones are dropped,
    each step in its own transaction.
  - An older apid rejects reports totalling more than two. Those instances
    then count as unobserved, as any missing report does.
- **Budget.** An observed instance contributes its reported connections.
  Unobserved and planned instances of a binding are estimated at the largest
  per-instance pool in the binding's valid reports, never below the default.
  Without valid reports, a binding whose app and scope set `DATA_API_POOL`
  counts at the largest pool, eight: the value is sealed, so the budget can
  see only that it is set. Otherwise it counts at the default. Coverage keeps
  saying partial or unknown exactly as before, so a larger setting is never
  assumed to be the default without evidence.

### Server-key limits

- **Concurrency.** Server-key requests together may hold at most
  `DATA_API_SERVER_KEY_CONCURRENCY` admission slots, queued or running. The
  default is the engine pool minus one, so signed-in users and visitors
  always keep a slot. A limit at or above the engine pool keeps nothing back,
  so only admission bounds server keys, as before; that includes the default
  on a one-connection engine.
  - Beyond the limit a server-key request waits in FIFO order, within
    admission's bounds: eight waiters and one second. It then answers
    `429 data_api_concurrency_limited`, `reason: "server_key"`, with
    `Retry-After`, before SQL.
  - A slot is held until both the handler and the response finish. A
    request whose client leaves after its SQL was dispatched keeps its slot
    for one query budget, as per-user leases do (ADR-1732), because a
    disconnect does not stop the SQL. Otherwise a backend could drop
    connections and retry to take the slot users keep.
  - `data-api configure --server-key-concurrency N|default` sets it, from 1
    to 8. A value at or above the engine pool removes the separate limit,
    because the CLI cannot read the current pool and a setting the runtime
    refuses would stop the API from starting.
- **Rate.** `DATA_API_SERVER_KEY_RATE_LIMIT_PER_SECOND` and
  `DATA_API_SERVER_KEY_RATE_LIMIT_BURST` add an optional token bucket per key
  slot, bounded like the per-user policy.
  - It answers `429 data_api_rate_limited`, `reason: "server_key"`.
  - `data-api configure --server-key-rate-limit N[,burst=M]|off` sets it.
- **Diagnostics.** Diagnostics classify both refusals under their existing
  codes with the new reason. They never show subjects or keys.

## Consequences

- An API can serve up to eight requests at once per instance, plus the
  platform's horizontal instances, without changing the database class.
- Owners who raise the pool should watch `data-api connection-budget`. The
  pooler bounds server connections, but a larger pool still raises client
  sessions and the budget's estimates.
- Backends using server keys can no longer starve users. A backend sending a
  few more concurrent requests than the limit sees them wait, as they used to
  in admission's queue. One that keeps more waiting than the queue holds, or
  for longer than its wait, gets 429s with `Retry-After` and should retry.
- CLIs from before this decision check health and diagnostics against the
  fixed pool. They call health from a runtime with another pool inconsistent,
  and diagnostics with server-key refusals partial. Teams should upgrade the
  CLI before changing the pool; the runtime's data is unchanged.

## Rejected alternatives

- **Read replicas now.** Routing reads to a replica needs a provider
  read-only compute delivered as an `EndpointReadOnly`, which the Neon adapter
  does not provision yet. It also needs a second engine process with its own
  pool, and an answer for read-after-write. That is a later decision, built on
  this pool setting.
- **A per-plan pool table.** No plan quota for sessions exists in the
  financial model. The budget, not the plan, decides whether a database can
  take more.
- **Refusing server-key requests past the limit at once.** On the default
  pool the limit is one, so a backend making two parallel calls, which used
  to queue briefly in admission, would start failing on upgrade. A FIFO wait
  bounded exactly like admission's keeps those backends working, and the
  slot users keep stays free.
