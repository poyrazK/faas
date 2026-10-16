# ADR-1384 · Live queries: an incremental SQL engine for native managed Postgres

- **Status:** proposed (owner direction, 2026-10-16: build a Gregale answer to
  Neon Realtime; full incremental engine; native Postgres first; sealed-query
  and row-level-security authorization).
- **Date:** 2026-10-16
- **Decision:** A client can subscribe to a read-only SQL query against a
  Gregale-managed native PostgreSQL database and receive the initial result,
  then one atomic delta per batch of committed transactions. Change capture and
  incremental maintenance run inside the database VM from logical decoding, so a
  subscription never keeps a parked database awake. Every SQL expression is
  evaluated by PostgreSQL itself; the engine maintains results by lineage and
  group keys and never re-implements SQL semantics. Clients connect through
  existing realtime endpoints. Queries are authorized either as backend-sealed
  queries or through the Data API's JWT path with PostgreSQL row-level security
  applied to every delta.
- **Why:** Neon launched Neon Realtime (public beta, 2026-10-15): any read-only
  deterministic `SELECT` becomes a live subscription. Gregale has no equivalent;
  its closest feature, ADR-1114 table-change feeds, sends row images through a
  polling outbox and supports no joins, aggregates or snapshots. Gregale already
  owns the pieces a better product needs: the database VM and its WAL (ADR-1027),
  managed client connections with plan limits (realtimed, ADR-1323), and a
  JWT-to-RLS path (ADR-650).

## Context

### What Neon shipped

Neon Realtime (beta) installs an incremental live-query engine inside Postgres
that consumes logical replication, matches writes to live queries and streams
changes through a managed WebSocket API. Its public documentation states:

- Any read-only, deterministic `SELECT` (joins, aggregates, window functions,
  subqueries); no `now()` or `gen_random_uuid()`; SQL up to 32 KiB, 256
  parameters of 16 KiB.
- A subscription starts with the full result; later changes arrive as batches
  of committed transactions, applied atomically and tagged with transaction IDs.
  A reconnect replaces the whole result. Writes go through the customer's
  backend and are confirmed by `awaitTxId`.
- Authorization: the backend seals the query, parameters and database into a
  60-second bearer capability. No row-level security yet.
- Subscriptions do not keep a scaled-to-zero database awake; starting one can
  wake it. Enabled per branch. React hook, TanStack DB, SSR, Drizzle and Kysely
  adapters. No published limits or benchmarks; the wire protocol is unstable.
- Free during the beta; database compute and egress are billed.

Sources: <https://neon.com/blog/neon-now-has-realtime>,
<https://neon.com/docs/realtime/overview>,
<https://neon.com/docs/realtime/guides/queries>,
<https://neon.com/docs/realtime/guides/subscribing>.

### What Gregale has

- **Native managed Postgres (ADR-1027):** each database runs in its own
  Firecracker VM on a database node. guest-init passes Postgres its mandatory
  settings as `-c` flags, ships WAL to the host itself (`archive-wal`), and
  allows no TCP listener. pgproxyd authenticates clients, wakes databases
  through schedd and reaches them through vmmd. Majors 14 to 18; production
  class adds a streaming standby.
- **realtimed:** managed WebSocket endpoints with OIDC, ticket and bearer
  authentication, per-account connection limits and a node outbound memory
  budget (ADR-1323), and the v2 resumable protocol.
- **Data API (ADR-650):** PostgREST behind a JWT-verifying gateway; the binding
  login has no RLS bypass and the gateway sets the request claims, so PostgreSQL
  policies are the authorization authority. `@gregale/data` is the typed client.
- **ADR-1114 table-change feeds** (operator preview): trigger capture into a
  customer outbox, relayed every five seconds, row images up to 4 KiB. They stay
  as the notification feature they are; live queries do not build on them.
- **Pricing principle (ADR-1323):** Gregale sells compute. Software features are
  free; features that hold resources get plan limits.

## Decision

### 1. Product surface

