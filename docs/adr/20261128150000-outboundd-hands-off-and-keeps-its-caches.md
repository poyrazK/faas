# ADR-20261128150000 · outboundd hands off on reload and keeps its caches

- **Status:** accepted
- **Date:** 2026-11-28
- **Extends:** [ADR-1736 zero-downtime daemon restarts](20261018072701-zero-downtime-daemon-restarts.md),
  [ADR-20261123110000 gateways hand off on converge](20261123110000-gateway-hand-off-on-converge.md).

## Context

outboundd carries every app's outbound and AI gateway call. It is one process
on the control plane and restarted 40 times in the week to 2026-11-28, mostly
for releases. Each restart:

- drained for 25 s, then cut every call still running. AI calls run up to
  30 minutes (streams, reasoning models, realtime sessions); #8198 made a
  stopping outboundd settle them at their reservation, but the caller still
  lost the answer;
- emptied the response cache and the semantic cache index, both process
  memory, so hit rates restarted at zero after every release.

Admission, leases, circuit breakers and provider cooldowns already live in
Postgres, so two outboundd processes can serve side by side without
double-spending a budget.

The owner chose (2026-11-28) to fix restarts on the control plane host
rather than run outboundd on several hosts or every compute node, to hand
the caches over across restarts rather than move them to Postgres or a
platform Valkey, and to keep preview deployments outside the gateway.

## Decision

1. **outboundd hands off on reload** like the gateways, apid and realtimed:
   `wire.HandoffDaemon`; unit `Type=notify`, `NotifyAccess=main`,
   `ExecReload=/bin/kill -USR2 $MAINPID`, `OOMPolicy=continue`,
   `WatchdogSec=60s`. Every listener (control, app relay, public access-token
   listener, metrics) binds through `wire.ListenInherited` / `wire.Share`
   before `RequireInheritedListenersClaimed`, so the successor claims the same
   sockets. The control plane role's handler runs `faas-reload-or-restart`,
   and deployctl reloads it because its unit hands off
   (`daemonunitspec.HandsOffOnReload`).
2. **A handed-off generation drains for up to `api.HandoffDrainMaxSeconds`**
   (1,800 s), which covers the longest AI call. Its workers (replays, canary
   evaluation, batches, evaluations, pruning) stop at the hand-off: they run on
   the generation context and the successor runs them. A stop during that
   drain ends its AI calls at once (`Handler.EndAICalls`), so they settle at
   their reservation within one grace.
3. **The caches cross restarts and hand-offs as one snapshot** in the unit's
   private runtime directory, `/run/faas-outboundd` (tmpfs, mode 0700,
   `RuntimeDirectoryPreserve=restart`, `FAAS_OUTBOUNDD_CACHE_SNAPSHOT_DIR`):
   - a hand-off writes it before starting the successor, which loads and
     deletes it before it serves; a failed hand-off deletes it;
   - a plain stop writes it after its drain, and the next start loads it;
   - the snapshot holds the response cache's key secret (keys are HMACs
     under it), its live entries in LRU order and the semantic index entries
     whose answers are still cached. It is ignored and deleted when older
     than `api.OutboundCacheSnapshotMaxAgeSeconds` (600 s) or of another
     format version (`cacheSnapshotVersion`, bumped when the key derivation
     or the shape changes).
4. **MemoryMax rises from 256M to 512M**: two generations share the unit's
   cgroup during a hand-off, and the snapshot (at most ~96 MB: 32 MB of
   responses, 64 MB of semantic index) is tmpfs charged to it until loaded.

## Consequences

- Binary-only releases no longer cut outbound or AI calls, and caches survive
  them. A release that changes outboundd's unit, drop-ins or env files still
  restarts it; the caches still survive (the stop writes the snapshot).
- Cached provider responses sit in a root-and-outboundd-only tmpfs file for
  the seconds between write and load. The runtime directory is removed when
  the unit stops, so they never outlive the service; a reboot clears it.
- The control plane host stays a single point for outbound calls; host-level
  HA (instances on several control planes, or per compute node) is left for
  ADR-1237's multi-control-plane phase.
- Preview deployments still call providers directly, outside the gateway's
  caps, logging and redaction (ADR 20261027132936 amendment 2).
- Tests: `pkg/outbound` cache snapshot round trip, stale and foreign
  snapshots, `EndAICalls`; `cmd/outboundd` stop and hand-off drains;
  `pkg/daemonunitspec` hand-off daemons.

## Alternatives

- **Instances on several control planes, or one per compute node.** Real
  host-level HA, but prod has one control plane, and per-node instances put
  provider keys on compute nodes. Deferred.
- **Caches in Postgres or a platform Valkey.** Shared and durable, but a
  database read per cacheable call or a new platform service to run, for a
  cache whose loss costs only hit rate.
- **Carrying the snapshot over the hand-off pipe.** Avoids the file, but a
  plain restart has no pipe; one mechanism covers both.
