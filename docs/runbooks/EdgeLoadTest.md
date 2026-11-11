# Compute edge load test, no app behind it

This test loads the public edge on every compute node (`gatewayd-public` in
compute mode, ADR-20261103163954) with requests that name no app. The edge and
`gatewayd-internal` answer them 404, so no VM wakes and no customer app is
involved. It measures what the platform pays per request before any app runs:

- **miss:** one fixed non-app host on kept-alive connections. This is the edge's
  and gatewayd-internal's per-request cost. Both answer from remembered misses,
  so the control plane is not read after the first request.
- **handshake:** the same host with a new TLS connection per request. This is
  the cost of the handshake: about 1.1 ms of edge CPU on 2026-11-05, mostly the
  hybrid post-quantum key exchange.
- **new-host:** a never-seen host per request, as a scanner sends. Each one
  spends the edge's and the gateway's new-host lookup budgets (25/s, burst 500
  per node) and costs about two control-plane Postgres reads.

It runs against production, so it is small and capped:

- Rates are per node and stay below the limits they would otherwise measure.
  `run.sh` refuses anything higher. The caps (`pkg/api/limits.go`) are:
  - **miss:** at most 450/s, under 500 requests a second per client address;
  - **handshake:** at most 60/s. The binding limit is 32 handshakes in flight
    per address (`EdgeTLSHandshakesInFlightPerAddrMax`), which binds before
    100 new connections a second. From a client 160 ms away a handshake takes
    about 330 ms, and 80/s was refused on 2026-11-09;
  - **new-host:** at most 22/s, under the 25/s lookup budget.
- Each step is a 10 s ramp and a 60 s hold. The first failing step ends the run.
- Control-plane Postgres CPU pressure is sampled every 5 s. Above 50% (avg10),
  the step is stopped at once. On 2026-11-05 a control-plane stall froze all app
  traffic for about 55 s, so this guard comes first.
- No step starts within 3 minutes before or 5 minutes after a quarter hour,
  when cron and probe wakes load the nodes and the control plane. No step
  starts while Postgres pressure is above 15% either. On 2026-11-09 the 17:45
  wave still held it near 60% at 17:48, and the first run stopped on load it
  had not caused.

It does not change any configuration. The per-address client limits stay on.

## Before you start

- **Quiet window.** No release or canary in flight, no other load test, no
  failure injection. Check `docs/STATUS.md` and the release channel.
- **Client.** k6 1.x, jq, and curl. You also need `NODE_SSH` access (default
  `~/gregale-e2e/bin/gpssh`, an IAP ssh wrapper) to every compute node and to
  the control plane. The client must not run Cloudflare WARP: it sends direct
  entry through Cloudflare and inflates the handshake.
- **Entry addresses.** Each compute node's public entry IPv4: the manifest's
  `entry_ipv4`, or the DNS-only A records of a direct-entry host. As of
  2026-11-09 the apps wildcard is still proxied by Cloudflare, so ask the
  pilot host:

  ```sh
  dig +short "direct-pilot.$DOMAIN"
  ```

## Run

```sh
NODES="gregale-compute-1=<entry IPv4> gregale-compute-2=<entry IPv4>" \
DOMAIN=gregale.dev tests/load/edge/run.sh
```

- First it checks that `MISS_HOST` (default `lt-edge-none.<domain>`) answers
  404 at every node, and that every node's counters can be read.
- Then it takes a 30 s idle baseline and runs `miss` 50/100/200/300,
  `handshake` 20/40/60 and `new-host` 5/10/20 per second per node. That is
  about 15 minutes, plus waits for cron waves.
- Change the steps with `SCENARIOS`, `MISS_RATES`, `HANDSHAKE_RATES`,
  `NEW_HOST_RATES` and `DURATION_S`.
- Each step's readout is in `results-edge-<time>/<scenario>-<rate>/readout.txt`,
  next to k6's summary per node and the Postgres samples.

Stop it (Ctrl-C) if `FaasComputeEdge*` or `FaasPostgresCPUStalled` fires, or
if customer 5xx rise on the fleet dashboard.

## Reading the results

| Check | Passes when |
|---|---|
| Answers | Every answer is 404. Up to `NO_ANSWER_MAX` (5) requests per node may get no answer at all, which is the client's own network. |
| Latency | p99 at most `P99_MAX_MS` (1,500 ms). From Istanbul the round trip is about 160 ms, and a new connection adds two more. |
| Node | Busy at most `NODE_BUSY_MAX` (85%). |
| Refusals | `gatewayd_public_edge_refused_total` does not move. A refusal means the step measured the limiter, not the edge. |
| Unknown hosts | `gateway_route_lookup_unknown_host_total{outcome="shed"}` does not move. `remembered_miss` grows with `miss` and `handshake`. |
| Rate | k6 sent at least 95% of the step's requests. Below that, the client set the rate. |
| Control plane | Postgres stalled (PSI "some") under half of the step, the `FaasPostgresCPUStalled` line. Pressure never passed 50% (avg10). |

Read these lines by eye:

- `net us/request` for the edge and gatewayd-internal: CPU above the idle
  baseline, divided by requests. Compare `miss` with the app-request cost from
  the last app load test (edge about 0.8 ms, gatewayd-internal about 1.4 ms on
  rc.328). It is the floor for a request that wakes nothing.
- `db pool acquires` in `new-host`: about two per name. Many more means unknown
  hosts reach Postgres more often than the budget allows.

`tests/load/edge/run_test.sh` (in `make lint-drill`) checks the runner itself
against fakes.