```ts
// Backend: seal a query for the signed-in user (local crypto, no network call).
import { createLive } from '@gregale/data/live';
const live = createLive({ secret: process.env.GREGALE_LIVE_SECRET! });
const sealed = live.seal(
  sql`SELECT c.id, c.name, count(m.*) FILTER (WHERE m.created_at > r.read_at) AS unread
      FROM channel_members cm
      JOIN channels c ON c.id = cm.channel_id
      LEFT JOIN messages m ON m.channel_id = c.id
      JOIN reads r ON r.channel_id = c.id AND r.user_id = cm.user_id
      WHERE cm.user_id = ${userId}
      GROUP BY c.id, c.name, r.read_at`,
);

// Browser: subscribe through the app's realtime endpoint.
const { rows, status, awaitTxId } = useLiveQuery(sealed, { refresh: fetchSealed });

// RLS mode: a Data API query with the user's JWT; policies decide visibility.
const q = data.from('messages').select('*, author(*)').eq('channel_id', id)
  .order('created_at', { ascending: false }).limit(50).live();
```

- A database is enabled with `gregale postgres live enable <database>` (API
  `POST /v1/databases/{id}/live`). Enabling issues the per-database live secret
  and injects `GREGALE_LIVE_SECRET` into apps bound to the database.
- Subscriptions travel over an app's realtime endpoint as new v2 frame types
  (`live.subscribe`, `live.rows`, `live.changes`, `live.progress`, `live.error`).
  The endpoint's app must hold a binding to the target database.
- Each change carries `upsert` (opaque row ID and row) or `remove` (row ID);
  each batch carries the `xid8` transaction IDs it covers.

### 2. Architecture and data path

```
customer write ──> Postgres (database VM) ──WAL──> liveq engine (same VM)
                                                      │ compiled plans in,
                                                      │ deltas out (vsock via vmmd)
                                                      v
                                   livequeryd (database node, host)
                                                      │ mTLS gRPC streams
                                                      v
                       realtimed (compute nodes): result cache + fan-out
                                                      │ WebSocket (v2 frames)
                                                      v
                                            browsers, agents, backends
```

- **Inside the database VM.** guest-init adds `wal_level=logical` to the
  mandatory flags when live queries are enabled and supervises a static `liveq`
  process in its own cgroup with a memory ceiling (a fixed share of the VM, set
  by class). liveq connects over the local Unix socket as the platform role
  `gregale_live`, owns one logical slot (`gregale_live`, pgoutput) and one
  publication, executes maintenance queries, and keeps per-query lineage
  indexes and result versions. It never sees client connections.
  `max_slot_wal_keep_size` bounds WAL held for a stalled engine.
- **livequeryd (new, one per database node).** Compiles queries (libpg_query
  parse, catalog-backed analysis, maintenance plan; cgo is acceptable on hosts,
  not in the guest), registers plans with the VM's engine through vmmd's vsock
  relay as pgproxyd does, wakes parked databases through schedd, and serves
  delta streams to realtimed. It deduplicates identical subscriptions by plan
  fingerprint so a query is maintained once per database however many clients
  hold it.
- **realtimed.** Verifies sealed queries or RLS-mode JWTs, keeps the current
  result of each live query it serves, sends new subscribers the cached result
  without touching the database, and fans out deltas. Live-query bytes count
  against the per-connection queue and the ADR-1323 node outbound budget; live
  connections count against plan connection limits.
- **apid.** Owns customer intent: enabling live queries per database, the live
  secret (sealed at rest, rotation with overlap), and plan limits. It mirrors
  live secrets to realtimed with endpoint registrations, as it does endpoint
  credentials today. It never executes live queries.
- **Ownership is unchanged elsewhere:** schedd remains the only waker and
  writer of instance state; vmmd remains the only component touching
  Firecracker; realtimed remains the owner of client connections.

### 3. Consistency model

The engine maintains results in rounds. Each round is a consistent snapshot:

1. Open a `REPEATABLE READ` transaction and take its snapshot S
   (`pg_current_snapshot()`); then read `pg_current_wal_insert_lsn()` as L.
2. Drain decoded WAL until the decoded position reaches L. Every transaction
   visible in S inserted its commit record before L, so all of them are now
   decoded; waiting for the flush to reach L also covers transactions committed
   with `synchronous_commit=off`, which become visible before their commit
   record is flushed. Decoded transactions not visible in S
   (`pg_visible_in_snapshot`) are held for the next round.
