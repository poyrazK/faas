# Gate-A — Per-node schedd + schedd-side async placement claim (Phase 2)

Spec §14, Phase 2 / Gate A. Founding doc for the multi-host
topology: N peer-equal schedds, each owning a slice of apps via
`apps.node_id`, and `gatewayd-internal` routing each customer request to the
owner schedd. The mid-PR architectural pivot moved the placement
chooser out of `apid` (where the depguard rule
`apid-control-plane-only` forbids it) into schedd via
`pkg/sched.PlacementClaimSubscriber`. See
[`../adr/062-tier-a-per-node-schedd-and-placement.md`](../adr/062-tier-a-per-node-schedd-and-placement.md).

## Definitions

- **Owner schedd.** The schedd instance whose `Engine.OwnerNodeID`
  matches `apps.node_id` for a given app. Exactly one schedd owns
  any given app at any moment; the conditional
  `UPDATE … WHERE node_id IS NULL` in `Store.SetAppNodeID` is the
  serialisation primitive.
- **App owner.** The `compute_nodes.id` recorded on `apps.node_id`
  (migration 00083). Set once at claim time, **immutable
  post-claim** for v1.0 — rebalance is a v1.1 follow-up.
- **Eligibility.** A compute node is eligible to host apps iff
  `compute_nodes.active = true`. Inactive rows are skipped by
  schedd's chooser and absent from `gatewayd-internal`'s dial cache.

## Topology

N peer-equal schedd instances, one per non-`default-local` active
compute node. No leases, no leader election, no advisory locks, no
consensus. Each schedd:

1. Resolves its `OwnerNodeID` from `cfg.NodeName` →
   `compute_nodes.id` at startup (via `ActiveComputeNodes`).
2. Maintains its own VMMRouter + per-node schedd client cache
   (gateway-side).
3. Filters every pg_notify feed by `app.node_id == OwnerNodeID`
   before issuing vmmd calls. Closes the duplicate-`Prime` hazard.
4. Runs the `PlacementClaimSubscriber` on `NotifyAppChanged`;
   races the conditional UPDATE to stamp owners on `kind="created"`.

`default-local` remains the synthetic single-box seed node
(migration 00024 + 00083 backfill). When a box runs in single-node
mode (no non-`default-local` is active), `cfg.NodeName` is empty
and schedd falls through to the legacy fleet-wide posture with
`OwnerNodeID = ""`.

The shared Postgres is the coordination point: the conditional
UPDATE serialises every schedd into one winner per unplaced app.

## Active-passive is control-plane only — do not invent `compute_nodes.state`

Phase 2 / Gate A does **not** introduce `compute_nodes.state`. There
is no per-schedd `state` column, no `standby`/`active`/`drained`
semantics on `compute_nodes`. The active-passive concept
applies only to the gateway DNS cutover (which is orthogonal to
schedd peer equality) and is not changed by this gate.

If you need to mark a schedd "standby", use the node drain action in
the Operations Console. The resulting durable `node_drain` intent makes
the chooser skip it and the gateway stop dialling it.

## Compute eligibility — `compute_nodes.active`

Drain a compute node without removing it in Operations → Nodes. Supply a
change/incident reason, confirm the action, and retain the returned
`intent_id`. To restore an unavailable node, use **Activate** on the same
page and poll the intent until it reaches a terminal state.

The `compute_node_changed` notify fires on UPDATE; the schedd's
router refresh watcher rebuilds its dial cache and the gateway's
per-node schedd client cache rebuilds its target URL map.

### Drain behaviour — Tier A4 cross-node rebalance (ADR-064)

Draining a node is now self-healing for the parked-app fleet.
Every peer schedd consumes the `active=false` event via the
rebalancer watcher (`pkg/sched/rebalancer.go`) and reassigns
each parked app owned by the drained node to itself, gated by
admission headroom + cooldown + per-tick cap (defaults: 60s
cooldown, 50 apps per drain event — both overridable via
`FAAS_REBALANCE_COOLDOWN_SECONDS` and
`FAAS_REBALANCE_MAX_PER_TICK`).

Apps that are `running` / `waking` / `cold_booting` /
`snapshotting` at the moment of drain stay with the dying node
(active-passive HA is orthogonal — the operator must let the
node drain cleanly before shutting down). Apps past the per-tick
cap retry on the next `compute_node_changed` event (the
upcoming heartbeat-staleness watchdog at issue #97 §3 will
re-fire on a different cadence, closing the gap on a stuck
app).

Observe the rebalancer's per-batch decision breakdown via the
metric:

```
curl -s http://localhost:9100/metrics | grep schedd_rebalance_decisions_total
```

The `outcome="migrated"` counter is the success signal;
`outcome="conflict"` is a peer winning the conditional UPDATE
race (expected under contention); `outcome="no_headroom"` is
the admission cap rejecting a rebalance (operator should drain
the dead node's apps across more peers or wait for the next
event); `outcome="cooldown"` is the per-app gate (a flap-loop
suppression); `outcome="no_eligibility"` is the status filter
rejecting a non-parked app.

