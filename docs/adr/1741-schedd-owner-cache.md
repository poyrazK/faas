# ADR-1741: schedd owner cache — per-app runtime generations and one guarded write per wake

- Status: Proposed. Built behind schedd's `owner_cache` switch, off by
  default.
- Date: 2026-10-16
- Builds on: ADR-1740 (frozen fast path), ADR-1151 (pause tiers), the
  scaling roadmap's wake rule (zero control-plane round trips per request,
  at most one per wake)
- Amends: ADR-1740 (its column guards and runtime-values read give way to the
  generation check), the roadmap's "one guarded write" for boots (section 5)
- Related: ADR-1344 (availability; its node-lease epochs join the guard when
  they land)

## Context

The wake path re-reads, on every wake, rows that almost never change between
two wakes of the same app. Measured on real Postgres
(`pkg/sched/wake_statements_pgtest_test.go`):

| wake | statements before the answer | sequential round trips |
|---|---|---|
| cold boot | 54 | 48 |
| thaw, ADR-1151 path | 24 | about 19 |
| thaw, ADR-1740 ticket | 5 | 3 |

A cold boot's 54 statements are 26 reads before the boot (workload
retirement gate, app, production release, deployment, standards enrollment,
account, cutover barrier, scaling state, runtime values, snapshot choice,
node usage), a 7-statement admission transaction, 17 statements of standards
capture and scaling-state bookkeeping, and a 9-statement publication.

A cache could serve those reads, but nothing tells schedd reliably when
they change. An inventory of the schema (2026-10-16) found:

- no notification at all for app status, size or type, the database cutover
  barrier, account abuse holds and `past_due`, deployment status and
  `parked_reason` (about 113 write statements against `deployments`), runtime
  configuration (environment values, secrets, sidecar layers, the config
  stamps), application-standards enrollment and scaling state;
- notifications sent from Go after the write commits, in a separate
  statement, often with the error ignored: `app_changed`,
  `deployment_changed`, account lifecycle;
- complete in-transaction notifications only for production release sets,
  workload heads and compute nodes;
- a change ledger (`control_plane_change_log`) that covers apps, deployment
  traffic and release sets, not the rest.

The runtime-config stamps are not complete either: apid writes a value and
stamps the change in a separate call (ADR-1740 section 4). A cache refreshed
by these signals would serve stale rows, and a cache refreshed by a time
limit would serve them for the length of the limit.

## Decision (proposed)

### 1. A runtime generation per app, bumped in the writer's transaction

A table `app_runtime_generations(app_id, generation)` holds one counter per
app. Triggers on every table a wake depends on bump the app's counter in
the same transaction as the change and send `pg_notify('app_runtime_changed',
'{"app_id":…,"generation":…}')`, which Postgres delivers at commit. Because
the triggers fire on the table write, no code path can change a row a wake
depends on without bumping the counter: apid, schedd, imaged, meterd,
GitOps and break-glass SQL alike.

The triggers cover only changes that alter a wake's decision:

| table | columns or events |
|---|---|
| `apps` | status, type, ram_mb, cpu_millicores, max_concurrency, idle timeout, manifest, org_id, project_id, managed_postgres_admission_cutover_id (not `last_scale_*` or activity stamps) |
| `accounts` | status, plan, abuse_hold_at; bumps every app of the account |
| `deployments` | insert, delete; status, traffic_percent, scope, parked_reason and the pinned settings (not snapshot-miss or scale stamps) |
| `project_release_sets`, `project_release_members` | production sets: activation, members |
| `app_envs`, `app_secrets`, `deployment_sidecar_layers` | any change |
| `project_environment_workload_specs`, `project_environment_workload_deployment_specs`, `project_environment_workload_heads` | any change |
| `app_runtime_config_changes`, `app_runtime_config_scope_changes` | any change |
| `app_application_standards` | enrollment state, revisions, effective hash, settings, exception expiry (not lease or checkpoint columns) |
| `deployment_runtime_environment_owners` | any change |
| `environment_workload_retirements` | any change |
| `project_environments` | slug or project changed, environment removed; bumps every app of the project |

`compute_nodes` keeps its own channel, `compute_node_changed`, which already
fires in the writer's transaction for every column admission uses.

### 2. The owner cache

schedd keeps, for each app it serves, one entry: the generation, the app, the
account and its plan limits, the production selection, the app resolved for
that deployment, its scaling owner and environment, the runtime values and
config fence, the standards verdict, the retirement verdict and the cutover
barrier, the deployment's usable snapshot. A miss loads the entry in one
pipelined round trip; nodes are cached separately.

- `app_runtime_changed` drops the app's entry; the next wake reloads it.
- `compute_node_changed` refreshes the node.
- A resync from `db.SubscribeWithResync` (reconnect or dropped delivery)
  drops every entry.

