# ADR-028 · gatewayd Remote Routing via gRPC-Bridged ForwardHTTP

- **Status:** accepted v1.2 (2026-08-01). Shipped via PRs #112, #113, #114, #115, #119, #122; the placeholder `host_ip:8080` is gone, `WakeResponse.node_id` is on the wire, and gatewayd's `NodeClientCache` evicts on `compute_node_changed`. v1.1 cross-references ADR-025 v1.1 and ADR-029 v1.1 — the three-axis story now reads as a unit. v1.2 (PR-D / ADR-047): the legacy unary `ForwardHTTP` RPC was removed in favour of the bidi `ForwardHTTPStream` RPC — every code path between gatewayd and vmmd now streams. The §6.4 failure-mode catalogue (spec §6.4) lists the per-error-mode contract for `ForwardHTTPStream` (overlay partition, per-instance bridge fail, TargetSet regression guard, WakeResponse wire-shape reverts).
- **Date:** 2026-07-22 (proposed); 2026-07-31 (accepted v1.1); 2026-08-01 (accepted v1.2 — streaming supersession)
- **Issue:** #98
- **Decision:** Replace gatewayd's direct HTTP-to-inner-VM reverse proxy
  with an in-process HTTP→gRPC forwarder. gatewayd dials the vmmd that owns
  the instance over the overlay and bridges the bytes through vmmd's
  ForwardHTTPStream RPC (issue #471 / ADR-047); vmmd nsenter's the
  per-instance netns and dials the guest's 10.0.0.2:8080 from inside.

## Context

Issue #98 closes the multi-node story that began with #97. After
PR #112 + slice 2 landed, schedd picks the right compute_node.id for
every wake; gatewayd still dials `host_ip:8080`, a placeholder inside
vmmd's jailer netns that is unreachable from outside vmmd's host.
Multi-node placement is correct; gatewayd has no way to put bytes on
the box.

Three pieces are missing:

1. **gatewayd's routing cache** learns which node an instance lives on.
2. **gatewayd's reverse proxy** hops the public listener to the remote
   node instead of assuming a local virtual interface.
3. **Tailscale/Wireguard overlay** is provisioned at the system-
   configuration level so the edge gateway can reach remote VM TAP
   interfaces securely.

ADR-025 already committed the platform to location-transparent
gRPC; this ADR chooses the bridge mechanism that fits the existing
vmmd gRPC surface and the §11 auth model.

## Decision

**vmmd exposes an in-process HTTP frontend per instance via a new
ForwardHTTP RPC.** The flow:

1. gatewayd's hot path calls `Backend.Pick(appID)`, which returns one
   `Target{NodeID, InstanceID, WakeID, AddedAt}` from the per-app
   `targetSet` via atomic round-robin. The string type is preserved
   for backwards compat with pre-#98 tests; the SEMANTIC changed from
   `host_ip:8080` to `compute_node.id`. Pre-#168 the cache was a
   `map[app_id]string` — one node id per app. Post-#168 the cache is a
   per-app set of Targets (size ≤ `max_concurrency`), supporting fan-out
   across the plan's effective concurrency cap. The picker uses an
   atomic round-robin cursor so the hot path is allocation-free even
   with multiple instances in the set.
2. gatewayd dispatches through `proxyByNode(addr)` (a new
   `Handler` field installed by `WithForwarding(fn)`). The
   factory returns an `http.Handler` for the node.
3. `pkg/gateway.ForwardingReverseProxy` translates the inbound
   HTTP request into a `ForwardHTTPRequest`, dials the per-node
   vmmd over the overlay through `NodeClientCache`, and writes the
   `ForwardHTTPResponse` back to the inbound response writer.
4. vmmd's `pkg/vmmdgrpc.ForwardHTTP` handler builds a shell
   bridge script that runs `ip netns exec <netns> sh -c <script>`,
   uses `/dev/tcp/10.0.0.2/8080` for the inner dial, and rewrites
   the Host header to the inner identity before the bridge.
5. Hop-by-hop headers (RFC 7230 §6.1) are stripped on both sides;
   the bridge script reads the request from a tmpfile so the
   bridge's stdin/stdout fd doesn't collide with the response
   socket.

Per-node gRPC conn caching: `NodeClientCache` keeps one
`*grpc.ClientConn` per `compute_node.id` for the process lifetime.
The first request pays the dial cost; subsequent requests hit the
cached conn. A `pg_notify` channel `compute_node_changed`
(migration 00026) evicts on every mutation so an admin
UPDATE or a watchdog-driven `active=false` drops the cached conn
and the next request re-dials against the fresh row.

Heartbeat direction is **reverse**: schedd pings vmmd on a 30s
tick, not vmmd pushing. schedd is the admission authority and
shouldn't trust inbound traffic from a box it may have already
drained; outbound probing means schedd detects failure on its own
clock. The vmmd `Heartbeat` RPC exists for symmetry / future
fields; schedd doesn't inspect its response, only that it
returned without `Unavailable`. Staleness gate is 90s
(`DefaultHeartbeatConfig`).

Body cap is 25 MiB (`ForwardMaxBodyBytes` in
`pkg/vmmdgrpc/forward.go`), matching spec §13 / `api.Limits.
HTTPRequestMax`. The cap is enforced inside the vmmd handler
*before* the bridge script runs so a 100 MiB body never reaches
the guest.

## Why this and not the other two bridges

**Vs vsock (option iii):** Clean in principle, but vsock has a
long-standing CI flake on lima/ubuntu and would require vmmd to
also hold the vsock listener, expanding the per-VM resource
budget. We carry enough bridges already.

**Vs a pure gRPC stream (option ii):** One new streaming RPC,
couples gatewayd↔vmmd at L7, adds a streaming-while-cancellation
protocol that has no precedent in this codebase. Heavier than the
slice needs to be.

**Chosen — gRPC-bridged ForwardHTTP:** Mirrors the existing
`wire.ListenAs` / `wire.Dial` pattern; every dial leg already
goes through `pkg/wire.DialContext`. The forwarder is a
`pkg/vmmdgrpc.ForwardHTTP` handler plus a `pkg/gateway.
ForwardingReverseProxy`. Overlay reachability is an OS-level
concern (Tailscale/Wireguard) that gatewayd dials through normally.
This is option (i) with the bridge implemented as in-process HTTP
inside vmmd, not socat.

## Consequences

- **Auth model unchanged.** mTLS from issue #95 covers the box-to-
  box gRPC leg; the bridge script inside vmmd runs as vmmd's own
  uid with full netns visibility (the only component that touches
  netns/jailer, CLAUDE.md ownership).
- **Cold-boot vs snapshot unchanged.** ADR-005 still pins snapshots
  to `firecracker.fc_version`; cold boot always works.
- **Per-instance resource budget unchanged.** No new per-VM
  resource is consumed by the bridge — vmmd is the only writer
  to `pkg/fcvm.Manager`, and the bridge runs on the vmmd host
  outside any jailer.
- **Wire format change.** `WakeResponse.addr` was deleted; the
  string field is now `node_id` (a UUID). PR #199 / pkg/scheddgrpc
  made the swap atomic, no shim. Replays of old clients fail at
  unmarshal time, which is the right signal.
- **Issue #168 fan-out:** `Backend.Pick` now returns a `Target` per
  pick (round-robin across the per-app set). The handler stamps
  `x-faas-instance` on the request BEFORE handing it to the forwarder
  so the bridge at vmmd can attribute inbound HTTP bytes to the
  specific instance the picker chose. Inbound `x-faas-instance` is
  overwritten — an attacker can't steer the proxy by setting the
  header (issue #168 trust model). `Backend.Admit(ctx, appID,
  maxConcurrency)` is the scale-out admission primitive; it
  atomically checks `HealthyCount < maxConcurrency` before the gRPC
  round-trip to schedd, so concurrent callers cannot collectively
  over-admit past the cap. At-capacity refusals surface as
  `atCapacity=true` (a typed result, no gRPC status).
- **Per-instance `last_request_at` (issue #168):** the gateway's
  `LastSeenSink.Touch(instanceID, t)` is keyed by instance id
  directly, not by `node_id:port`. The handler no longer needs the
  `nodeInstance` addr→instance resolver hop that was load-bearing in
  M5; the flush sink is `schedFlushSink` and ships `Touch{InstanceID}`
  directly to schedd. Per-instance attribution survives the
  multi-instance fan-out where multiple instances share one node.
- **Operational surface grows.** `compute_nodes` is operator-
  intent; the apid admin surface (ADR-029) gives operators CRUD
  on those rows. The synthetic `default-local` row stays
  untouched by the admin API (hard-delete refused with HTTP 409
  `default_local_protected`).
- **Overlay provisioning.** A new ansible role `deploy/ansible/
  roles/overlay/` installs Tailscale (default) or Wireguard
  (stub). Operators own authkey / peer exchange; the role consumes
  vaulted secrets and renders systemd units.

## Risks

- **Per-node gRPC conn cache across many nodes:** unbounded on a
  large fleet. Today the cache is bounded by the number of
  compute_nodes rows in Postgres; a 1k-node fleet would cache 1k
  conns. Each is lazy-dialed, so an idle fleet doesn't hold them;
  eviction on `compute_node_changed` frees drained rows. We don't
  bound the cache explicitly because the placement algorithm and
  the postgres partial index already cap concurrent admissions
  per node.
- **Bridge script failure surface:** the script runs `ip netns
  exec <netns> sh -c <script>`. A bug in the script template could
  surface as a 502 on the inbound request. The handler logs the
  raw stderr; we don't propagate it to the client because it
  contains paths inside vmmd.
- **Heartbeat-over-overlay latency:** on a 30s tick + 90s
  staleness, a tailnet partition of 60s is invisible to the
  watchdog. Acceptable for v1.0; tighter alerting comes from
  Prometheus on `gateway_wake_latency_seconds`.

## Out of scope

- Multi-tenant gatewayd routing policies — gatewayd forwards to
  one node per instance today.
- Tailscale ACLs — operators own their tailnet ACLs.
- WireGuard mesh configuration automation — operators manage
  peer lists via Ansible vault.
- vmmd-as-both-gatewayd-and-vmmd on one node — collapsed topology
  is fine (default-local still works); no special-case code.

## Amendment (issue #686 / PR #750, 2026-08-08)

HTTP/2 between gatewayd and the bridge is **in scope** as of PR #750.
The original "HTTP/2 between gatewayd and the bridge — HTTP/1.1 keeps
the in-process forwarder tiny" carve-out was made before the gatewayd
split (ADR-070) introduced H2C on the public→internal hop, at which
point the inner leg became the only plaintext HTTP/1.1 hop in the
gatewayd → vmmd → guest chain. The asymmetry was load-bearing: a
gold-plated H2 hop on the wire that doesn't change the customer's
HTTP/1.1 boundary at the public listener. The reversal is justified
because:

1. **One plaintext hop is worse than zero.** With H2C on the
   public→internal hop (PR #713/#719), the inner leg was the last
   H1 step. Keeping it H1 cost a content-type sniff and a chunked-
   decoder path in the bridge for no upside.
2. **The bridge binary is on disk anyway.** ADR-028 already ships
   a `vmmd-stream-bridge` shell script; replacing it with a small
   Go binary that emits H2C on the inbound unix socket and H1 to
   the guest costs ~150 LOC and removes the H1 hop. PR #750 does
   exactly this.
3. **The customer-visible contract is unchanged.** The H1 envelope
   the bridge writes to the guest is byte-for-byte the v1 shell
   output — same `Transfer-Encoding: chunked`, same 8 KiB chunks,
   same Host header. Guest-side `net/http` sees the same request.
4. **Live rollback.** `FAAS_STREAM_BRIDGE_VERSION=v1` on vmmd
   reverts to the shell bridge without a deploy (the env var is
   consulted in `streamBridgeVersion` and routes the gRPC
   `ForwardHTTPStream` call to the v1 wire-up). The default flipped
   to `v2` in PR #750 after the metal-lima gate
   (`TestE2E_Streaming_Metal_H2CInnerLeg`) confirmed H2C end-to-end.

Artifacts:

- `cmd/vmmd-stream-bridge/main.go` — the H2C server + H1 client
  bridge binary; replaces the v1 shell script template.
- `pkg/vmmdgrpc/forward.go::forwardHTTPStreamV2` — the v2 wire-up
  (spawn, env vars, H2C client transport, response stream).
- `pkg/vmmdgrpc/forward_v2_test.go` — bridge-binary wire-shape
  unit test (H1 framing emitted to the guest).
- `cmd/e2e/streaming_metal_test.go::TestE2E_Streaming_Metal_H2CInnerLeg`
  — metal acceptance gate.

## Reference call sites

| Site                                                | Change                                |
|-----------------------------------------------------|---------------------------------------|
| `pkg/gateway/handler.go`                            | `proxyByNode` field + `WithForwarding` |
| `pkg/gateway/forwardproxy.go`                       | `ForwardingReverseProxy` + `NodeClientCache` |
| `pkg/vmmdgrpc/forward.go`                           | `ForwardHTTP` handler + bridge script |
| `pkg/sched/heartbeat.go`                            | `Heartbeat` goroutine + staleness gate |
| `pkg/overlay/overlay.go`                            | cross-box dial helper                  |
| `cmd/gatewayd/nodecache.go`                         | per-node cache + eviction subscriber  |
| `cmd/vmmd/register.go`                              | self-registration at startup          |
| `cmd/schedd/main.go`                                | heartbeat loop                        |
| `migrations/00026_compute_node_notify.sql`          | trigger for `compute_node_changed`     |
| `deploy/ansible/roles/overlay/`                     | Tailscale + Wireguard provisioning    |

## Acceptance

- `WakeResponse.node_id` on the wire; `addr` removed.
- gatewayd RoutingCache maps `appID → (node_id)`; eviction by
  node id on `compute_node_changed`.
- gatewayd dials the per-node vmmd over the overlay; reverse-
  proxy semantics unchanged.
- vmmd registers itself in `compute_nodes` on startup.
- vmmd heartbeats via ForwardHTTP-twin `Heartbeat` RPC.
- Watchdog marks a node `active = false` after 90s of missed
  pings.
- apid POST/GET/DELETE `/v1/compute-nodes` (admin-gated) live.
- `deploy/ansible/roles/overlay/` provisions Tailscale or
  Wireguard.
