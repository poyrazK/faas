# Realtime core beta: load run against production (2026-11-23)

Evidence for [ADR-20261123074615](../../../adr/20261123074615-core-realtime-beta.md).
Production `https://api.gregale.dev` on release `v0.1.18-rc.373`, a Scale
account, one client machine. Journey: `tests/journeys/realtime_load_test.go`
(`TestRealtimeLoad`), run with:

```sh
GREGALE_E2E_REALTIME_LOAD=1 GREGALE_E2E_REALTIME_LOAD_OUT=result.json \
  scripts/e2e-journeys.sh --tier full --run '^TestRealtimeLoad$' --cli-version v0.1.18-rc.373
```

## Result (run 20261123T081833Z, 11/11 steps passed)

| Measure | Result |
|---|---|
| Connections (original protocol, OIDC) | 500 of 500, 0 retries; handshake p50 499 ms, p95 553 ms, p99 1,049 ms |
| Idle hold | 90 s, 0 dropped |
| Fleet inventory | 500 listed, 2 realtime nodes queried, not partial |
| Principal send fan-out | 375 connections × 10 messages: 3,750 of 3,750 delivered; p50 179 ms, p95 221 ms, p99 223 ms |
| Channel publish fan-out | 125 subscribed connections × 10 messages: 1,250 of 1,250 delivered; p50 168 ms, p95 188 ms, p99 191 ms |
| Endpoint delete | every connection closed |

Latency is measured from the start of the API call on the client machine to
the frame's arrival on the same machine, so it includes both network paths.
`result.json` has the raw measurements; `journey-report.md` the step report.

## The per-address edge limit

A first run with 1,000 connections from the one machine connected 530: the
rest were reset at TCP with no HTTP response (2,440 retries, handshake p99
20 s). The compute edge holds at most `EdgeClientOpenConnsPerAddrMax` (512)
open connections per client address (ADR 20261103163954,
`FAAS_EDGE_CLIENT_LIMITS=1` on production). That is the edge's abuse guard,
not realtime capacity, so the journey now stays under it (500 by default) and
`docs/realtime.md` documents it: users behind one shared address, such as an
office NAT, share those 512 connections.

## Not covered

- More than 512 connections need several client addresses; the per-node
  ceiling (6,000 connections in realtimed's 512 MB, ADR-1323) is not
  measured here.
- Channel routing is off on production (`FAAS_REALTIME_CHANNEL_ROUTING_ENABLED`),
  so publishes are broadcast to both nodes; the run measures that mode.
