# ADR-066 — Tier A5: cross-node live-instance migration

Status: **Accepted** (revised 2026-08-07)

- **Superseded (in part, PR-E):** prose referred to the monolithic
  `cmd/gatewayd/` daemon split by ADR-070 into `gatewayd-public` (TLS-only
  edge) and `gatewayd-internal` (routing + wake + proxy). Body is preserved
  verbatim; readers should substitute "gatewayd-internal" for the
  routing/wake/proxy path and "gatewayd-public" for the certmagic/TLS path.
  `cmd/gatewayd/<file>.go` citations in this body are stale; see PR-E for
  the new file locations.

Date: 2026-08-02

Deciders: platform team

## Context

PR #509 (Phase 2 / Gate A) shipped per-node schedd + per-node
placement claim — the per-box schedd writes its
`compute_node_target_url`, gates wakes on
`apps.node_id == s.OwnerNodeID`, and the gateway caches
per-node schedd clients. PR #509's slice 3 (Tier A4, ADR-064)
shipped the parked-app rebalance: a `compute_node_changed
active=false` event triggers every active peer schedd to
atomically re-stamp the dead node's parked apps to itself,
gated by admission + cooldown + per-tick cap.

Tier A4 explicitly deferred cross-node **live-instance**
migration (ADR-064 §"Deferred (explicitly out of scope)"):
"a running / waking / cold_booting / snapshotting instance
on the dying node stays with the dying node". The active-
passive HA path (operator drains a node, waits for in-flight
work to complete, then flips `active=false`) is documented in
`docs/runbooks/gate-a.md` and is the realistic ops use-case —
the Tier A4 implementation matches it.

Tier A5 closes the gap for the "operator-driven active-active
failover" case where the dying node is hard-killed (process
crash, OOM, NIC failure) and the customer-visible workloads
on that node must be served by an active peer without the
operator waiting for the in-flight VMs to drain.

## Decision

Ship the four-phase cross-node live-instance handoff.

### Phase model

```
dying vmmd                                  new owner vmmd
───────────                                  ──────────────
Phase 1  PrepareLiveMigration
         (Park → snapshot → mint lease)
                            ─────────────►
                                       Phase 3  AdoptMigratedInstance
                                                  (validate lease → Restore → TriggerResumeHook)
                            ◄─────────────
Phase 2  MarkInstanceMigrating
         (state running → migrating)
                                       Phase 3.5  MigrateInstanceOwner
                                                  (tx: node_id flip, lineage stamp, apps.migrated_at bump)
Phase 5  AcknowledgeMigration
         (lease cleared, no-op since VM is gone)
```

Phase 4 (rollback, `CancelLiveMigration`) runs on any failure
between Phase 1 and Phase 3.5: the lease is invalidated,
`Store.CancelInstanceMigration` restores the instance row to
`state='running'`, and the dying vmmd's tracker entry is
deleted.

### Architectural decisions

1. **Trigger.** A second consumer of the
   `db.NotifyComputeNodesChanged` channel (post-00276; was
   `db.NotifyComputeNodeChanged` before the split) —
   `LiveMigrator`, parallel to the Tier A4 `Rebalancer` watcher.
   Same payload filter (`active=false` + valid `node_id`);
   different per-instance dispatch (the four-phase handoff).

2. **Per-tick cap.** `MigrateLiveMaxPerTick = 10` (env-
   overridable via `FAAS_MIGRATE_LIVE_MAX_PER_TICK`). Lower
   than the parked-app rebalance cap (50) because each
   live migration spins up a fresh firecracker VM on the
   new owner. Capping the per-drain-event batch prevents a
   single bad node from monopolising the schedd worker
   pool.

3. **Lease window.** `MigrateLiveLeaseSeconds = 90`
   (env-overridable). The dying vmmd mints the lease at
   Phase 1; the lease clock bounds the whole flow. On
   expiry, the dying vmmd's tracker drops the entry (the
   canonical snapshot stays in storage until the per-vmmd
   snapshot-drift sweep reaps it). A lease-expiry mid-
   handoff surfaces as a `peer_failure` metric outcome;
   the orchestrator's Phase 4 is idempotent on the dying
   vmmd.