3. Maintain every affected query using only S-visible changes, executing all
   maintenance SQL inside the round's transaction, so every row read reflects S.
4. Emit, per query, one delta tagged with the S-visible `xid8`s, then commit
   the round and advance the slot's confirmed position past fully applied
   transactions.

A client therefore always holds exactly the result of the query at some
committed snapshot: a transaction's effects appear all at once or not at all,
and results move forward monotonically. Rounds run back-to-back while changes
arrive and stop when the slot is idle; batching under load is automatic. Each
round also emits `live.progress` with the highest decoded `xid8`, so
`awaitTxId` resolves for writes that do not change the query. pgoutput reports
32-bit xids; the engine extends them to `xid8` with the current epoch.

Delivery to clients is at-least-once per result version. A reconnecting client
presents its version; realtimed sends the missing deltas if it still holds them,
otherwise a full `live.rows` replacement.

### 4. The incremental engine

**Principle.** PostgreSQL evaluates every expression: casts, collations,
operators, extension functions and NULL semantics are exactly the database's.
The engine rewrites the subscribed query into maintenance queries that are
restricted to the keys a change can affect, runs them at the round snapshot,
and diffs their output against what it last emitted. Work is proportional to
the change and the rows it touches, not to the size of the result.

**Lineage.** At compile time the query is normalized and every base relation
contributes hidden lineage columns (its primary key) to the maintained
relation. The engine stores, per query, an index from lineage key to the result
row IDs derived from it, plus a hash of each emitted row. For a change to table
T with key k (old or new image; the default replica identity supplies the old
key, so `REPLICA IDENTITY FULL` is not required):

- removal candidates are the cached rows whose T-lineage is k;
- replacements come from the query re-evaluated with `T.pk = ANY($keys)`,
  which the database answers through its indexes;
- the delta is the difference.

**Maintenance strategy by construct:**

| Construct | Strategy |
|---|---|
| WHERE, projections, immutable functions, CASE, casts | Lineage-keyed (as above) |
| Inner and outer joins | Lineage-keyed across every joined relation |
| GROUP BY with `count`, `sum`, `avg`, `bool_and/or` | Group state updated from input deltas; values computed by the database over the changed input rows |
| `min`, `max` | Incremental on insert; group recomputed when the current extreme is removed |
| Holistic aggregates (`string_agg`, `array_agg`, `json_agg`, percentiles, `mode`) | Affected groups recomputed |
| DISTINCT, HAVING | DISTINCT as GROUP BY with counts; HAVING filters maintained groups |
| ORDER BY with LIMIT/OFFSET | Top-k over the maintained input; a bounded refill query when rows leave the window |
| Window functions | Affected partitions recomputed |
| EXISTS, IN, NOT EXISTS subqueries | Semi- and anti-join lineage |
| Uncorrelated scalar subqueries | Tracked as a single dependency; dependents recomputed when its value changes |
| Non-recursive CTEs, views | Inlined, then maintained like their definitions |
| UNION, INTERSECT, EXCEPT (with or without ALL) | Per-branch lineage with multiplicities |
| Recursive CTEs, LATERAL over table data, set-returning functions over table data | Recompute fallback (below) |

**Recompute fallback.** A construct without a keyed strategy is maintained by
re-running the query at the round snapshot and diffing, coalesced across all
subscribers. Subscriptions report `maintenance: "incremental" | "recompute"`,
and recompute queries carry their own, tighter limits. The fallback list
shrinks as strategies are added; it is never a reason to accept a query that
would otherwise be refused.

**Accepted queries.** One read-only `SELECT` whose result depends only on table
data and its parameters, checked in `pg_proc` at compile time:

- `IMMUTABLE` functions and operators are accepted.
- Built-in (`pg_catalog`) `STABLE` functions are accepted, because maintenance
  runs with pinned session settings (`TimeZone`, `DateStyle`, `search_path`,
  and so on, fixed per subscription). This admits `to_char` and timezone
  arithmetic.
- User-defined `STABLE` functions are rejected, since they may read tables the
  engine cannot track. A user-defined function mislabeled `IMMUTABLE` that
  reads tables is the customer's error and is documented as such.
