# ADR 20261027173012 · Production e2e findings: rate counter grace, unroutable capacity, build drive space

- **Status:** proposed
- **Date:** 2026-10-27
- **Amends:** ADR-570 (traffic enforcement guarantees), its central rate
  admission paragraph; ADR-1520 (rate-limit token leasing), its accuracy
  note on the breaker fallback
- **Related:** issue #5709 (customer journey suite against production,
  rc.296/rc.297), ADR-20261026160000 and ADR-20261027085556 (the outage
  graces this record matches), ADR-1736 (two-generation hand-off),
  ADR-20261023115741 (hidden capabilities), the owner rule of 2026-10-27:
  no control-plane Postgres on the request hot path

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

The customer journey suite (`tests/journeys`) ran against production on
2026-10-26/27 and filed #5709. Several of its findings were already fixed
on main and only wait for a release: `gregale logs` (#5611), the parked-app
burst (#5672), the failed-scan boot refusal (#5685, #5587), routing reads
(#5674) and security.txt (#5604). This record covers the ones that were
still open:

1. **Rate counter outages refuse traffic.** Central mode consumes the shared
   Postgres counter for every admitted request; leasing (ADR-1520, on in
   production since 2026-10-25) makes that rare but does not change the
   failure path. One failed consult or lease statement opens a gateway-wide
   breaker for one second, and every request without a held lease token is
   refused with 503 `rate_limit_unavailable`. During a safe redeploy at
   least 10 of 1,232 probes to a serving app got it. The control-plane VM
   (2 vCPU, Postgres beside apid, schedd and CI runners) ran above load 4 in
   14% of minutes over 36 hours.
2. **429 `app_concurrency_reached` with nothing live.** When schedd answered
   an admission at capacity and the gateway had no routable target, the
   gateway answered 429 with its own target count as `observed`:
   "max_concurrency is 1; 0 already live". Schedd counts instances the
   gateway cannot route to yet: a deployment check's instance, a wake
   started by another producer, an instance being stopped. Customers saw
   their limit blamed for a platform transition, on fresh deploys, on
   bursts, and on a live release while a bad release was refused.
3. **Builds refused for disk space.** builderd needs 20 GiB free
   (`BuildDriveMinFreeBytes`) on the filesystem that holds build drives. The
   node artifact cache shares it (`/var/lib/faas/cache` is a bind mount of
   `/srv/fc/cache`) and evicts only below `api.ArtifactCacheMinFreeBytes`,
   8 GiB. Production compute nodes sat at 18.5–21.2 GB free, so most
   cache-miss builds failed within a second while the cache held tens of
   gigabytes it could drop.

## Decision

1. **Rate counter outage grace.** For `api.TrafficRateLimitOutageGrace`
   (5 minutes, the same as the security and routing graces) after the shared
   counter last decided a request on a gateway, a counter failure is decided
   by that gateway's own bucket. The bucket holds the counter's last answer
   and refills at the plan or app rate, so the limit still holds, per
   gateway: a request over it gets the ordinary 429, one within it is
   served. Past the grace, or on a gateway that never reached the counter,
   the request is refused with 503 `rate_limit_unavailable` as before. Each
   local decision counts in `gateway_ratelimit_local_fallback_total{scope}`
   and `FaasRateLimitCounterFallback` warns; refusals keep
   `gateway_ratelimit_degraded_total` and `FaasTrafficControlRefusing`.
   During the grace a limit can admit up to once per gateway (two in
   production). Pre-auth source budgets keep their own fail-closed path.
2. **Unroutable capacity is not the customer's limit.** An admission
   answered at capacity is a 429 `app_concurrency_reached` only when the
   gateway itself sees as many instances as the effective
   `max_concurrency`, and then `observed` is that count. Otherwise the
   gateway re-reads the app's live targets and waits up to 2 seconds for
   one to become routable, serving the request when it does. Past that it
   answers 503 `capacity_unavailable` "App instance starting" with
   `Retry-After: 1`. This applies to ordinary requests, exact-deployment
   and revision requests, and public routing wakes.
3. **Build drives reclaim cache space.** Before builderd materialises a
   build drive on a filesystem with less than `BuildDriveMinFreeBytes` free,
   it asks the node artifact cache to evict its oldest unpinned entries
   until that much is free (`storage.ReclaimFreeBytes`). The cache's
   standing floor is unchanged; the drive capacity check still decides.
   Evicted snapshots and layers are cache (ADR-005) and refetch on demand.
4. **Smaller fixes, no behaviour change beyond the message.**
   - vmmd names imaged's failed-scan placeholder (`CRITICAL=9999`,
     `wire.BaseScanFailedCount`) "Base image scan unavailable" with refusal
     reason `scan_failed`, instead of reporting 9999 vulnerabilities.
   - `gregale logs --source http` and its HTTP-only flags explain that HTTP
     request logs belong to the production debugger when that capability is
     hidden for the account, instead of printing "no such API route".
   - gatewayd-public cancels the context of everything `run` started before
     its Postgres pool closes. `pool.Close` waits for the LISTEN
     subscriptions' connections, which only ended on SIGTERM, so a
     generation that handed off (ADR-1736) never exited and logged "closed
     pool" every two seconds (production, 2026-10-26 21:24 onwards).

## Consequences

- A control-plane Postgres blip under five minutes no longer refuses app
  requests for rate limiting; limits loosen to per-gateway enforcement for
  its duration. A longer outage still fails closed.
- Requests that met a capacity transition wait up to 2 seconds longer
  before their answer, and most are served instead of refused.
- A full build node loses its oldest cached artifacts before a build; the
  next wake of an evicted snapshot is a remote fetch.

## Verification

- `pkg/gateway`: `TestLimiter_CentralOutageGraceDecidesLocally`,
  `TestHandler_CentralFallbackIsObservableAndAuditCooledDown`,
  `TestAtCapacityWithoutRoutableTarget`,
  `TestAtCapacityWithVisibleInstancesIsTheLimit`; the full package passes.
- `cmd/gatewayd-internal`: `TestTrafficFleetDaemonRateCounterOutageGrace`
  (Postgres fleet); the existing outage fleets pin the refusal past the
  grace.
- `pkg/storage`: `TestLocalCacheBackend_ReclaimFreeBytesEvictsOldestUntilFree`;
  `pkg/builderd`: `TestReclaimBuildDriveSpace`.
- `pkg/fcvm`: the scan refusal metric table covers `scan_failed`;
  `cmd/gregale`: `TestCmdLogsHTTPExplainsHiddenProductionDebugger`.
- Production, after release: #5709's journeys
  (`TestSafeReleaseZeroDowntime`, `TestGuestGuarantees/burst_wake`,
  `TestHealthGate`, `TestServeAndObserve`) and a cache-miss build on a node
  under 21.5 GB free.

## Amendment 1 (2026-10-29): over-limit requests stay 429 when the counter fails

Eight parallel requests against a 2 rps, burst 3 throttle rule returned 503
`rate_limit_unavailable` for three of them instead of 429. Two things turned
one slow counter round trip into refusals of requests that were over the
limit anyway:

- A request that joins a batched consume waited only
  `centralConsumeTimeout` (1 s), although its batch starts only when the
  statement in flight ends and each statement may take that long
  (ADR-570 amendment 2026-10-28). Two in-bound statements timed it out, and
  the failure opened the 1 s consult breaker for every other request.
- Past the grace, every counter failure was answered as unverified, even
  for a request the gateway's own bucket already refused.

Decision:

1. A joined request waits up to `2 × centralConsumeTimeout`: the statement
   in flight plus its own batch.
2. Past the grace (or before the gateway ever reached the counter), a
   request refused because the counter failed gives its token back to the
   gateway's bucket, so that bucket counts only requests the counter
   admitted and never holds fewer tokens than the counter. A request the
   bucket refuses is therefore over the limit and gets the ordinary 429; a
   request the bucket would admit is refused with 503
   `rate_limit_unavailable` as before. Both still count in
   `gateway_ratelimit_degraded_total`. No request is admitted without the
   counter, so the decision of ADR-570 stands; only the refusal code
   changes. This applies to app, account and throttle-rule limiters,
   dimensional rules included.

Consequences: a burst over a limit during a counter blip past the grace now
reads as rate limiting, not as a platform outage. A pure outage still
answers every request 503, because the returned tokens keep the bucket from
draining.

Verification: `pkg/gateway`
`TestLimiter_CentralOutageRefusesDrainedBucketAsOverLimit`,
`TestCentralCoalescer_FollowerWaitsForLeaderAndOwnBatch`, and the existing
outage tests (`TestLimiter_RealBackend_PGErrorFailsClosed`,
`TestLimiter_CentralOutageGraceDecidesLocally`,
`TestSharedRateStoreFailureReturns503BeforeGuest`).

## Amendment 2 (2026-10-30): leases get the consume bound

Production gateways run with ADR-1520 leasing on (`ratelimit_lease: true` in
the production manifest since 2026-10-25), so their counters are consumed by
lease statements, not by the direct consume amendment 1 and ADR-570's
2026-10-28 amendment bounded at 1 s. A lease statement was still bounded by
`centralConsultTimeout` (250 ms), the pre-auth bound, and so was a request
waiting for one. A counter commit past 250 ms (a checkpoint or fsync stall)
failed the lease, opened the 1 s breaker, and refused requests within their
limit with 503 `rate_limit_unavailable`.

Decision:

1. A lease statement is an authoritative consume and is bounded by
   `centralConsumeTimeout` (1 s). The PostgreSQL backend still bounds
   connection acquisition by 250 ms, so a saturated pool fails fast and opens
   the breaker as before.
2. A request waiting for a lease waits up to `centralBatchWait`
   (`2 × centralConsumeTimeout`): the statement in flight plus the one sized
   for it, as a joined batched consume does (amendment 1).
3. Returning unspent lease tokens stays bounded by 250 ms; it runs off the
   request path.

Consequences: a request that waits for a lease can take up to 2 s longer
before its answer when Postgres stalls, instead of failing at 250 ms. Only
counters at or near their limit wait (ADR-1520), and the request budget
still bounds the request as a whole.

Verification: `pkg/gateway` `TestLeaseOutlastsASlowCommit` (a 400 ms commit
admits the waiting request) and the existing lease tests.
