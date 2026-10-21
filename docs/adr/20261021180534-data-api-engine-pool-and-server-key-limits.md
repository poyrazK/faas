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
  per-instance pool recently observed for that binding, or at the default when
  none is observed. Coverage keeps saying partial or unknown exactly as
  before, so a larger setting is never assumed to be the default without
  evidence.

### Server-key limits

- **Concurrency.** Server-key requests together may hold at most
  `DATA_API_SERVER_KEY_CONCURRENCY` admission slots. The default is the
  engine pool minus one when it is at least two, so signed-in users and
  visitors always keep a slot; with a pool of one it is that one slot.
  - Beyond the limit a server-key request answers
    `429 data_api_concurrency_limited`, `reason: "server_key"`,
    `Retry-After: 1`, before JWT work or SQL.
  - `data-api configure --server-key-concurrency N|default` sets it, from 1
    to 8. A value above the engine pool acts as the pool, because the CLI
    cannot read the current pool and a setting the runtime refuses would stop
    the API from starting.
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
- Backends using server keys can no longer starve users. A backend sending
  more concurrent requests than the limit gets 429s with `Retry-After: 1` and
  should retry.

## Rejected alternatives

- **Read replicas now.** Routing reads to a replica needs a provider
  read-only compute delivered as an `EndpointReadOnly`, which the Neon adapter
  does not provision yet. It also needs a second engine process with its own
  pool, and an answer for read-after-write. That is a later decision, built on
  this pool setting.
- **A per-plan pool table.** No plan quota for sessions exists in the
  financial model. The budget, not the plan, decides whether a database can
  take more.
- **Queueing server-key requests separately.** A second queue would add
  ordering and fairness rules. A fast 429 with `Retry-After` keeps admission
  simple and makes backends retry.