The notification only saves a failed write: section 3 makes a stale entry
harmless.

### 3. Every write a wake makes checks the generation

The writes a wake makes carry the entry's generation and recheck it inside
their own statement, with the counter row locked `FOR SHARE`:

- the instance admission insert (cold boot, restore);
- the RUNNING publication;
- `ThawFrozenInstance` and the warm-pool promotion.

A writer that bumps the counter waits for the statement, or the statement
waits for the writer and then sees the new counter. Either way, a statement
never commits a decision made from an older generation. A mismatch refuses
with `ErrConflict`, changes nothing, drops the entry and retries once from
fresh reads. Reads checked against the entry before the insert can find the
staleness first (the runtime values' owner check compares the cached app),
so an admission that used the cache retries on any `ErrConflict`, not only
on the generation's. An app taken from the cache is never handed to a path
that does not carry the guard: wake phase 1 passes on only an app it read
from the database.

ADR-1151's thaw and the warm-pool promotion publish without an insert, so
when admission took the entry they reread the generation beside their own
reads, before vmmd resumes the VM, and refuse and retry the same way. Their
decision is then as fresh as the uncached path's, which also reads before it
resumes.

The workload-retirement gate is held back for a production wake that an
entry or a thaw ticket can serve. It runs as soon as the wake decides from
fresh reads: in wake phase 1 when no entry serves it, or before a retry. A
wake answered by a running instance writes nothing; it trusts the entry
until the notification drops it.

The guard replaces ADR-1740's column guards and its runtime-values read: a
config edit bumps the counter in the edit's own transaction, so the counter
answers what the stamps could not.

Lock order: a writer takes its rows first and bumps the counter in an
`AFTER` trigger, so it holds the app's row locks before the counter. A
guarded statement must take the counter last, after any app or deployment
row it locks, or lock nothing but the counter, as `ThawFrozenInstance` does.
Taking the counter first could deadlock against a writer that holds the app
row and waits for the counter.

### 4. Node leases

When ADR-1344's node leases land, the same statements also check the node's
lease epoch. The guard has one place to add it.

### 5. Writes per wake

| wake | control-plane round trips |
|---|---|
| running instance | 0 (from the cache and the ledger) |
| thaw, warm-pool resume | 1 |
| cold boot, snapshot restore | 2: the admission insert and the publication |

A boot needs its instance row before vmmd starts the VM, so the watchdog and
crash recovery see it; the publication after the boot is the second write.
Its bookkeeping (standards capture, event, notification) rides in those two
statements. The scale-out stamp does not: it runs while vmmd boots, off
the critical path (see Rollout).

## Alternatives considered

- Invalidate on the existing notifications only. Rejected: the inventory
  shows most of a wake's inputs change silently.
- A time-limited cache. Rejected: stale for the length of the limit, and
  every write would still need its own guard.
- Replay the change ledger, as gatewayd-internal does. Rejected as the
  primary mechanism: the ledger covers apps, traffic and releases only.
- Compare row versions (`xmin`) of each row at write time. Rejected: rows
  change for bookkeeping (scale stamps, enrollment leases), so writes would
  fail for no reason, and each guard would list many rows.

## Consequences

- One migration adds the counter table and about a dozen triggers on tables
  apid, imaged and GitOps write. Their writes gain one upsert and one
  notification each; writers to one app serialize on its counter row for the
  rest of their transaction.
- A conformance test runs every store method that writes a covered table
  and fails if the counter did not move, so a new writer or a new column
  cannot silently bypass the cache.
- A wake's latency stops depending on the control plane's read load.
- Turning `owner_cache` off returns schedd to reading every row. The triggers
  stay; they cost nothing a reader depends on.

## Rollout

Steps 1 to 4 and step 5's admission guard landed first: a ticketed thaw
runs one statement, and the entry also keeps the runtime values' three
reads, exists only while the cutover barrier is clear at its generation,
and carries the workload-retirement gate's verdict, as does a thaw ticket.
The gate is deferred only for a production wake with no explicit
deployment, scope or bypass; when the cache misses, it runs in wake phase 1
before any write.

Step 5 (2026-10-18) measures what a boot waits for, not every statement it
runs: `TestPostgresBootStatements` gives vmmd 150 ms, as a real restore
takes hundreds, and counts the round trips before and after it. Statements
that run while vmmd boots are off the critical path. That corrected one
part of this ADR: the scale-out stamp, a 17-statement transaction, already
runs during the boot and is joined only before the publication, so folding
it into the publication would lengthen the wake. It stays where it is.

Step 5 then did three things:

- A boot admitted from the cache publishes on its admission's generation:
  the publication takes the generation's share lock after its own row
  locks instead of the fence's three locks, values read and recomputed
  fingerprint, and the managed Postgres barrier (an apps column) is not
  read again. When the generation moved during the boot, nothing is
  written and the publication goes through the barrier and the fence as
  before, so a VM is never discarded for the coarser check.
  `schedd_owner_cache_total{outcome="guarded_publish"|"guarded_publish_moved"}`.
- The admission insert binds ADR-1523's snapshot-recovery lineage in its
  own transaction, one `INSERT ... SELECT` that share-locks the deployment
  and app, instead of a ten-statement transaction after it. A binding it
  cannot make refuses the admission, so no instance exists unbound.
- Independent reads run concurrently: the snapshot gate and choice, the
  receipt checks (which reuse the wake's deployment), the node memory and
  CPU aggregates, the standards capture and enrollment; the boot reuses the
  receipt the choice read.

Round trips on the critical path, on Postgres. The "after" column places
from schedd's node registry, as `cmd/schedd` does; the registry alone saves
one round trip of the difference.

| wake | before step 5 | after step 5 |
|---|---|---|
| restore, owner cache warm | 52 (42 before vmmd, 10 after) | 31 (25 before, 6 after) |
| cold boot, owner cache warm | 44 | 28 |
| restore, cache off | 62 | 44 |
| thaw, frozen fast path | 1 | 1 |

Step 5, second round (2026-10-18), after the owner asked to cut the
round trips between compute nodes and the control plane further. A
compute node's schedd talks to the control plane's Postgres for every
statement, so each round trip crosses the network. The test now runs as a
compute schedd does: it owns its node, places from the node registry and
vmmd's live capacity reports, and leaves the scale-out stamp's parallel
transaction out of the chain.

- **Admission is one statement.** Migration 20261018153840637 adds
  `admission_reserve_node` (the capacity policy lock, the node check at the
  placement's lifecycle generation, the per-node advisory lock, then the
  headroom read as its own statement, so it still follows the lock) and
  `admit_wake_instance` (those checks, the insert, the ADR-1523 binding and
  the generation check, taken last). Refusals raise with constraint names
  mapped to the transaction's errors. The transaction path (worker
  reservations, job instances) calls `admission_reserve_node` for its node
  checks. A half-and-half race through both paths still fills a node
  exactly to its ceiling.
- **The publication is one statement.** `publish_instance_on_generation`
  does the compare-and-swap, the config proof with the wake's event and
  notification, the runtime-config receipt and the generation check.
  Upgrade acceptances, standards receipts and warm or frozen thaws keep the
  transaction.
- **The restore-pressure lease is one round trip.**
  `snapshot_restore_pressure_acquire` takes the fleet-wide session lock and
  then counts in its own statement; the lease insert and the unlock finish
  in the background, in that order, so the next holder still counts it. The
  release deletes in the background; the TTL is authoritative.
- **The rest overlaps.** The boot's reads of the app and deployment alone
  (private network, runtime base, rehearsal network) and placement's
  snapshot (nodes, usage, owner check, which uses the wake's app) load while
  the snapshot reads run; each candidate's locality is read while it is
  checked; the standards check hands its capture to the boot.

Every function is used only after a once-per-process probe finds it, so a
schedd that starts before its release's migrations admits as before.

| wake, owner cache warm | before step 5 | after the first round | now |
|---|---|---|---|
| snapshot restore | 52 | 31 | 8 (7 before vmmd, 1 after) |
| cold boot | 44 | 28 | 7 (6 before, 1 after) |
| thaw, frozen fast path | 1 | 1 | 1 |

The earlier columns predate the test's live capacity reports (which take
two usage reads off the path) and node ownership (which adds one app read),
so they are not exact baselines for the last column.

What a warm-cache restore still waits for: wake phase 1's
running-instance lookup, the snapshot reads with placement, the receipt
checks with locality, the lease lock and counts, the admission, the
standards check, and the publication after vmmd. Taking the lookup off the
path would mean speculative reads on every wake a running instance
answers; folding the standards check into the admission would tie the
function to the standards capture's format; taking the lease lock earlier
would hold the fleet-wide restore lock longer during bursts. With the
cache cold (the first wake after a change), a restore is 27 round trips.

1. This ADR.
2. Migration: the counter table, triggers and notification, the store
   method that reads a generation, and the writer conformance test.
3. The cache in schedd: load, invalidation, resync, metrics
   (`schedd_owner_cache_total{outcome}`), behind `owner_cache`.
4. Thaw and warm promotion use the generation guard, replacing ADR-1740's
   column guards.
5. Cold boot and restore: the admission insert and the publication carry the
   guard; their bookkeeping folds in.
6. Canary on one node; compare `wake.thaw_breakdown`, boot latency and
   conflict rates with the uncached path.