4. **Atomicity.** Two conditional UPDATEs in series:
   - **Phase 2 (MarkInstanceMigrating):** `WHERE id = $1
     AND node_id = $2 AND state = 'running'` →
     `state='migrating'`. A peer that already won flips
     the row away first; the loser sees `ErrConflict`.
   - **Phase 3.5 (MigrateInstanceOwner):** `WHERE id =
     $1 AND node_id = $2 AND state = 'migrating'` →
     `node_id = newOwner, state = 'running',
     migrated_from_node_id = dyingNode, migrated_at =
     now(), lease_token = $4`. Carries the lease_token
     as part of the predicate so a stale lease cannot
     silently commit.

   Both UPDATEs live in the same `Store` interface and
   each returns `state.ErrConflict` on `RowsAffected()==0`.
   The orchestrator's harness logs Warn + bumps
   `schedd_live_migration_decisions_total{outcome=
   "conflict"}` on each.

5. **Storage backend.** Tier A5 only works on fleets that
   have already shipped `OCIRegistryStorageBackend`
   (ADR-054 / PR #457). `LocalStorageBackend` cannot
   share snapshots across nodes; on a default-local-only
   fleet the live migration path will fail at Phase 3
   (Restore can't resolve the storage keys). Documented
   as a hard prerequisite.

6. **VM lifecycle.** The dying vmmd does NOT keep the VM
   paused during the migration window — `pkg/fcvm.Park`
   destroys the VM as part of its contract (see
   `pkg/fcvm/manager.go:1207`: "Park snapshots a running
   instance then destroys it, freeing all resident
   RAM"). Tier A5 is therefore a "live migration" in the
   sense that the instance survives (state preserved on
   disk) but the VM process itself is destroyed and
   recreated on the new owner — there is no
   pause-and-keep-running model because Firecracker
   does not expose VM-level live migration primitives.
   The customer sees a one-shot blip of ~350 ms (cold-
   boot from snapshot) instead of the seconds-to-minutes
   wait of an active-passive drain.

7. **State machine.** New `StateMigrating = "migrating"`
   (transient instance state, mirrors
   `StateEvictingAccountDeleting`'s shape). Edges:
   `RUNNING → MIGRATING` and
   `MIGRATING → {RUNNING, PARKED, FAILED}`.
   `CountsForRAM` includes `MIGRATING` so the per-node
   admission ceiling is honest across the migration
   window.

8. **Lineage.** Two new columns on `instances`:
   `migrated_from_node_id` (nullable, FK to
   `compute_nodes` with `ON DELETE SET NULL`) and
   `migrated_at` (nullable, clock-skew CHECK). The
   migration lineage lets the future per-node "this
   instance was migrated from X" telemetry surface on
   the §12 dashboard without a join to a separate audit
   table.

9. **App-level telemetry.** `apps.migrated_at` is a
   single nullable column on `apps` (no partial index —
   it's telemetry only, not on a hot read path). The
   per-app rate of migration is surfaced via
   `schedd_app_migrations_total{app_slug}` (a future
   PR — Tier A5 only ships the column).

10. **Metric.** `schedd_live_migration_decisions_total{
    outcome="migrated|peer_failure|conflict|lease_expired"}`.
    Pre-instantiated at boot for all 4 outcomes so the
    dashboard surfaces zero from boot, not "no data".

11. **Wire.** Four new RPCs on `vmmdpb.Vmmd`:
    `PrepareLiveMigration`, `AdoptMigratedInstance`,
    `AcknowledgeMigration`, `CancelLiveMigration`. ADR-
    016 holds (additive wire change, versioned proto).
    All four are schedd→vmmd only; the gateway never
    drives the migration RPCs.

12. **Per-instance lineage vs. one-shot cold-boot.** A
    migration's "new owner" can be the same node as the
    dying one if the operator flipped `active=false` on
    the wrong node (operator mistake). The orchestrator
    rejects `fromNodeID == newOwnerNodeID` early in
    `Engine.MigrateLiveInstances` (returns 0 attempted
    with a log line); the lease loop on the dying vmmd
    would otherwise create a snapshot no peer will ever
    read.

### Storage + indexes

Migration `00103_instances_migrated_from.sql`:

```sql
alter table instances
  add column migrated_from_node_id uuid,
  add constraint instances_migrated_from_fkey
    foreign key (migrated_from_node_id)
    references compute_nodes (id)
    on delete set null,
  add column migrated_at timestamptz,
  add constraint instances_migrated_at_chk
    check (migrated_at is null or migrated_at <= now() + interval '1 minute'),
  add column lease_token text,
  add constraint instances_lease_token_chk
    check (lease_token is null or length(lease_token) between 16 and 64);

create index instances_migrated_at_partial_idx
  on instances (migrated_at)
  where migrated_at is not null;
```

The partial index keeps the dashboard panel
("live migrations in the last hour") at O(few hundred)
rows even on a busy fleet. The full unique index on
`instances.node_id` already covers the
`ListLiveInstancesOnNode` read path.

Migration `00104_apps_migrated_at.sql`:

```sql
alter table apps
  add column migrated_at timestamptz,
  add constraint apps_migrated_at_chk
    check (migrated_at is null or migrated_at <= now() + interval '1 minute');
```

No partial index — telemetry only.

### Failure modes (called out explicitly)

- **Tier A4 reassigned app is then hard-killed.** A live
  migration on a dying node finds zero parked apps
  (Tier A4 already drained them) but might find live
  instances whose parent app is now owned by an active
  peer. The migration still runs (Tier A5's lease
  authority is the dying vmmd, not the app's owner
  schedd), and the resulting `instances.node_id` flip
  to the new owner is coherent with the app's
  rebalanced `apps.node_id`.
- **Peer raced.** Two schedds see the same drain
  event; each races to migrate the same instance. The
  conditional UPDATEs at Phase 2 + Phase 3.5 produce
  exactly one winner per instance; the loser sees
  `ErrConflict` and drops. Metric bumps
  `outcome="conflict"`.
- **Lease expiry.** Phase 3.5 didn't commit before
  `MigrateLiveLeaseSeconds` elapsed. The dying vmmd
  drops the tracker entry; the orchestrator's Phase 4
  returns "no lease" on the dying vmmd; the row stays
  in `state='migrating'`. A future PR (Tier A6?)
  needs a watchdog to recover stuck `migrating` rows;
  out of scope for Tier A5.
- **Tier A5 fails before Tier A4 ships.** Tier A5
  builds on the per-node schedd + placement claim
  primitives Tier A4 / PR #509 already shipped.
  Tier A5 is meaningless on a single-box posture.

### Known downstream effects

- **Snapshot key orphaning.** A migration writes the
  canonical snapshot at `snap/migration-<id>/{mem,
  vmstate}`. On Phase 4 the snapshot stays in storage
  (the dying vmmd's Park already wrote it). The
  per-vmmd snapshot-drift sweep reaps it on the next
  1-hour tick. A future PR can plumb a "purge on
  lease expiry" hook if storage proves tight.
- **mTLS epoch.** Tier A5 doesn't touch `node_keys`
  or the per-node mTLS epoch. The new owner vmmd's
  schedd already authenticated the dying vmmd at
  Phase 1 / Phase 3.5; the existing ADR-053 epoch
  rotation continues.
- **Counter lifecycle.** `apps.migrated_at` and
  `instances.migrated_at` are stamped in the same
  transaction at Phase 3.5; the two timestamps are
  guaranteed equal by the SQL `BEGIN; ... COMMIT;`
  boundary.

### Migration slots

Claim `97 + 98` at branch creation per ADR-041.
Slots must be empty on `main` immediately before
branch creation — verify with
`ls migrations | sort | tail -5`.

### Hard limits

Per the CLAUDE.md "hard limits" policy, every limit
in this ADR lives in `pkg/api/limits.go` and never
inline:

- `MigrateLiveMaxPerTick = 10` — per-drain-event cap
  on live-instance migrations.
- `MigrateLiveLeaseSeconds = 90` — total window from
  Phase 1 mint to Phase 3.5 commit; on expiry the
  dying vmmd drops the lease and the orchestrator's
  Phase 4 returns "no lease".

Both are env-overridable via `FAAS_MIGRATE_LIVE_*
`. A bad env panics via `WithMigrateLiveConfig` so a
typo doesn't silently fall back to the api.* default.

### Tests

- `pkg/state/pgstore_*_test.go` — parity for
  `ListLiveInstancesOnNode`, `MarkInstanceMigrating`,
  `MigrateInstanceOwner`, `CancelInstanceMigration`
  against a real Postgres.
- `pkg/sched/migration_handoff_test.go` — state-
  machine half of the contract (the four-phase
  orchestrator is exercised by the e2e metal-side
  gate; the unit tests pin the Store surface).
- `pkg/sched/live_migrator_test.go` — table-driven
  watcher tests parallel to `rebalancer_test.go`.
- `migrations/00103_instances_migrated_from_test.go` —
  8 cases (column shape, null/past/future timestamp
  checks, FK on-delete cascade, partial index,
  replay-safe, down-symmetry).
- `migrations/00104_apps_migrated_at_test.go` — 6
  cases.

### Open follow-ups (deliberately deferred)

- **Migrating-instance watchdog.** A stuck
  `state='migrating'` row (Phase 3.5 didn't commit and
  the lease expired) needs a watchdog to either
  resume or hard-delete. Out of scope for Tier A5; a
  follow-up ADR.
- **Per-app migration counter.** `apps.migrated_at`
  is the only per-app signal; a counter
  (`schedd_app_migrations_total{app_slug}`) would let
  the dashboard surface a "migrations per app per
  hour" panel. Defer.
- **Cross-node pause-and-resume.** Firecracker does
  not expose VM-level live migration primitives
  (CRIU or equivalent). Tier A5 is therefore a
  "snapshot-and-restore" model, not a true VM-handoff
  model. A future Firecracker / kernel upgrade could
  revisit this.

## Consequences

### Positive

- Active-active failover: a hard-killed node's
  customer-visible workloads are served by an active
  peer within seconds, without operator intervention
  beyond the standard `UPDATE compute_nodes SET
  active=false` drain.
- The Tier A4 active-passive HA shape is unchanged:
  the operator's runbook is the same shape (drain
  event → peer picks up); Tier A5 just adds the
  live-instance subset to the pickup list.

### Negative

- VM lifecycle: Tier A5 destroys + recreates the VM
  (one-shot blip), it does not keep the VM paused.
  Customers see ~350 ms of downtime per migration,
  not zero.
- Live migrations cost more CPU/RAM per event than
  parked-app reassignment (each spins a fresh
  firecracker). The 10/tick cap is the floor; a busy
  fleet with N dying nodes at once can take up to
  N×90 s to drain. The future watchdog will tighten
  this.
- A new transient state (`migrating`) in the state
  machine. Every consumer of `instances.state`
  (gateways, schedulers, watchdog) must explicitly
  handle it. The state-machine `IsLive` and
  `CountsForRAM` predicates are updated; future PRs
  may surface it in other dashboards.

## Verification

- `make migrations-check` — gates the new migrations
  (cross-PR slot check via
  `scripts/ci/check_migration_slots.sh`).
- `make test` — full unit suite must pass.
- `make test-metal` (or `make metal-lima` on Apple
  Silicon) — exercises the schedd↔vmmd four-phase
  path on Lima nested KVM or a reference control-plane node.
- `make leakcheck` — zero leaked netns/TAPs/cgroups.
- `make lint` — golangci-lint clean.
- `make spec-check` — vacuum + AST parity + git clean.

End-to-end manual smoke (Lima or a reference control-plane node):

1. Bootstrap a two-node fleet (FSN-1 + FSN-2) per
   `docs/runbooks/multi-box.md`.
2. Create an app + deploy + wake once on FSN-1
   (instance state = running).
3. `UPDATE compute_nodes SET active=false WHERE
   name='fsn-1'` — the operator's standard drain
   command.
4. Within `MigrateLiveLeaseSeconds + ~5s`:
   - `select name, active from compute_nodes;`
   - `select id, node_id, state, migrated_from_node_id,
     migrated_at from instances where app_id=<app>;`
   - The instance should now show
     `node_id = <fsn-2's id>`,
     `state = 'running'`,
     `migrated_from_node_id = <fsn-1's id>`.
5. Verify the metric on FSN-2:
   `curl -s http://localhost:9100/metrics | grep
   schedd_live_migration_decisions_total` —
   `outcome="migrated"` counter equals 1.
6. Curl the app from the gateway:
   `curl https://<app>.fsn-2.example.com/`.
   The response should arrive within ~350 ms of the
   `UPDATE compute_nodes` (cold-boot from snapshot).

## Acceptance (2026-08-07)

The four gaps that blocked acceptance are closed in the
PR that flips this ADR from `Proposed` to `Accepted`:

1. **Cross-node snapshot read** — verified via the
   regression test that the existing ADR-054
   `OCIRegistryStorageBackend` + `LocalCacheBackend`
   path delivers snapshot blobs from source node's
   `Put` to destination node's `Get`. The cache is
   default-on for `oci` mode (ADR-054 acceptance PR),
   so the destination's first pull populates the local
   cache and subsequent migrations hit the warm path.
   No streaming Put/Get variant is needed (a future
   v1.1 optimisation, out of scope).

2. **Destination-side slot reservation** —
   `pkg/sched/admission.go::NodeLedger.Request` gains
   a `Kind` field (`KindWake | KindMigration`).
   `KindMigration` reserves per-node RAM + vCPU
   (invariant §6.2-2 re-stated per-node) but skips
   per-app concurrency (invariant §6.2-1) so a
   customer with 1 instance at `MaxConcurrency=1`
   doesn't see a transient cap during the failover
   window. Wired into
   `pkg/sched/migration_handoff.go` at Phase 3 of the
   four-phase commit, BEFORE the wire call so a flood
   of inbound migrations cannot over-admit a
   destination. Rolled back on Phase 3 wire failure;
   persisted on Phase 4 success (the instance is now
   RUNNING on the destination).

3. **Gateway `state='migrating'` visibility** —
   `cmd/gatewayd/backend.go::handleInvalidation`
   treats `state='migrating'` as terminal-ish for
   routing purposes, alongside the existing
   `stopped | failed | parked | snapshotting`
   eviction set. The picker no longer routes traffic
   to a node mid-handoff; the next request re-admits
   which lands on the destination's wake path.

4. **Acceptance gates** — §14 M9 milestone row added
   to `docs/faas_implementation_spec.md` with the
   executable acceptance tests; new "Tier A5 gate"
   section in `docs/runbooks/multi-host-rollout.md`;
   two-node Lima fleet target (`make metal-lima-2node`)
   added so the §14 M9 gate is runnable on Apple
   Silicon M3+ without bare-metal x86_64.

### Cross-references

- §14 M9 row: `docs/faas_implementation_spec.md:914`
  (after M8).
- Runbook Tier A5 gate section:
  `docs/runbooks/multi-host-rollout.md`.
- ADR-054 (storage prerequisite):
  `docs/adr/054-oci-registry-storage-end-to-end.md`.
- ADR-067 (Tier A6 watchdog, ships alongside):
  `docs/adr/067-tier-a6-migrating-instance-watchdog.md`.
- Issue #95 slice 5 — the multi-box slice this ADR
  closes.