If a schedd was down while the drain notify fired, the next
boot runs `Engine.RebalanceOrphanedApps(ctx, "")` once at
startup — the cold-start sweep reconciles every orphan
regardless of which dead node owned it, so an interrupted
drain doesn't leave apps permanently pinned to the dead node.

### Drain behaviour — Tier A5 cross-node live-instance migration (ADR-066)

Tier A4 only handles parked apps. **Tier A5** handles the
remaining case: a `running` / `waking` / `cold_booting` /
`snapshotting` instance on the drained node is migrated to
an active peer via a four-phase handoff
(`pkg/sched/migration_handoff.go`):

  Phase 1  PrepareLiveMigration      — dying vmmd Park +
                                        mint lease
  Phase 2  MarkInstanceMigrating      — store conditional
                                        UPDATE state=running
                                        → state=migrating
  Phase 3  AdoptMigratedInstance      — new owner vmmd
                                        Restore from the
                                        snapshot dying vmmd
                                        wrote at Phase 1
  Phase 3.5  MigrateInstanceOwner    — store single-tx
                                        commit: node_id flip,
                                        lineage stamp
  Phase 5  AcknowledgeMigration       — dying vmmd lease
                                        cleared (VM already
                                        gone — Park's
                                        contract)

Phase 4 (rollback) runs on any failure between Phase 1 and
Phase 3.5: `CancelLiveMigration` + `CancelInstanceMigration`
restore the instance row to `state='running'` and the dying
vmmd's lease entry is dropped.

Per-drain-event cap: `MigrateLiveMaxPerTick = 10` (env-
overridable via `FAAS_MIGRATE_LIVE_MAX_PER_TICK`). Lease
window: `MigrateLiveLeaseSeconds = 90` (env-overridable via
`FAAS_MIGRATE_LIVE_LEASE_SECONDS`). On lease expiry the
dying vmmd drops the lease and the canonical snapshot stays
in storage until the per-vmmd snapshot-drift sweep reaps it.

**Caveat (read carefully):** Tier A5 destroys + recreates
the VM (one-shot blip of ~350 ms cold-boot from snapshot).
Firecracker does not expose VM-level live migration
primitives — there is no pause-and-keep-running model. For
true zero-downtime failover the operator must use the
active-passive HA path (let the node drain cleanly before
shutting down).

