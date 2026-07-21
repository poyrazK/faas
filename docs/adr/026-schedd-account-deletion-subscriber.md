# ADR-026 · schedd deletion-pending subscriber (G6 closure, second half)

- **Status:** accepted
- **Date:** 2026-07-21
- **Decision:** schedd consumes `NotifyAccountDeletionPending` from the
  existing `pg_notify` channel ADR-021 first published, and on every
  message transitions each live instance of the customer to a new
  `EVICTED_ACCOUNT_DELETING` state. Reaper sweeps continue to run
  (the status enum now includes the new value, not a string reason
  overlay — see D2 below). The pg_notify consumer is the same
  `db.Subscribe(...)` mechanism apid's DNS poller uses; on connection
  loss it reconnects with 1s linear backoff up to 30s and resumes.
- **Why:** ADR-021 shipped the producer side (apid's
  `scheduleDeletion` emits `NotifyAccountDeletionPending`) but the
  consumer side was deferred — the spec's "scheduled deletion" promise
  was half-honored: the DB row flips to `deleted_pending`, the customer
  gets the email, but **live instances keep running and keep billing**
  for up to 30 days. Customers clicking Delete expect the work to stop
  promptly; a grace window for the data is not the same as a grace
  window for compute. Schedd is the right owner here: it already
  transitions instances between `RUNNING / PARKED / STOPPED`, owns the
  reaper, and is the only writer to the instances table (spec §6.1).
- **Consequences:**
  - `pkg/state.Instance.State` adds `EVICTED_ACCOUNT_DELETING` to its
    CHECK-constrained string set (new migration `00012`).
  - A long-lived subscriber goroutine starts in `cmd/schedd/run` next
    to the existing `loop.Run(ctx)` block. Reuses the production
    pgxpool via `db.Subscribe(ctx, pool, []string{db.NotifyAccountDeletionPending})`.
  - On message receipt the subscriber walks
    `store.ListInstancesForAccount(ctx, accountID)` and issues one
    `Stop(ctx, instanceID, reason=EVICTED_ACCOUNT_DELETING)` per row.
    Stop semantics already exist; this is a thin caller.
  - meterd's quota tick sees the instance counts drop within seconds,
    not 30 days, so the customer's bill also stops early. The grace
    data row in `accounts` remains — the 30-day clock still runs for
    the hard delete path.
  - If schedd is down at the moment a customer hits DELETE, the
    message sits in the pg_notify channel until schedd reconnects.
    pg_notify is a transient channel (queues inside PG), so a single
    crash+reboot doesn't drop the message; we re-read unconsumed
    notifications on subscribe by storing the last-seen `id` in a
    scratch row (`account_deletion_consumer_status`). Failed message
    parse logs and continues — a malformed payload MUST NOT block
    other accounts.

## Decisions

- **D1 Subscriber lifetime** — long-lived goroutine under the same
  ctx as `loop.Run`. NOT a separate daemon. Adding a daemon for one
  pg_notify channel is the wrong abstraction; schedd already owns
  every pg_notify consumer in this codebase (it's the only writer to
  instances, so it's the only consumer that needs to react to
  customer-state changes).
- **D2 Status enum vs. reason string** — reuse the existing
  `InstanceState` enum and add a new value rather than overlaying a
  string `reason` column. Two reasons: (a) every dashboard / log /
  metrics surface that renders instance state can keep treating
  state as a small enum; (b) the SHUTDOWN → EVICTED transition is
  visible at a glance in the §12 dashboard, which an opaque reason
  string wouldn't be.
- **D3 Backoff** — linear 1s→30s with jitter. The pg_notify channel
  reconnects transparently in production; a flaky connection during
  apid restarts should resume within seconds, not minutes.
- **D4 Idempotency** — a redelivered message on the same pending
  account is safe: the subscriber iterates instances and transitions
  them, and a second pass finds zero. ListInstancesForAccount
  already filters live rows so an already-evicted instance is never
  re-touched.
- **D5 Failure modes** — connection loss / pg_notify backlog full /
  message parse error all share the same handling: log Warn, skip
  the message, continue. The audit trail (gdpr_requests ledger) is
  the source of truth for "did this account get deleted" — pg_notify
  is best-effort delivery. The grace timer in apid still reaps on
  schedule even if schedd never receives the message.
- **D6 DeleteAccount-after-restore race** — the subscriber only acts
  on `pending`. If a customer restores mid-window the account
  transitions to `active`; any in-flight message is discarded by
  the conditional `WHERE status='deleted_pending'` guard when schedd
  finally re-reads. Same single-flight pattern pkg/grace uses.

## Trade-offs

- **Pause-billing vs. evict-billing** — we could have apid ask meterd
  to "stop billing on pending" while leaving instances running.
  Rejected. Customers expect Delete to stop work, not to continue
  charging. The customer-facing email explicitly says "your apps will
  continue to be available until <ts>" — that's a softer promise than
  the spec implicitly makes, and schedd eviction aligns the two.
- **Separate daemon vs. schedd goroutine** — a dedicated
  `deletiond` is more orthogonal but doubles the
  dial-vmm/seed-ledger/grpc-server surface. Spec §6 deliberately keeps
  schedd the sole instance-table writer; a second daemon that
  touches instances breaks that.
- **Polling** — alternative is schedd polling for
  `status='deleted_pending'` instances every 5 s. Rejected:
  pg_notify is the existing producer, polling duplicates it.

## Files

### New

| Path | Purpose |
|---|---|
| `migrations/00012_instance_evicting_state.sql` | Adds `EVICTED_ACCOUNT_DELETING` to the instances.state CHECK constraint |
| `pkg/sched/deletion_subscriber.go` | Subscriber loop + handler; reads `db.Subscribe` feed, walks instances, calls `sched.Stop` |
| `pkg/sched/deletion_subscriber_test.go` | MemStore-backed unit tests with a fake notify channel |

### Modified

| Path | Change |
|---|---|
| `cmd/schedd/main.go` | Start the subscriber goroutine under `deps.bgBefore` (already used by apid for DNS poller). Reuses `srv.engine` and `srv.store`. |
| `pkg/state/types.go` | New `InstanceEvictingAccountDeleting InstanceState = "evicting_account_deleting"` constant |

## Future work

- **Vary the eviction reason** — once Dunning achieves a
  "machine-suspended" state, schedd will want to evict those too;
  the reason string is the natural place to differentiate. Defer
  until the next dunning reason lands.
- **Subscriber tests against a real PG** — the current test uses a
  fake `Notifier` channel because pg_notify needs a live cluster. A
  follow-up test with `pgtest.Open(t)` will exercise the production
  reconnect path.

## Rejected alternatives

- **On-each-instance poll from schedd** — duplicate producer
  pathway; pg_notify is already there.
- **apid directly calls vmmd.Stop** — breaks the §6.1 ownership rule
  (apid never touches vmmd). Schedd owns the dial-up and the state
  machine; a one-line pg_notify subscription is the correct seam.
- **Hard-stop on DELETE with no grace** — defeats ADR-021's "the
  customer can come back within 30 days" promise.

## Acceptance

- `make test` green including `pkg/sched/deletion_subscriber_test.go`.
- A new dashboard row: "Instances evicted by account deletion in the
  last 24 h" (a small counter on §12's schedd metrics surface).
- Manual drill: seed an account + 1 running instance → DELETE
  /v1/account → within one `loop.Run` tick, the instance row's state
  is `evicting_account_deleting` and vmmd reports it as stopped.
