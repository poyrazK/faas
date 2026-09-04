# ADR-124 · Fleet migration race — session-scoped pg_advisory_lock

> **ADR-142 amendment (2026-09-04):** the advisory-lock decision remains in
> force. Readiness now compares exact migration IDs rather than maxima, and
> the ledger trigger notifies on every applied insert to support out-of-order
> timestamp migrations.

- **Status:** accepted (2026-08-21)
- **Issue:** multi-host safety cluster PR-1 / audit F2
- **Decision:** Wrap `db.MigrateUp` in a session-scoped `pg_advisory_lock` on a constant bigint key, so concurrent daemons in a fleet serialise before any DDL runs. Companion `cmd/migrate -leader` mode (PR-2) is the preferred prod boot order; the advisory lock is the safety net for parallel boot.

## Context

`goose.UpContext` is invoked at the top of every daemon's `main()`:

```
cmd/builderd/main.go:75      db.MigrateUp
cmd/meterd/main.go:565       db.MigrateUp
cmd/schedd/main.go:159       db.MigrateUp
cmd/apid/main.go:540         db.MigrateUp (direct)
cmd/schema-dump/main.go:124  db.MigrateUp (direct; -status path skips the lock)
cmd/imaged/main.go:88        db.MigrateUp
cmd/migrate/main.go:64       db.MigrateUp (standalone CLI)
```

Goose maintains its own connection state through the `database/sql` interface; it does not consult Postgres for "is anyone else migrating?" — its version ledger is the `goose_db_version` table, which only catches a conflict when two writers both attempt to `INSERT` the same `version_id` row. On a fresh database where the ledger is empty, both daemons see "no row at v=N", both run the migration to v=N+1, and the second one crashes:

```
ERROR: duplicate key value violates unique constraint "goose_db_version_pkey" (SQLSTATE 23505)
```

On a single-host install this never fires because there's only one daemon process. On a multi-host fleet where every daemon boots in parallel — the operator runs `ansible` across all boxes in one shot, or a kubernetes rolling deploy scales each box simultaneously — at least N-1 daemons panic with the same 23505. Goose's idempotency is at the per-version level (it skips already-applied versions) but does NOT serialise across processes.

The one existing advisory-lock precedent in this codebase (`pkg/state/pgstore_ratelimit.go:35` on the rate-limit counter) is xact-scoped — a single SQL statement holds `pg_advisory_xact_lock(hashtext(...))` for the duration of one UPDATE. Schema migration is many statements; an xact-scoped lock would release at the first COMMIT, letting the second daemon in.

## Decision

Three components:

1. **`pkg/db/migrate_advisory.go`** (new) — `AcquireMigrationLock(ctx, pool) (release func() error, err error)` pins a connection from `pool.Acquire()` and runs `SELECT pg_advisory_lock($1)` with `MigrationLockKey int64 = 0xfa4_5a5d_0e1a_0001`. The returned `release` closure runs `SELECT pg_advisory_unlock($1)` on the same pinned connection, then releases it back to the pool. Uses an internal timeout context for the unlock so the release still works after the caller's ctx is cancelled (daemon shutdown path).

2. **`pkg/db/migrate.go`** — `MigrateUp` acquires the lock before opening the goose shim and defers release. The lock sits on a pgxpool connection; goose uses a separate `*sql.DB` derived from the same ConnConfig; the two are intentionally on different connections so the lock holder's pinned conn isn't returned to the pool while goose is mid-run.

3. **`cmd/migrate -leader`** (PR-2) — preferred prod boot order. The leader runs migrations; every other daemon passes `-wait-for-migrations` which blocks on `pg_notify('migrations_applied')` (a new trigger on `goose_db_version` INSERT) before opening its own connection. The advisory lock is the safety net for parallel boot when the operator launches all daemons in one shot without the leader ordering.

### Why session-scoped, not xact-scoped

Goose runs multiple internal transactions during UpContext. An xact-scoped `pg_advisory_xact_lock` would release at the first COMMIT, letting a second daemon enter and race the next migration's version INSERT. Session-scoped `pg_advisory_lock` holds across all statements in the session and releases on either explicit unlock or connection close. The migration is many statements; the lock must span them.

### Why a constant key, not hashtext(svcName)

A global lock on "any schema-migrating connection in this database". Hashing a service name would create one lock per service and reintroduce the very race we're closing — N daemons hashing to N distinct keys → N concurrent migrations. A single bigint key means at most one daemon in the world can be applying migrations at any instant.

### Why not run goose in a single SQL transaction

Goose already manages its own DDL transactions per migration; wrapping the entire UpContext in one big transaction would either conflict with goose's internal tx boundaries or require rewriting goose. Not worth it.

### Why not use the existing `compute_node_changed` pg_notify as a barrier

The notify fires on `compute_nodes` mutations; there's no equivalent for `goose_db_version` (which doesn't have a notify trigger today). PR-2 adds one (`migration_notify_trg`); the leader's final INSERT into `goose_db_version` at v=MaxEmbedded notifies, and waiters unblock.

## Consequences