- `VOLATILE` functions are rejected: `now()`, `random()`, `gen_random_uuid()`,
  `nextval()`.

Every referenced table
needs a primary key or replica identity index. Partitioned tables are published
through their root. Materialized views, foreign tables, temporary tables,
system catalogs and `FOR UPDATE` are rejected. The compiler also rejects
queries whose planner cost estimate exceeds the plan limit.

**Schema changes.** pgoutput does not carry DDL, so enabling live queries
installs a platform event trigger that records DDL in a platform table included
in the publication. The engine recompiles affected queries at the round where
the DDL commits. A query that no longer compiles ends with `live.error`
`schema_changed`; the SDK re-subscribes with a freshly sealed query.

**Where maintenance runs.** On the primary in the first release; maintenance
queries are read-only and indexed by construction. Running them on the
production-class standby (PostgreSQL 16+ supports logical decoding on standbys)
is a later step.

### 5. Authorization

**Sealed mode.**

- The backend seals `{database, sql, params, settings, exp, kid}` with the
  per-database live secret using AES-256-GCM (available in Web Crypto, so
  sealing also works in edge runtimes). Sealing is local and makes no network
  call.
- Seals expire after 60 seconds by default and 5 minutes at most. Renewal keeps
  the subscription when the query fingerprint matches.
- Sealed queries execute as the database binding's live reader role, whose
  table grants the customer controls.
- Revocation:
  - Secret rotation, with overlap, invalidates outstanding seals.
  - Endpoint access controls and ticket revocation (ADR-1323 era) close
    connections within about a second.

**RLS mode.**

- The endpoint must use `oidc_jwt`. The client sends a Data API query (the
  PostgREST subset that `@gregale/data` emits: select with foreign-key
  embedding, filters, order, limit), which livequeryd compiles to SQL.
- Snapshots and maintenance queries run with `SET LOCAL ROLE` to the Data API
  binding login (granted to `gregale_live` at enablement) and
  `request.jwt.claims` set from the verified token, exactly as the Data API
  does. PostgreSQL row-level security therefore filters every row
  of every delta.
- RLS-mode subscriptions share maintenance only when the query and claims are
  identical, so they carry their own per-database limit.
- When the JWT expires, the subscription is `stale` until the client renews it.
- Policy changes arrive as DDL and resnapshot the affected queries.

### 6. Scale-to-zero, failover and recovery

- **Parked databases.**
  - A subscription never keeps a database awake. realtimed serves the cached
    result while subscribers remain connected.
  - On park, liveq checkpoints its registry, lineage indexes and result
    versions to the data volume. On wake (any write wakes the database anyway)
    it restores them and resumes from the slot.
  - Subscribing to a parked database with no cached result wakes it through
    schedd.
- **Engine or livequeryd restart:** resume from the checkpoint and the slot.
- **Slot loss:** point-in-time restore, `max_slot_wal_keep_size` exceeded, or
  failover on majors below 17. Every query resnapshots and clients receive a
  full `live.rows` replacement.
- **Failover on 17 and later:** the slot is created with `failover = true` and
  synchronized to the standby, so failover continues without a resnapshot.
- **Changing `wal_level`:** this requires a restart of the database, so
  enabling or disabling live queries performs a controlled restart. The API
  states this.

### 7. Limits, pricing and metrics

- **No new meter or price.** Following the compute-only pricing principle:
  - the engine runs on the customer's database compute, which is already
    metered;
  - live connections count against the ADR-1323 connection limits;
  - the larger WAL archive volume is billed as database history.
- **Limits live in `pkg/api/limits.go`,** per plan or database class. Starting
  values, to be confirmed by measurement before general availability:
  - live queries per database;
  - distinct compiled queries per database;
  - RLS-mode subscriptions per database;
  - rows per result: 10,000;
  - bytes per result;
  - the engine's memory share of the VM;
  - the recompute fallback budget per round;
  - planner-cost ceiling per query;
  - SQL size 32 KiB; 256 parameters of 16 KiB each, matching Neon.
- **Refusals** use RFC 7807 problems with code `live_query_limit`, carrying the
  limit, the observed value and a docs URL.