**Prerequisite:** Tier A5 only works on fleets that have
shipped `OCIRegistryStorageBackend` (ADR-054 / PR #457).
`LocalStorageBackend` cannot share snapshots across nodes;
on a default-local-only fleet the live migration path
fails at Phase 3 (Restore can't resolve the storage keys).

Observe the migrator's per-instance outcomes via the metric:

```
curl -s http://localhost:9100/metrics | grep schedd_live_migration_decisions_total
```

`outcome="migrated"` is the success signal; `outcome=
"peer_failure"` is a gRPC dial / FC uAPI error (transient,
next drain retries); `outcome="conflict"` is a peer winning
the conditional-UPDATE race; `outcome="lease_expired"` is
the lease timer firing before Phase 3.5 committed (the
instance row stays in `state='migrating'` — a future
watchdog will recover stuck rows).

If a schedd was down while the drain notify fired, the next
boot runs `Engine.MigrateLiveInstances(ctx, "")` once at
startup — the cold-start sweep reconciles every dead-node-
owned live instance regardless of which dead node owned it.

## Adding a second compute node

The minimum operator surface for cutting over a second compute
node:

```bash
# 1. Register the row through the typed operator command.
gregalectl compute-nodes add \
  --name fsn-2 \
  --target-url tcp://10.0.0.2:7000 \
  --vcpus 80 --mem-mb 28000 --max-concurrency 10 \
  --admission-ceiling-mb 23800

# 2. Bootstrap vmmd on the new box (per deploy/ansible).
ssh faas-fsn-2 'make bootstrap-compute'
ssh faas-fsn-2 'systemctl enable --now faas-vmmd'

# 3. Bootstrap schedd on the new box with the per-node identity.
ssh faas-fsn-2 'FAAS_NODE_NAME=fsn-2 systemctl enable --now faas-schedd'

# 4. Verify the schedd resolved its owner and stamped at least one
#    claim (or none, if no unplaced apps existed at boot).
ssh faas-fsn-2 'journalctl -u faas-schedd --since -1m | grep "claim\|owner"'
```

Schedd startup refuses to come up if:

- `FAAS_NODE_NAME` is empty while any non-`default-local` is
  active. **Fail-fast on ambiguity.**
- `FAAS_NODE_NAME` matches no active row. **Fail-fast on typo.**
- `FAAS_NODE_NAME` resolves to `default-local` while any
  non-`default-local` is active. **Fail-fast on synthetic
  poisoning.**

## Cold-start sweep

`pg_notify` is fire-and-forget. A schedd that was down while an
apid createApp landed missed the `kind="created"` notify. To close
the missed-event window, `cmd/schedd` runs a one-shot
`store.ListUnplacedApps()` + `engine.ClaimUnplaced` per row at
boot. Sub-second in steady state; one tx per unplaced app.

Operator diagnosis:

```bash
# Are there any unplaced apps right now?
psql -c "SELECT id, slug, account_id FROM apps WHERE node_id IS NULL AND status <> 'deleted';"
```

If non-empty and a schedd is healthy, the cold-start sweep or the
live subscriber will reconcile within seconds. If non-empty and no
schedd is running, start one — the sweep will run on its first
tick.

## Gateway wake race (sub-second, ≤ 5 s pathological)

A fresh app's first Wake may arrive in the gateway before the
schedd has stamped `node_id`. The gateway's per-node schedd client
cache rejects empty NodeID today and the existing fallthrough path
in `cmd/gatewayd-internal/scheddrouter.go` handles it. The customer's
TCP-level retry closes any gap.

No new gateway retry/poll is added in this gate. The schedd
claim latency is sub-second in steady state. Document but do not
hand-hold.

## Validation matrix

| Check | Where | When |
|---|---|---|
| `select name, active, schedd_target_url from compute_nodes` | psql | continuous; all non-default rows must have `schedd_target_url` populated |
| `select node_id, count(*) from apps group by node_id` | psql | continuous; should split across non-default rows within seconds of a createApp |
| `select node_id, count(*) from instances group by node_id` | psql | continuous; should mirror the apps distribution once wakes land |
| `select id, slug from apps where node_id is null and status <> 'deleted'` | psql | continuous; empty in steady state, ≤ 1 during a cold-start sweep |
| `journalctl -u faas-schedd \| grep "stamped owner"` | box | continuous; one log per successful claim |
| `gateway_wake_latency_seconds` p95 ≤ 1 s | `pkg/wire` ops metrics | continuous; alert if breached for 5 min (`FaasWakeLatencyHigh`) |
| `faas_build_success_pct` ≥ 99 % | `pkg/wire` ops metrics | continuous; alert if breached for 5 min (`FaasBuildSuccessLow`) |
| `make migrations-check` exit 0 | CI | continuous; gates 00083 + 00084 |
| `make test` exit 0 | CI | continuous |

## Rollback

If a Phase 2 schedd surfaces a regression (e.g. two schedds stamp
the same app to different owners — `apps.node_id` flipping on each
notify — see the rejection case in
`pkg/sched/placement_claim_test.go::TestClaimUnplaced_RaceLosesSilently`),
rolling back is local:

1. **Stop the new schedd.** On the new box:
   ```bash
   ssh faas-fsn-2 'systemctl stop faas-schedd'
   ```
2. **Drain it in Operations → Nodes.** Record the returned intent ID.
   Once the `node_drain` intent succeeds, the gateway stops dialling and
   the chooser skips it.
3. **Apps auto-rebalance to the surviving schedds.** Each peer
   schedd consumes the `active=false` notify via the
   Tier-A4 rebalancer (ADR-064) and atomically re-stamps the
   pinned apps onto itself, gated by admission headroom and
   capped at 50 apps per drain event (60s cooldown between
   same-app reassignments). Excess apps past the per-tick cap
   retry on the next `compute_node_changed` event. Verify the
   rebalance landed:
   ```bash
   psql -c "SELECT name, active FROM compute_nodes;"
   psql -c "SELECT slug, node_id, status, reassigned_at FROM apps ORDER BY slug;"
   ```
   The apps previously owned by the dead node should now show
   `node_id = <surviving schedd's id>`. Confirm via the
   metric on the surviving schedd:
   ```bash
   curl -s http://localhost:9100/metrics | grep schedd_rebalance_decisions_total
   ```
   The `outcome="migrated"` counter should equal the number of
   apps that moved. A schedd that was down during the drain
   catches up on its next boot via the cold-start sweep
   (`Engine.RebalanceOrphanedApps(ctx, "")`).

`vmmd`, `imaged`, `meterd`, `builderd`, `apid`, `gatewayd-public`, `gatewayd-internal` are
unaffected by the rollback.

## Removed in this gate

The following constructs from the pre-Phase-2 runbook are
**removed** and MUST NOT be referenced:

- `compute_nodes.node_name` (replaced by `name`)
- `compute_nodes.state` ('active' / 'standby' / 'drained')
- `compute_node_notify` channel
- `pkg/scheduler` package
- `pkg/scheduler` per-app admission path

If you find a reference in another doc or runbook, file a
follow-up to delete it.

## Acceptance

The runbook is required by spec §14 Phase 2 / Gate A. The test
that pins its presence + section shape is
`TestRunbooks_GateA_ExistsAndHasRequiredSections` in
`cmd/e2e/runbooks_e2e_test.go`.