- **Single critical section per database:** at most one Gregale daemon in the entire fleet can be inside `goose.UpContext` at any instant. The lock holder's pinned pool connection holds the lock for the duration of the migration. A second daemon blocks at `AcquireMigrationLock` until the first releases.

- **Boot order remains the operator's call:** the lock serialises, not orders. Without `cmd/migrate -leader`, daemons boot in arbitrary order; whichever wins the lock runs first. PR-2 adds the leader pattern so the prod boot order is deterministic; the lock is the safety net for the "I forgot to run migrate -leader first" case.

- **Schema-dump fast-path stays open:** `cmd/schema-dump -status` reads via `db.Status` (which does NOT acquire the lock). This is intentional: a CI step that just wants to print the current migration version must not block on a long migration in flight. Documented inline.

- **Per-instance ledger still required:** the lock prevents concurrent migration but does not address the partial-migration case where a daemon is killed mid-run. The goose version ledger (`goose_db_version`) remains the source of truth for "what was applied"; on next boot, goose picks up at the last committed version. This was already true before ADR-124 and is unchanged.

- **Failure modes:** if `pg_advisory_lock` itself fails (network partition, Postgres restart), `AcquireMigrationLock` returns the wrapped error and `MigrateUp` exits. The daemon does not boot. Operators see the same red they would have seen before ADR-124, but for a smaller failure surface.

## Rejected alternatives

- **Per-service hash lock.** N locks, N races. Rejected: the problem is exactly that N daemons all need exclusive access to the schema, not exclusive access to a service name.
- **`SELECT … FOR UPDATE` on `goose_db_version`.** Goose doesn't take a row lock during INSERT; retrofitting one requires changes in the goose library. The advisory lock is external to goose and doesn't touch its internals.
- **Run migrations out-of-band in CI / a separate migrator container.** Pushes the race to a different system but doesn't eliminate it. The CI migrator must also be serialised; otherwise a CI step that runs the same migration on a fresh DB races itself. Same problem, different process.
- **Quorum/leader election via `compute_nodes`.** The existing ADR-029 admin surface manages `compute_nodes`; adding a "is this node the migration leader?" column would couple migration to fleet membership. ADR-124 deliberately keeps migration ownership orthogonal to compute-node ownership.

## Follow-on

- PR-2 — `cmd/migrate -leader` mode + `WaitForMigrationsApplied` + `migrations_applied` notify trigger.
- This ADR is the foundation; subsequent cluster PRs (PR-3, PR-4, PR-5) assume PR-1 has landed because their migrations run inside the lock.

## Amendment (2026-08-21, PR-2)

The prod boot order is `cmd/migrate -leader` on box A first, then every other daemon with `-wait-for-migrations` before its own `db.MigrateUp`. The advisory lock is the **safety net** for the case where the operator launches all daemons in parallel without the leader ordering (e.g. an `ansible` one-shot across the fleet, a kubernetes rolling deploy, a CI step that runs `migrate -default` and then boots a daemon before the leader finishes). With the leader pattern the lock is rarely contended in steady-state; without it the lock is the load-bearing serialisation. Both layers ship.

Three concrete pieces of the leader pattern (PR-2):

1. `cmd/migrate -leader` is identical to the default mode except it logs `mode=leader` so operators and monitoring can attribute the boot ordering. The behaviour — `MigrateUp` then exit — is unchanged.

2. `cmd/migrate -wait-for-migrations` does NOT call `MigrateUp`. It blocks on `pg_notify('migrations_applied')` (new channel constant `db.NotifyMigrationsApplied`) and a 5-second safety-net poll on the ledger. Subscribes BEFORE the first poll so a leader commit between the initial SELECT and the LISTEN cannot be lost. Returns when `MAX(version_id) >= MaxEmbedded`. The non-leader daemon's own `MigrateUp` runs after this helper returns.

3. Migration `00347_migration_notify.sql` adds `migration_notify_trg` on `goose_db_version` INSERT, which fires `pg_notify('migrations_applied', NEW.version_id::text)` ONLY when `NEW.version_id = (SELECT MAX(version_id) FROM goose_db_version)` AND `NEW.is_applied = true`. The waiter treats the payload as informational and re-reads the ledger directly, so spurious early fires are harmless.

The leader pattern does NOT replace the advisory lock; both run in production. The lock prevents concurrent migration; the leader prevents out-of-order migration. Removing either layer reintroduces a fleet-bootstrap race the other layer cannot catch (concurrent without lock = 23505; out-of-order without leader = box B sees its own schema-dump output but not box A's fresh columns when B races A's last-migration-in-flight).

Reservation fences `00348`-`00350` are absorbed by subsequent cluster PRs (PR-3 fleet signing key, PR-5 wakeCoord). No code path outside the migration-advisory primitives (lock + trigger + leader/waiter) changes in PR-2.

The call-site audit in `pkg/db/migrate.go` enumerates the seven `MigrateUp` callers and the one deliberate bypass (`cmd/migrate -status` calls `db.Status` instead). Any future caller MUST route through `MigrateUp`; the lock is the cluster-wide invariant.