- **Metrics (§12 names):**
  - `livequery_subscriptions`;
  - `livequery_compiled_queries`;
  - `livequery_round_duration_seconds`;
  - `livequery_commit_to_delta_seconds` (from the pgoutput commit timestamp to
    the delta leaving livequeryd);
  - `livequery_recompute_total`;
  - `livequery_resnapshots_total{reason}`;
  - `livequery_engine_memory_bytes`;
  - `livequery_slot_lag_bytes`.

### 8. Phases and exit criteria

- **Phase 0, correctness first.**
  - This ADR, an engine prototype covering lineage-keyed joins and aggregates,
    and a differential oracle harness.
  - The harness generates random schemas, queries and concurrent write
    workloads, and asserts that the client-materialized result equals the query
    executed at the reported snapshot after every delta.
  - Exit when there are zero divergences across the generated corpus and the
    workload suite, including round-boundary races.
- **Phase 1, operator preview.**
  - Native databases, sealed mode.
  - Keyed strategies for joins, algebraic and holistic aggregates, DISTINCT,
    HAVING and top-k; recompute fallback for everything else.
  - realtimed frames, `@gregale/data/live` with `useLiveQuery`, SSR
    `initialData`, `awaitTxId`.
  - Checkpointing across park.
  - Metal tests and leakcheck for the guest process and slot lifecycle.
- **Phase 2:**
  - RLS mode;
  - keyed window functions and subqueries;
  - failover slots;
  - TanStack DB collections;
  - published limits and a commit-to-client latency benchmark.
- **General availability** requires:
  - the Phase 2 benchmarks published;
  - a qualification runbook (park and wake, failover, slot loss, schema
    change);
  - a bake-off against Neon Realtime on the same application.
- **Later, each with its own ADR:**
  - standby execution;
  - Neon-backed databases (a replication consumer keeps a Neon compute awake,
    so it must connect only while Neon reports the compute active);
  - bring-your-own Postgres.

## Consequences

- **Database settings.** Native databases with live queries run
  `wal_level=logical` and hold one logical slot. WAL volume rises slightly, and
  a stalled engine can hold WAL up to `max_slot_wal_keep_size`.
- **Resources.** Maintenance work and engine memory come out of the customer's
  database VM. A small class with many complex live queries reaches its limits
  sooner; the limits make that explicit rather than letting Postgres starve.
- **New moving parts.** The guest gains a supervised process; database nodes
  gain livequeryd; realtimed gains live frames and a result cache.
- **SDK.** `@gregale/data` becomes the single client for request/response and
  live data.
- **ADR-1114 feeds remain.** They stay the low-level row-change notification
  feature; live queries do not depend on the Commit relay.

## Rejected alternatives

- **Re-run and diff for every change.** Simple and fully expressive, but cost
  grows with result size and subscriber diversity. It survives only as the
  bounded fallback.
- **pg_ivm or other trigger-based maintenance on the primary.** It adds work to
  every customer write, needs one materialized view per subscription shape, and
  cannot apply per-subscriber row-level security.
- **An external dataflow engine (Materialize, Feldera/DBSP, ReadySet).**
  - Licensing and footprint do not fit a per-database VM.
  - Evaluating SQL outside PostgreSQL drifts from its semantics for collations,
    numerics and extension functions.
- **Our own expression evaluator.** It would have the same semantic drift, and
  it would forbid extension functions.
- **A WAL consumer outside the VM over TCP.** Database VMs deliberately have no
  TCP listener, and an external consumer would keep parked databases awake.
- **Building on ADR-1114 trigger feeds.** It adds latency and work to every
  write, caps events at 4 KiB, and provides no snapshots, joins or aggregates.
- **Offering Neon Realtime to Gregale customers.**
  - It ties customers to a competitor's beta SDK and protocol.
  - It works only on Neon-backed databases, which Gregale plans to leave.

## Open questions

- Engine memory share and the starting limit values per class, from Phase 0
  measurements.
- Whether compilation belongs in livequeryd or apid. livequeryd keeps catalog
  access local to the database node.
- Whether live queries ride existing realtime endpoints (proposed) or get a
  dedicated endpoint kind.
- Seal format compatibility: whether to accept Neon-style sealed-query shapes
  to ease migration from Neon Realtime.
