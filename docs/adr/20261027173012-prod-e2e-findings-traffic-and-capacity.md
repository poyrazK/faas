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
