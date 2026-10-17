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
import { createLiveSealer, sql } from '@gregale/data/live';
const live = await createLiveSealer({
  secret: process.env.GREGALE_LIVE_SECRET!,
  database: process.env.GREGALE_LIVE_DATABASE!,
});
const sealed = await live.seal(
  sql`SELECT c.id, c.name, count(m.*) FILTER (WHERE m.created_at > r.read_at) AS unread
      FROM channel_members cm
      JOIN channels c ON c.id = cm.channel_id
      LEFT JOIN messages m ON m.channel_id = c.id
      JOIN reads r ON r.channel_id = c.id AND r.user_id = cm.user_id
      WHERE cm.user_id = ${userId}
      GROUP BY c.id, c.name, r.read_at`,
);

// Browser: subscribe through the app's realtime endpoint.
const connection = new LiveConnection({ connect: openRealtimeSocket });
const { rows, status, awaitTxId } = useLiveQuery(connection, sealed, { refresh: fetchSealed, initialRows });

// RLS mode: a Data API query with the user's JWT; policies decide visibility.
const q = data.from('messages').select('*, author(*)').eq('channel_id', id)
  .order('created_at', { ascending: false }).limit(50).live();
```

- A database is enabled with `gregale postgres live enable <database>` (API
  `POST /v1/postgres/databases/{id}/live`). Enabling issues the per-database
  live secret.
  - *As built (2026-10-17):* the secret is returned once and apid cannot read
    it back. `--app` stores it as the app's `GREGALE_LIVE_SECRET`; otherwise
    the CLI prints it.
  - Automatic injection into bound apps was dropped. It would have made the
    secret a managed app secret, a third managed owner in `app_secrets`
    beside Postgres bindings and object storage, threaded through clone and
    promotion fences. It would also have required apid to keep a readable
    copy.
- Subscriptions travel over an app's realtime endpoint as new v2 frame types.
  The endpoint's app must hold a binding to the target database.
  - Client frames: `live.subscribe`, `live.renew`, `live.unsubscribe`.
  - Server frames: `live.rows`, `live.changes`, `live.progress`,
    `live.renewed`, `live.error`.
- Each change carries `upsert` (opaque row ID and row) or `remove` (row ID);
  each batch carries the `xid8` transaction IDs it covers and the snapshot the
  result now reflects. An ordered result also carries its row order whenever
  rows were added or moved.
- `pkg/livequery/liveproto` defines the frames and a reference client. Its
  session fixtures (`testdata/sessions.json`) are the conformance suite that
  the JavaScript SDK runs too.

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
- **livequeryd (new, one per database node).** Compiles queries (PostgreSQL's
  own parser, libpg_query, through `github.com/wasilibs/go-pgquery`, which runs
  it as WebAssembly on wazero, so binaries stay static with `CGO_ENABLED=0`;
  catalog-backed analysis; maintenance plan), registers plans with the VM's engine through vmmd's vsock
  relay as pgproxyd does, wakes parked databases through schedd, and serves
  delta streams to realtimed. It deduplicates identical subscriptions by plan
  fingerprint so a query is maintained once per database however many clients
  hold it.
- **realtimed.** Carries live frames on v2 connections. Live-query bytes count
  against the per-connection queue and the ADR-1323 node outbound budget; live
  connections count against plan connection limits.
  - *As built (2026-10-17):* realtimed proxies (`pkg/livequery/liveproxy`) and
    does not open seals. Each database's hub opens its own seals, keeps
    per-subscription versions, and sends whole results to subscribers that
    join. So live secrets never leave the database node.
  - realtimed needs only routing: a seal's clear header names its key ID,
    which maps to the database and its hub. One upstream WebSocket per
    database carries every client subscription to it. RLS-mode JWT
    verification will sit in the same place.
- **apid.** Owns customer intent: enabling live queries per database, the live
  secret (sealed at rest, rotation with overlap), and plan limits. It delivers
  live secrets to the database's livequeryd, and key-ID routes (not secret) to
  realtimed with endpoint registrations. It never executes live queries.
- **Ownership is unchanged elsewhere:** schedd remains the only waker and
  writer of instance state; vmmd remains the only component touching
  Firecracker; realtimed remains the owner of client connections.

### 3. Consistency model

The engine maintains results in rounds. Each round is a consistent snapshot:

1. Open a `REPEATABLE READ` transaction and take `ACCESS SHARE` locks on every
   table a live query reads, before any statement takes the snapshot (`LOCK`
   does not). TRUNCATE and table rewrites are not MVCC-safe: one that commits
   while a round reads would make a table look empty to a snapshot that
   predates it. The locks keep them waiting until the round commits; ordinary
   writes take weaker locks and proceed. A round whose locks time out or lose a
   deadlock retries before it has emitted anything. Then take the snapshot S
   (`pg_current_snapshot()`).
2. Emit a non-transactional logical message (`pg_logical_emit_message`) as a
   round marker, reading `pg_snapshot_xmax(pg_current_snapshot())` with it as
   the reference for extending decoded xids. Every transaction visible in S
   inserted its commit record before the marker, so draining the slot up to
   the marker decodes all of them; the walsender sends WAL only once flushed,
   so this also covers `synchronous_commit=off`. Decoded transactions not
   visible in S are held for the next round.
3. Maintain every affected query using only S-visible changes, executing all
   maintenance SQL inside the round's transaction, so every row read reflects S.
4. Emit, per query, one delta tagged with the S-visible `xid8`s, then commit
   the round and advance the slot's confirmed position past fully applied
   transactions.

A client therefore always holds exactly the result of the query at some
committed snapshot: a transaction's effects appear all at once or not at all,
and results move forward monotonically. Rounds run back-to-back while changes
arrive and stop when the slot is idle; batching under load is automatic.

Every result frame carries S. After a round's changes, realtimed sends
`live.progress` with S for the database: every live subscription on it now
reflects S, even one whose rows did not change. `awaitTxId(x)` resolves once
the subscription's snapshot makes x visible, by the rules of
`pg_visible_in_snapshot`. A highest-xid watermark could not decide this,
because transactions commit out of xid order. pgoutput reports 32-bit xids;
the engine extends them to `xid8` with the current epoch.

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

**Outer joins.** A change to a relation on the nullable side of an outer join
can create or remove NULL-extended rows of the preserved side, and a WHERE
clause can hide the pairs that explain it. Queries with outer joins therefore
also maintain the lineage of the FROM clause alone, before WHERE: one key tuple
per join row. A change expands to the preserved-side keys that matched the
changed rows before (from that join lineage) or match them now (re-read at the
snapshot), and both the join lineage and the result are re-read for the
expanded keys. The join lineage costs memory proportional to the unfiltered
join, so it counts against the result limits.

**Parameters.** Every derived query carries each query parameter as a hidden,
explicitly typed column, so parameter numbers and types stay fixed even where
the clause that used a parameter (WHERE, the select list) is dropped.

**Maintenance strategy by construct:**

| Construct | Strategy |
|---|---|
| WHERE, projections, immutable functions, CASE, casts | Lineage-keyed (as above) |
| Inner and outer joins | Lineage-keyed across every joined relation |
| GROUP BY with `count`, `sum`, `avg`, `bool_and/or` | Group state updated from input deltas; values computed by the database over the changed input rows |
| `min`, `max` | Incremental on insert; group recomputed when the current extreme is removed |
| Holistic aggregates (`string_agg`, `array_agg`, `json_agg`, percentiles, `mode`) | Affected groups recomputed |
| DISTINCT, HAVING | DISTINCT as GROUP BY with counts; HAVING filters maintained groups |
| ORDER BY with LIMIT/OFFSET | Ordered window ranked by the database; a bounded refill query when rows leave the window (see Phase 1 progress) |
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
installs a platform event trigger that logs every DDL command, with the tables
it touched, in `gregale_live.ddl_log`. That table is published like any other,
so the log row is decoded in the round where the command commits.

- **Recompile.** The round recompiles every query that reads a touched table.
  A command that touched anything else (a function, a type, a schema)
  recompiles every query. Recompiling runs in a savepoint inside the round's
  transaction, so a failure does not abort the round.
- **Outcome for subscribers:**
  - a query that no longer compiles ends with `live.error` `schema_changed`;
    the SDK re-subscribes with a freshly sealed query;
  - a query whose result columns or parameter types changed starts over with
    a full `live.rows`;
  - any other affected query is re-read at the round snapshot and diffed, so
    its subscribers see ordinary changes.
- **Consistency.** The round's locks keep DDL on live tables out of a round in
  progress, so the catalog a round compiles against matches its snapshot.
- **Renamed and dropped tables.** Live tables are locked by their current names,
  resolved from their OIDs before each round. A renamed table is still locked,
  and a dropped one is skipped until the round that decodes the drop closes
  its queries.
- **Uncached statements.** Maintenance statements are described on every
  execution, never cached, because a cached plan fails once its result types
  change.

**Where maintenance runs.** On the primary in the first release; maintenance
queries are read-only and indexed by construction. Running them on the
production-class standby (PostgreSQL 16+ supports logical decoding on standbys)
is a later step.

### 5. Authorization

**Sealed mode.**

- The backend seals `{db, sql, params, iat, exp}` with the per-database live
  secret. Sealing is local and makes no network call. `pkg/livequery/seal`
  implements it:
  - **Secret:** `glsk1_<kid>_<key>`, a 16-hex-digit key ID and 32 bytes of key
    material.
  - **Seal:** `gls1.<kid>.<nonce ‖ ciphertext>`. The cipher is AES-256-GCM
    under HKDF-SHA256(key, "gregale live seal v1"), and `gls1.<kid>` is
    authenticated as additional data.
  - **Parameters:** PostgreSQL text values (null is NULL), so the database
    parses each one as its parameter's type.
  - **Web Crypto:** every primitive is available there, so edge runtimes
    can seal. A known-answer vector (`testdata/vector.json`) is reproduced
    byte for byte by Web Crypto in Node.
- Key IDs are unique across databases. A seal opens only with its own
  database's key, and the key ring refuses a payload that names another
  database. Rotation keeps the previous key until a retirement time (default
  overlap 24 hours). Removing a key revokes its outstanding seals at once.
- Limits (`pkg/api/limits.go`):
  - SQL up to 32 KiB, 256 parameters of 16 KiB each (as Neon);
  - a sealed payload up to 48 KiB;
  - lifetime from 1 second to 5 minutes, default 60 seconds;
  - 30 seconds of clock skew.
- The seal carries no session settings. Settings that RLS policies read would
  need per-plan-group isolation inside a round's single snapshot transaction.
  RLS mode covers per-user policies until then.
- **Renewal and staleness:**
  - `live.rows` and `live.renewed` tell the browser when its seal expires,
    since it cannot read the seal.
  - The SDK renews before then with `live.renew` and a fresh seal. Renewal
    keeps the subscription only when the query fingerprint (database, SQL,
    parameters) matches.
  - A subscription whose seal lapses turns stale (`live.error seal_expired`,
    not closed) and receives nothing until renewed.
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
  - **Checkpoint contents.** A checkpoint (`Engine.WriteCheckpointFile`,
    written atomically) holds the engine's state as of its last round S:
    registrations, member caches, aggregate state and windows.
  - **Slot confirmation.** The engine confirms the persistent slot only when a
    checkpoint is durable, so the slot never moves past state a crash could
    lose. WAL is therefore retained since the last checkpoint, and livequeryd
    checkpoints periodically as well as on park.
  - **Resume.** A restored engine streams from the slot's confirmed position.
    It skips transactions visible in S, which were already applied, and
    applies the rest.
  - **Recompiled plans.** Plans are compiled again at restore. Members whose
    plan changed while the engine was down start over with a full result.
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
  - bytes per result sent whole: 2 MiB (`LiveQueryResultBytesMax`, half of
    realtimed's per-connection outbound queue);
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

### Phase 0 result (2026-10-16)

The prototype lives in `pkg/livequery`. It covers capture, the compiler, the
engine and the differential oracle.

- **What runs incrementally:**
  - lineage maintenance for inner and outer joins (including self-joins and
    composite keys);
  - group-keyed maintenance with HAVING;
  - every other construct falls back to recompute.
- **Oracle workload:**
  - concurrent writers with multi-row and primary-key-changing statements,
    join-key updates, rollbacks, `synchronous_commit=off` and TRUNCATE;
  - after every round, each result and each internal cache is compared with
    PostgreSQL at the round snapshot, and every decoded transaction's
    visibility verdict is checked against `pg_visible_in_snapshot`.
- **Soak:** 25 seeds, 42,281 transactions, 1,107 rounds and 15,498 comparisons,
  with 549 transactions held across round boundaries. Zero divergences.
- **What the oracle found** (each fixed before the soak):
  - TRUNCATE is not MVCC-safe, hence the round locks in step 1;
  - parameters vanished from derived queries, hence the hidden parameter
    columns.

Phase 0 did not cover:

- **Algebraic aggregates:** maintained by group re-aggregation, not yet
  incrementally.
- **Top-k, window functions, subqueries and DISTINCT:** still recompute.
- **Durability and schema changes:** checkpoints, DDL handling and
  per-subscription settings.
- **Performance:** not measured. That is the first Phase 1 work, together with
  the operator preview.

### Phase 1 progress (2026-10-16)

- **Plan groups.** Subscriptions with the same SQL share one plan group,
  compiled once. Every maintenance statement serves all members: their
  arguments are unnested from arrays and joined LATERAL, and rows carry their
  member. Identical arguments share a member. Statements per round depend on
  the number of distinct queries, not subscribers.
- **Incremental aggregates.**
  - `count`, `count(t.*)`, and `sum` and `avg` over integer and numeric
    arguments are kept per group. The engine uses exact decimal arithmetic
    and tracks numeric display scales, so text matches PostgreSQL.
  - PostgreSQL computes `min` and `max`: from the current extreme plus new
    values, or from the group's cached values after a removal that may have
    taken the extreme.
  - PostgreSQL sums groups that hold NaN or Infinity.
  - One batched statement evaluates the select list and HAVING from group
    keys and aggregate values.
  - Plain DISTINCT and aggregates without GROUP BY use the same path. Other
    aggregates, and sums of floating-point values, re-aggregate the affected
    groups.
- **Benchmark** (local PG16 on a shared, loaded laptop; 100k orders, 200
  writes/s):

  | Subscribers | Statements/round | Commit-to-delta |
  |---|---|---|
  | 100 | about 11 | p50 23 ms, p95 46–103 ms |
  | 1,000 | about 11 | p50 41 ms, p95 92 ms |

  Before plan groups, 300 subscribers needed 417 statements per round
  (p95 227 ms). Tail latency needs a dedicated host to measure.
- **Oracle soak** with aggregates, DISTINCT, numeric specials and
  multi-member plan groups: 25 seeds, 26,808 transactions, 1,097 incremental
  passes, 11,652 comparisons, zero divergences.
- **Keyed subqueries.**
  - Supported forms:
    - EXISTS and NOT EXISTS anywhere in WHERE;
    - IN and `op ANY` as top-level AND conditions;
    - over one table with a primary key, without aggregates or nesting.
  - The subquery table becomes a relation that result rows carry no lineage
    for. A per-subquery pair cache (the FROM clause inner-joined with that
    table on the subquery's condition, before WHERE) records which outer rows
    each subquery row matches.
  - A change to the subquery table re-reads the outer rows it matched before
    or matches now. Unqualified references resolve as PostgreSQL scopes them:
    the subquery table first, then the one FROM relation with the column.
  - NOT IN recomputes, since one NULL changes its result for every row; so do
    subqueries outside WHERE.
  - Soak with these predicates mixed into joins and aggregates: 25 seeds,
    26,728 transactions, 10,495 comparisons, zero divergences.
- **Ordered windows** (ORDER BY ... LIMIT [OFFSET] over inner joins):
  - The engine keeps each member's window: the first LIMIT + OFFSET rows,
    ordered by the query's sort items, then every relation's primary key.
    With ties a query may return any of the tied rows; the keys pick one
    answer and make the order total.
  - A change re-reads only the rows derived from changed keys, with the
    window's LIMIT. PostgreSQL ranks them against the rest of the window in
    one batched statement, using the query's own types, collations
    (`pg_collation_for`) and NULL ordering.
  - A full window keeps only candidates that rank up to its old last row:
    rows past it, which the engine does not hold, could outrank the rest.
    Only a window that loses rows nothing replaces is read again.
  - Edits that leave the sort values unchanged update rows in place, and
    members with no affected rows are skipped.
  - For a latest-N feed, a new row enters and pushes the last one out with
    no refill.
  - LIMIT and OFFSET may be constants, parameters, ALL or NULL. LIMIT
    without ORDER BY keeps a key-ordered window and an unordered result.
  - Still recompute:
    - grouped or DISTINCT queries with ORDER BY or LIMIT;
    - outer joins and subqueries with ORDER BY or LIMIT (their caches would
      span the whole table);
    - FETCH WITH TIES, ORDER BY USING, and array or record sort keys.
  - The oracle checks each window row by row and in order against the
    window query, and the visible rows against the query with its
    tie-breakers.
  - Soaks, with every delta also passing through the frame contract, had
    zero divergences:

    | Workload | Seeds | Transactions | Comparisons | Re-ranks | Refills |
    |---|---|---|---|---|---|
    | Windows only (ties, mixed directions and NULL orders, collations, positions, output names, hidden sort keys, OFFSET, parameter and NULL limits) | 15 | 11,007 | 7,762 | 1,986 | 318 |
    | Mixed with every other mode | 25 | 18,211 | 6,803 | 436 | 58 |

  - Benchmark: 1,000 latest-10 feeds over 200 customers, 200 writes/s.
    Statement time per round is medians across runs on the loaded laptop;
    end-to-end commit-to-delta stayed the same at this scale, since other
    queries dominate it:

    | Maintenance | Feed statement time per round |
    |---|---|
    | Recompute (re-reads every member's 10 rows) | 2.0 ms |
    | Windows (changed rows, then one rank statement) | 1.4–1.6 ms |

    Windows re-read rows only for members whose rows changed, so the gap
    grows with window size and join cost.
- **Sealed queries and the frame contract** (section 5 and section 1): seals,
  the key ring and the frames. The oracle harness now sends every engine
  delta through JSON frames into the reference client, which checks
  versions, snapshots, transaction visibility and ordering. After each
  round, every client must see every applied transaction.
- **Schema changes and checkpoints** (sections 4 and 6).
  - A deterministic test covers each schema-change outcome: an index (no
    effect), an added column (`SELECT *` starts over; the rest diff), a
    column type change, a renamed column (closed), and a dropped table
    (closed).
  - A second test covers resuming from checkpoints: writes made before and
    during a restart, back-to-back restores, a schema change while the
    engine is down, and a lost slot.
  - The oracle now adds random schema changes to its writers' transactions:
    columns added and dropped, numeric scale and text/varchar type changes,
    and indexes. It also restarts the engine from a checkpoint between
    rounds, a quarter of the time losing the slot.
  - The reference client caught one bug before the soak: a full result sent
    after a schema change carried the subscription's original columns. A
    reset now carries the current plan's columns.
  - The first heavy soak reported two divergences that were PostgreSQL's
    own nondeterminism, not engine errors. Altering a numeric column to a
    scaled type and back left old rows printing `-3.0000` and new rows
    `-3`. `min` and `max` over equal values return whichever row the scan
    meets last, so either text is a correct answer, and a fresh query can
    return either.
    - The workload now moves numeric columns only between scaled types, so
      equal values always print alike.
    - Subscribers can see the same ambiguity: a live `min` or `max` over
      equal values that print differently may show either form, as a
      re-run query may.
  - Soaks, with zero divergences:

    | Workload | Seeds | Transactions | Comparisons | Recompiles | Restarts | Re-deliveries skipped |
    |---|---|---|---|---|---|---|
    | Heavy (2.5% of statements are schema changes, restart after 25% of rounds) | 100 | 68,777 | 22,472 | 6,480 | 358 | 69,760 |
    | Default rates (0.5% schema changes, restart after 8% of rounds) | 25 | 17,404 | 5,349 | 792 | 36 | 7,948 |

    A quarter of the restarts lost the slot.
- **The JavaScript SDK** (`@gregale/data/live`, `@gregale/data/live/react`):
  - `createLiveSealer` and the `sql` tag;
  - `LiveConnection`, with renewal before expiry, reconnect from the version
    held, and `awaitTxId` by snapshot visibility;
  - the `useLiveQuery` hook, with server-rendered initial rows.

  The SDK runs the same seal vector and session fixtures as the Go
  packages. The contract gained one rule from the SDK work: after a client
  subscribes again, a full result may start a new version sequence, since
  a restarted realtimed no longer knows the versions it sent.
- **The live session server and livequeryd.**
  - **`pkg/livequery/liveserver`** is realtimed's half of the frame contract,
    built as a `Hub` per database that owns the engine and its round loop.
    It:
    - opens seals with the key ring and checks the database they name;
    - tracks each client subscription's version and seal expiry, and
      renders engine deltas as frames;
    - turns lapsed subscriptions stale, and sends the whole current result
      again on renewal;
    - sends `live.progress` after every round;
    - closes connections whose bounded outbound queue fills
      (`slow_consumer`).
  - **Limits** (`pkg/api/limits.go`, starting values):
    - 10,000 subscriptions and 1,000 distinct queries per database;
    - 64 subscriptions per connection;
    - 10,000 rows per result or ordered window (the engine closes a larger
      result with `live_query_limit`, carrying the limit and the observed
      value).
  - **Metrics:** the §7 names, plus checkpoint duration, slow consumers,
    window refills and applied transactions.
  - **`cmd/livequeryd`** runs a hub beside the database:
    - installs the schema-change log and creates the publication;
    - restores from its checkpoint and checkpoints periodically and on
      shutdown;
    - reports slot lag;
    - with `-dev-listen`, serves frames over a local WebSocket until
      realtimed carries them.
  - **Restored subscriptions.** Subscriptions restored from a checkpoint
    belong to connections that no longer exist. They stay registered for a
    grace period (two minutes), so reconnecting clients join results that
    are already maintained and receive them at once. Then they are released.
  - **End-to-end tests:**
    - over real PostgreSQL and a real WebSocket, with every frame checked
      by the Go reference client;
    - against the JavaScript SDK itself: a Go test drives a Node script
      through changes, `awaitTxId`, three-second seals renewed across ten
      seconds, a dropped connection, and a schema-change close.
  - **Smoke test of the `livequeryd` binary.** A client stayed subscribed
    across SIGTERM (checkpoint), a write while the daemon was down, and a
    restart (restore). The client reconnected on its own and received the
    missed row. The smoke test found two bugs:
    - engine subscription IDs repeated those restored from the previous
      process, so the hub now prefixes them per process;
    - reinstalling the schema-change log at start counted as a schema change
      and re-read every query, so the trigger now ignores commands on its own
      schema.
  - **Two engine fixes:**
    - **Late subscribers.** A subscriber joining a result the engine already
      maintained never received it whole. It now gets a reset at the next
      round.
    - **Round marker flush.** The walsender sends only flushed WAL, and a
      non-transactional marker waited for the WAL writer (200 ms by
      default), so quiet databases paid up to that per round. The marker
      statement now takes a transaction ID, so it commits and flushes at
      once; decoding skips the empty transaction. On the loaded laptop, idle
      rounds went from p50 406 ms to 9 ms. Clean numbers need a quiet host.

- **Memory, and the compiler split** (2026-10-17).
  - **Where the memory goes.** The engine itself is small: 101 queries and
    2,000 subscriptions over 10,000 rows hold about 23 MiB of Go heap. The
    fixed cost is PostgreSQL's parser, which runs as WebAssembly:
    - starting it reserves about 280 MiB of address space, mostly virtual;
    - it keeps about 9 MiB of decoded module on the heap;
    - it spikes resident memory while wazero compiles the parser to native
      code: 65 to 127 MiB on the first parse.
  - **The split.** Rounds never parse; they run statements compiled ahead of
    time. So the engine takes a `Compiler`, and `livequery.RemoteCompiler`
    sends compilation over HTTP (`CompilerHandler`) to the host. Plans travel
    as JSON, with fingerprints identical to in-process compilation. Schema
    changes and restores compile remotely too, and a compiler outage fails
    the round (retried) instead of closing subscriptions.
  - **`livequeryd -role`:**
    - `engine` runs beside the database and never starts the parser;
    - `compiler` runs on the host;
    - `all` runs both in one process.
  - **Measured on the laptop** (macOS RSS, indicative), 600 subscriptions
    over 60 distinct queries:

    | Engine process | Peak RSS | Final RSS | Go heap |
    |---|---|---|---|
    | With the parser (`-role all`) | 197 MiB | 31 MiB | 40 MiB |
    | Without it (`-role engine`) | 66 MiB | 25 MiB | 29 MiB |

    The engine idles at 23 MiB; the compiler process holds about 24 MiB.
  - **Open question resolved:** compilation belongs in livequeryd on the
    host, as section 2 drew it.
  - **Still owed:** cgroup-accurate numbers inside a Linux database VM, and
    the engine's memory ceiling per class.
- **realtimed carries live queries** (2026-10-17, on main's v2 protocol).
  - **pkg/realtime.** v2 connections pass `live.*` frames to `Config.Live`
    before channel access checks and read-only tickets (live frames are not
    channel traffic; the seal is the capability). They queue the replies on
    the connection's outbound budget. Without a configured proxy, a
    subscription is refused with `unavailable`.
  - **`pkg/livequery/liveproxy`.** One upstream WebSocket per database, with
    per-session subscription IDs and progress sent once per session.
    - When an upstream drops, its subscriptions turn `unavailable` (stale),
      and the proxy reconnects and subscribes them again with their latest
      seals.
    - Version offsets keep each client's sequence rising across hub
      sessions.
    - A seal that expired while the upstream was away becomes a renewable
      `seal_expired`.
  - **Routing.** Routes arrive with each endpoint's registration (see "apid
    enablement" below). The preview's static routes file is gone.
  - **Hub additions:**
    - a per-connection limit for trusted proxies (`livequeryd
      -max-per-connection`);
    - a round's changes too large for one frame are sent as a full result,
      split to size;
    - `Hub.Subscriptions`, read on the hub's loop.
  - **Tests:**
    - the proxy over a real hub, with two client sessions, upstream cuts,
      resumption above the held versions, and an expired seal renewed across
      a cut; race-free;
    - a full-path test: a real `realtime.Manager` v2 connection carries a
      sealed query through the proxy and hub to PostgreSQL and back, checked
      by the reference client.
  - **Not yet:** see "apid enablement" below.
- **apid enablement** (2026-10-17).
  - **API.** `GET`, `POST` and `DELETE /v1/postgres/databases/{id}/live`, and
    `POST .../live/rotate`, in the OpenAPI spec and the Go, Node and Python
    SDKs. The CLI is `gregale postgres live status|enable|rotate|disable`.
  - **Storage.** `managed_postgres_live_keys` holds at most one active and
    one retiring key per database. Each secret is sealed with age to
    livequeryd's recipient (`FAAS_APID_LIVE_KEY_RECIPIENT_FILE`). apid holds
    only the public half, so it cannot read a stored secret.
  - **Rotation.** Rotation keeps the previous key for an overlap: 24 hours
    by default, at most 7 days (`LiveQuerySecretRotationOverlapMax`).
    Overlap 0 and disable revoke at once.
  - **Eligibility.** A backend runs live queries only when its configuration
    names a hub (`live_hub_url`, with `{database_id}` substituted).
    Configuration refuses one on Neon backends, so on main today no
    production database can enable them. The native provider (ADR-1027) will
    set it.
  - **livequeryd's keys.** livequeryd reads its database's sealed keys from
    the control plane, over a read-only connection (`-keys-dsn-file`). It
    opens them with its age identity (`-keys-identity`) and replaces the
    hub's key ring when they change (`-keys-refresh`, 5 seconds).
    - When a key leaves the ring, the subscriptions its seals opened close
      at once with `seal_rejected`. A key leaves through a rotation without
      overlap, disabling, or retirement.
    - A failed read keeps the last ring, so a control-plane outage does not
      stop live queries. apid is down with it, so no revocation is lost.
    - `-listen` serves realtimed; `-tls-cert` with `-client-ca` requires its
      client certificate. Browser origins are refused there.
  - **Routes and the binding check.**
    - apid registers each realtime endpoint with `LiveRoutes`. Each route
      gives a key ID, its database and the database's hub, for every
      unretired key of a live database that the endpoint's app holds a
      ready or retiring binding to. An endpoint holds at most
      `LiveQueryRoutesPerEndpoint` (64) routes.
    - A live session reads its endpoint's routes at every subscribe and
      renewal. Re-registration therefore reaches open connections, and a
      seal whose key is not routed is refused in realtimed with
      `forbidden`.
    - A renewal must route to the subscription's own database.
    - Enabling, rotating or disabling re-registers the endpoints of the
      database's bound apps at once. Binding changes reach realtimed with
      the 30-second endpoint reconciliation.
    - realtimed dials hubs with `FAAS_REALTIME_LIVE_TLS_CA_FILE` and an
      optional client certificate (`_CERT_FILE`, `_KEY_FILE`). A hub whose
      URL changes is redialed and its subscriptions are resubscribed.
    - Rollout order: realtimed before apid. An older realtimed rejects a
      registration carrying `live_routes`, because the field is unknown to
      it.
- **Fair use (ADR-1323)** (2026-10-17).
  - **Covered by construction.** Live queries ride ordinary v2 connections,
    so the per-account connection allowance applies to them unchanged. Every
    live frame goes through `queueOutbound`, where the per-connection queue,
    and ADR-1323's node outbound budget, are enforced. A client too slow
    for its live frames is closed with 1013 and reconnects; a test covers
    this.
  - **Results sent whole are bounded.** A new subscriber, a reset or a
    resubscription receives the whole result. Above 2 MiB of rows it is
    refused with `live_query_limit` (limit and observed bytes), because
    the burst could otherwise overflow a slow client's 4 MiB queue. The
    client's reconnect would then meet the same result and loop.
    Incremental changes are already capped at one 256 KiB frame per round;
    anything larger is sent whole.
  - **Metrics:**
    - `realtimed_live_sessions`, `realtimed_live_subscriptions` and
      `realtimed_live_upstreams`;
    - `realtimed_live_upstream_drops_total`;
    - `realtimed_live_frames_total{direction}` and
      `realtimed_live_bytes_total{direction}`;
    - `realtimed_live_refusals_total{code}`.
  - **When ADR-1323's branch merges:** its usage recording counts delivered
    frames in `queueResume`, which live frames bypass. `queueLive` must also
    call `c.state.usage.delivered`. The connection allowance and the node
    budget need no change.
- **RLS mode, the engine** (2026-10-17).
  - **Sessions.** `Engine.RegisterAs` takes a `Session` (role and JWT
    claims). Subscriptions share a plan group only with an identical query
    and session; plans are compiled once per SQL.
  - **Running as the session.** A session group's statements run in a
    savepoint, under `set_config('role')` and `request.jwt.claims`, so
    row-level security filters every snapshot and maintenance read.
    PostgreSQL refusing them (a revoked grant, a failing policy) closes that
    group's subscriptions with `invalid_query`, not the round.
    `VerifyEachRound` checks session groups as their session.
  - **Policy dependencies.** Policies often read other tables (membership),
    and those changes alter what a role sees without changing any row the
    query reads. On a session group's creation the engine walks the
    policies on its tables:
    - `pg_depend` gives the tables and functions a policy references, and
      the walk follows those tables' own policies and views.
    - A SQL function with a string body records nothing, so the engine
      creates a `BEGIN ATOMIC` copy in a rolled-back transaction and reads
      what PostgreSQL records for it. This keeps `auth.uid()`-style
      functions free.
    - A function in another language is opaque.
    - A change in a dependency table re-reads the group. An opaque policy
      re-reads it on every round with changes.
    - Dependency tables are locked with the round's tables. Policy DDL is
      already a broad schema change, and dependencies are read again after
      it.
  - **Checkpoints** keep each group's session.
  - **Oracle.** RLS seeds add a `grants` table that every policy reads
    (directly, or through a plpgsql function on a third of seeds). Writers
    change it, and every query also runs as two subjects. Soak: 20 seeds,
    14,386 transactions, 20,554 checks, 0 divergences.
  - **Cost.** Each distinct claim set is maintained separately, and a
    dependency change re-reads the groups it affects. The ADR's per-database
    RLS subscription limit bounds both.
- **RLS mode, the PostgREST compiler** (2026-10-17).
  - **What it compiles.** `pkg/livequery/rest` compiles the reads that
    `@gregale/data` sends to the Data API, as `relation?query`, into SQL
    the engine maintains:
    - select with aliases, casts, JSON paths and foreign-key embedding:
      to-one as an object or null, to-many as an array, nested, with
      `!inner` and `!hint`;
    - filters on the queried relation (`eq` through `wfts`, `in`, `is`,
      `not.`, `any`/`all`, `or=`/`and=` trees);
    - `order`, `limit` and `offset`.
  - **Safety.** Identifiers are checked against the catalog and quoted,
    casts are resolved by `to_regtype`, and every filter value is a
    parameter, so requests differing only in values share one plan.
  - **Refused:** filters on embedded relations, many-to-many embedding,
    writes' parameters, and `limit` or `offset` without an `order` (a live
    result must be deterministic).
  - **Embeds as correlated subqueries.** Embeds compile to correlated JSON
    subqueries. The engine maintains those queries by recompute (a
    lineage-mode rewrite of to-one embeds as joins is a later
    optimisation). Filters, windows and plain projections stay incremental.
  - **Engine fix.** Array parameters, and array sort keys, were unnested as
    `text[]::T[][]`, which `unnest` flattens. They now travel as text and
    are cast where used. This also fixes sealed queries such as
    `id = ANY($1)`.
  - **Tests:**
    - every request above is maintained sealed and as a session through
      writes to the queried, embedded and policy tables, with
      `VerifyEachRound`;
    - an array-parameter regression test.
- **RLS mode, end to end** (2026-10-17).
  - **Frames.** `live.subscribe` carries `database`, `rest` (the Data API
    read), an optional `schema` and the user's `token`. `live.renew` carries
    a fresh `token`.
  - **realtimed** accepts these only on `oidc_jwt` endpoints. It verifies
    the token against the endpoint's issuer, JWKS and audience through
    `ClaimsAuthorizer`, and replaces it with its claims and expiry. A client
    may not send claims. Routing is by database through the endpoint's live
    routes, so the binding check applies to RLS mode too.
  - **The hub** believes claims only from a peer whose client certificate
    livequeryd verified (`-client-ca`), that is realtimed.
    - It reads as the app's Data API binding login, which realtimed names
      (see *Logins* below). The token's `role` claim does not choose it, as
      the Data API gateway also overwrites it (ADR-650).
    - It drops `exp`, `iat`, `nbf` and `jti` from the claims, so a refreshed
      token keeps its plan group.
    - It compiles the read against the database's catalog, in the `api`
      schema unless the read names another (the Data API default).
    - A subscription goes stale at the token's expiry until renewed. A
      renewal whose claims differ is a `renew_mismatch`.
    - RLS-mode subscriptions have their own limit,
      `LiveQueryRLSSubscriptionsPerDatabase` (2,000), and seal revocation
      does not touch them.
  - **SDK.** `connection.query({ database, token, ...liveRead(dataQuery) })`
    and the React hook `useLiveRead`. `liveRead` takes a query built with
    `createDataClient`, so the same builder serves a request and a live
    read.
  - **Tests:**
    - a full-path test with a browser connection, realtimed, the proxy, the
      hub and PostgreSQL row-level security: per-user rows, a membership
      change, renewal and mismatch, and claims refused from an untrusted
      peer;
    - realtime's token-to-claims swap;
    - SDK framing and `liveRead` over a real postgrest-js builder.
- **Logins** (2026-10-17). Every query from realtimed reads as one of the
  app's own binding logins, never livequeryd's:
  - apid adds `sealed_role` (the app's read-only binding, else read-write)
    and `data_api_role` (its Data API binding) to each live route
    (`AppLiveRoutes`). The proxy sets `role` on every subscribe and renew
    from the route; a client-sent `role` or `claims` is refused.
  - The hub requires `role` from realtimed and refuses it from anyone else.
    Direct development sessions still read as the engine login.
  - The engine reads each group as its login (`set_config('role')`, per
    group, in a savepoint). It refuses a login that is a superuser or has
    `BYPASSRLS`, or that the engine login may not `SET ROLE` to (`SET` from
    PostgreSQL 16).
  - Sealed queries are therefore bounded by the binding's grants and by row
    policies the customer writes for it, rather than by livequeryd's
    superuser login.
  - Binding changes reach realtimed through the 30-second endpoint
    reconciler. A dropped login closes its queries with `invalid_query`.
  - Replaces the `-rls-roles` and `-rls-anon-role` flags.

## Consequences

- **TRUNCATE waits.** TRUNCATE and table rewrites on live tables wait for the
  round in progress, which is bounded by round duration.
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
- ~~Whether compilation belongs in livequeryd or apid.~~ Resolved: livequeryd
  on the host. It keeps catalog access local to the database node, and keeps
  the parser's memory out of the VM (see Phase 1 progress, "Memory, and the
  compiler split").
- Whether live queries ride existing realtime endpoints (proposed) or get a
  dedicated endpoint kind.
- Seal format compatibility: whether to accept Neon-style sealed-query shapes
  to ease migration from Neon Realtime.
