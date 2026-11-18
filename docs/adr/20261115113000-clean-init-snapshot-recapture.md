# ADR-20261115113000: Recapture a missing init snapshot from a clean boot

- Status: Accepted (owner decisions 2026-11-15: destroy at park and
  recapture cleanly in the background; without headroom, retry later and
  cold-boot meanwhile, never capture request state)
- Date: 2026-11-15
- Amends: ADR-20261106214916 (snapshot refresh after a release),
  ADR-20261114153000 (primed init snapshots), ADR-055/071 (snapshot tiers)

## Context

A deployment's init snapshot is meant to be the app just after readiness,
and since ADR-20261114153000 just after priming. The deploy prime and the
release refresher capture it that way.

When a wake finds no usable init snapshot on its node, it cold-boots. That
happens after a snapshot is evicted from the node cache, on a node that never
held the snapshot, or after a release whose refresh did not run. The instance
then serves requests, and at idle park `captureInitOrReuse` finds nothing to
reuse and captures it. That snapshot holds whatever the requests left in
memory: in-memory sessions, caches, per-user data and `/tmp`, which lives in
memory. Every later wake on that node restores that state, in every instance,
until the next deploy. It is the same leak the warm tier was made opt-in for
(ADR-20261110180000), except that nobody opted in.

## Decision

1. **Park.** When a park of a request-mode instance would capture a new init
   snapshot because none on its node can be reused, schedd destroys the
   instance instead, transitions it to STOPPED (kind
   `park_clean_recapture`, reason `init_snapshot_missing`) and queues a clean
   recapture for the app's deployment on that node. These parks keep their
   capture:
   - profiled parks (the ADR-824 checkpoint handshake);
   - frozen-function parks;
   - sealed-grant ("measured") runtimes, which never reuse a capture;
   - instances without a deployment.
2. **Recapture.** The snapshot refresher drains the queue through its
   release-refresh boot path. The recapture is admitted like a prime (RAM and
   CPU, never a serving slot), boots in mode `refresh`, is primed, captured
   and parked. It is never routed or billed, and it runs one at a time, only
   while the node's tenant residency stays under
   `SnapshotRefreshMaxResidencyPercent`. The request is dropped when:
   - a snapshot appeared meanwhile;
   - the deployment is no longer live;
   - the app is no longer eligible (inactive, not request mode, held,
     quarantined).
3. **No headroom.** A request that cannot run is retried every 30 s;
   meanwhile wakes on that node cold-boot (ADR-005: snapshots are cache). A
   failed boot is retried after 1, 5 and 15 minutes
   (`SnapshotCleanRecaptureMaxAttempts`), then dropped; the app's next park
   queues it again. Request state is never captured.
4. **Switch.** Runtime config `snapshot_clean_recapture` is `on` (default) or
   `off`. `off` restores the old capture at park, and turning it off clears
   the queue. The queue is in memory: a schedd restart loses it, and the
   next park requeues.
5. **Metric.** `schedd_init_snapshot_clean_recapture_total{outcome}` counts
   `queued`, `refreshed`, `deferred`, `failed` and `dropped`.

## Consequences

- Restored state is always the app's startup state. A request-path cache or
  session can no longer come back from an earlier request.
- Until the recapture lands, wakes on that node cold-boot. That covers one
  park plus a boot and capture, usually under a minute with headroom, longer
  without. The previous behavior restored the dirty snapshot sooner.
- One extra boot per missing snapshot per node, unbilled, inside the
  refresher's headroom rule.
- The warm tier (opt-in) is unchanged: it still captures after traffic, by
  the customer's choice.
- Release skew: the change is schedd-only. vmmd only needs `PrimeInstance`
  (ADR-20261114153000) for the recapture to be primed; an older vmmd yields
  an unprimed but still clean capture.
