# ADR-1343 · App edge on compute nodes behind Cloudflare Tunnel

- **Status:** proposed
- **Date:** 2026-10-15
- **Decision:** every compute node runs the public app edge: a Cloudflare
  Tunnel connector (`cloudflared`) and a gatewayd-public that sends requests
  to its own node's gatewayd-internal over the Unix socket. The app wildcard
  `*.<apps domain>` moves to the tunnel; the platform hosts (apex, `api.`,
  `operations.`, `s3.`) and custom domains stay on the control plane.
  Requests to a running app then reach no control-plane process.
  Credential-bound paths on app hosts are forwarded to the control plane.
  The whole edge is off by default and is turned on per manifest.
- **Why:**
  - The scaling roadmap (2026-10-15) measured the 2-vCPU GCP control plane
    saturating at about 157 req/s with the edge on it, while the compute
    nodes sat 75–90% idle.
  - ADR-1344 §8 puts the OVH launch's app edge on the two compute nodes and
    defers the design to this ADR; ADR-1344 §9 assigns it to the HA work.
- **Consequences:** a new compute-node role, an edge mode in gatewayd-public,
  a CPU and RAM hold-back in admission, per-node monitoring, a two-origin
  Cloudflare setup, and a connector-withdrawal step before every drain.
- **Rejected alternatives:** dedicated edge nodes; a Cloudflare Load Balancer
  to per-node origins; Caddy on every compute node; ADR-083's DNS handoff.
- **Builds on:** ADR-070 (one public ingress per box, in front of the local
  gatewayd-internal), ADR-1344 (§2 edge, §8 launch topology, §9 ownership),
  ADR-698–702 (public-edge roster and withdrawal barrier, default off),
  ADR-1521 (per-gateway journal batching, scaling roadmap).
- **Amends (when accepted):** ADR-070 (a compute node's public ingress is the
  tunnel-fed gatewayd-public); ADR-520 (custom domains stay on the control
  plane's direct-ingress Caddy); CLAUDE.md "Component ownership" (gatewayd-public
  also runs on compute nodes; it is still the only public listener on a node,
  since `cloudflared` only dials out).

## Context

**Today** (main @ 03709ac42a):

- The control-plane play runs `gatewayd_public_service` and `public_edge`
  (Caddy); the compute play has no edge role (`deploy/ansible/bootstrap.yml`).
  Caddy proxies the apex and `*.domain` to gatewayd-public on
  `127.0.0.1:8080`.
- Three things stop gatewayd-public on a compute node:
  - `role_convergence` stops and masks it there
    (`deploy/ansible/vars/daemons.yml`);
  - the daemon registry pins it to `RoleControlPlane`
    (`pkg/daemonunitspec/registry.go`);
  - the binary refuses to start in the compute-only role
    (`cmd/gatewayd-public/main.go`).
- gatewayd-public reaches compute through the database-backed pool
  (`compute_gateway_pool.go`): a new TCP connection per request to
  `tcp://<node>:8080`, round robin, no app affinity, keep-alives off.
- On a compute node gatewayd-internal already serves the full mux on
  `/run/faas/gatewayd-internal.sock`, and any node's gatewayd-internal can
  forward to any node's vmmd (`cmd/gatewayd-internal/nodecache.go`). Routing
  to the local socket loses nothing.
- Control-plane dependencies of gatewayd-public:
  - `Requires=postgresql.service` and `Wants=faas-apid.service` in its unit;
  - apid's Auth socket and spans-writer socket (`/run/faas/auth.sock`,
    `/run/faas/otel_spans_writer.sock`), used by `/v1/otel/v1/traces`;
  - the public object-storage handler, which needs the provider credentials
    that only `s3_gateway_service` provisions on the control plane. Public
    reads and uploads are served on `<slug>.<apps domain>`, the hosts that
    move;
  - `control_plane_proxy.go` for platform hosts, githubd webhooks and the
    platform `/healthz`.
- Client IP: the unit trusts `X-Forwarded-For` from loopback only if it is a
  single value; Caddy rewrites it from `CF-Connecting-IP`. No Go code reads
  `CF-Connecting-IP`.
- Admission reserves RAM for host daemons (1,536 MB plus the 2,048 MB builder
  slot, `pkg/api/limits.go`) but no CPU; its comment says compute hosts do
  not run gatewayd-public.
- Prometheus scrapes gatewayd-public as one static target; alerts assume one
  edge.

## Decision

### 1. Placement

On every compute node with the edge enabled:

- **`cloudflared`** runs one replica of the cell's app tunnel. It only dials
  out to Cloudflare, so the node gains no public listener. Its ingress rules
  send `*.<apps domain>` to `http://127.0.0.1:8090` and return 404 for
  anything else. It runs in `faas-cp.slice`.
- **gatewayd-public** listens on `127.0.0.1:8090` (8080 is gatewayd-internal's
  TCP listener on compute nodes). Its control listener binds port 9092,
  which nftables already limits to the control plane. Its compute unit drops
  `Requires=postgresql.service` and `Wants=faas-apid.service`, and uses
  `/etc/faas/compute-db.env`.
- The compute unit is a separate optional unit, `faas-gatewayd-public-edge`
  (`OptionalRegistry`, compute-only), derived from the control-plane unit so
  the two cannot drift. A separate unit leaves the core registry,
  `role_convergence` and release activation untouched; the
  `gatewayd_public_edge_service` role installs it, or removes it when the
  edge is off. `PartOf=` and `Wants=` tie it to gatewayd-internal, so every
  compute release and node join restarts it on the active binary. The binary
  accepts the compute role only in edge mode.
- The control plane's gatewayd-public gains a second listener on its private
  address, port 8090, for the routes an edge forwards (§2). nftables admits
  the compute nodes there, and on each compute node admits its peers on 8080
  for the compute-pool fallback. Both rules exist only while the edge is on.

The control plane keeps Caddy and gatewayd-public for the platform hosts and
custom domains. Its gatewayd-public keeps the compute pool, because `api.`
routes logs, attach, debug, synthesize and dispatch to compute, and custom
domains still enter there.

### 2. Edge mode in gatewayd-public

`FAAS_PUBLIC_EDGE_MODE=compute` (unset on the control plane) turns on:

- **Node-local routing.** A hybrid dialer uses the local gatewayd-internal
  socket and falls back to the compute pool only while the local gateway is
  unready or draining. Readiness follows the hybrid dialer.
- **Host check.** Only `*.<apps domain>` is served. Platform hosts (per
  `apid.IsPlatformHost`, including IP literals) and unknown hosts get
  `421 Misdirected Request` with a metric. The DNS split keeps them away; the
  check is defence in depth, and the compute edge never becomes a path to
  apid.
- **Credential-bound routes go to the control plane.** Public object-storage
  reads, upload routes and `/v1/otel/v1/traces` are forwarded over the
  private network to the control plane's gatewayd-public. The route set comes
  from the same Go registration code as the mux, so the two cannot drift.
  Compute nodes never receive storage-provider credentials or apid sockets.
  `FAAS_OTEL_SPANS_WRITER_ENABLED=false` on compute edges.
- **Client IP.** When the peer is the local connector (loopback, trusted by
  `FAAS_TRUSTED_INGRESS_CIDRS=127.0.0.0/8,::1/128`), the client IP comes from
  `CF-Connecting-IP`, and client-supplied `X-Forwarded-For` is replaced, not
  appended. Without this, any client that sends its own `X-Forwarded-For`
  would collapse to `127.0.0.1` and break per-IP throttles, allowlists and
  geo checks.

### 3. DNS and Cloudflare

- One named tunnel per cell for the apps domain, with a connector on every
  compute node. Cloudflare spreads requests over healthy connectors, and a
  withdrawn or dead connector drops out.
- `*.<apps domain>` becomes a proxied CNAME to the tunnel. The apex, `api.`,
  `operations.` and `s3.` keep the control-plane origin. The split list is
  derived from `apid.IsPlatformHost` plus `s3.`, and checked in CI.
- The public-timeout Worker gets two origins: platform hosts to the control
  plane, the app wildcard to the tunnel.
- Tunnel credentials are sealed like other node secrets; each node holds the
  connector token, never an account API token.

### 4. Capacity

- **CPU:** node join subtracts an edge hold-back from the physical millicore
  budget it registers. The default is 1,000 millicores (one vCPU) for
  gatewayd-public and `cloudflared` together. The value is a limit in
  `pkg/api/limits.go`, sized from the cutover load test.
- **RAM:** the host-daemon reserve gains 256 MB for the two daemons.
- Both daemons run in `faas-cp.slice`, so the existing CPU quota caps them and
  they cannot starve tenant VMs.

### 5. Operability

- apid's HTTP service discovery lists each enabled compute edge's
  gatewayd-public control target and `cloudflared` metrics endpoint.
- Alerts become per instance: daemon down or not ready, connector
  disconnected, local-gateway fallback rate, `421` rate.
- A per-node check (`make check-compute-edges`,
  `deploy/scripts/faas-check-compute-edges.sh`) probes each edge from the
  control plane: gatewayd-public's `/readyz` on its control port and the
  connector's `/ready` on its metrics port. The edge's own listener is
  loopback-only, and a public request cannot pick its connector, so
  `check_public_endpoint` alone cannot see one bad node.
- `/v1/traces` lookups are per edge process: a lookup sees only spans from
  the edge that served the request. This is accepted; the spans pipeline is
  the durable record.

### 6. Drain and withdrawal

Before a compute node is drained, upgraded or rebooted:

1. stop its connector, so Cloudflare sends new requests elsewhere;
2. let gatewayd-public drain: new admissions close and existing HTTP,
   WebSocket and SSE streams finish within the drain grace (25 s today);
3. then drain the node as today.

Two mechanisms carry this out without an operator step:

- **Drain.** A node drained in the registry withdraws its edge's local path.
  The edge keeps answering tunnel traffic and sends every request to the
  other active nodes. That is a planned route, so it does not count as a
  local-gateway fallback (`gatewayd_public_edge_node_drained` is 1 instead).
  The edge withdraws only while the registry is readable and another node is
  active: a drain must not move traffic to nowhere.
- **Stop, restart or reboot.** `faas-cloudflared` is ordered after the edge
  unit, so it stops first. On SIGTERM it withdraws from the tunnel and serves
  in-flight requests for its grace period (30 s), through the edge that is
  still running. Its stop timeout (45 s) outwaits that.

For planned maintenance that must also end long-lived streams before the
node goes down, stop `faas-cloudflared` on the node first.

The steps run inside ADR-1344 P4's lossless drain. When the runtime-upgrade
public-edge protocol (ADR-698–702) is on, each compute gatewayd-public is a
roster member and step 2 is ADR-701's withdrawal fence. Binding that
protocol's identity probe to a tunnel-fed listener instead of Caddy (ADR-702)
is follow-up work; the protocol stays off until then.

### 7. Out of scope

- **Custom domains (ADR-520, off today).** They keep the control plane's
  direct-ingress Caddy with on-demand TLS. A tunnel cannot carry hostnames
  Cloudflare does not proxy, and ADR-520 rejects Cloudflare for SaaS. Serving
  them from every node needs shared certificate storage and a later ADR.
- **ADR-083's DNS handoff** stays off. The tunnel is active-active.
- **Raw TCP/UDP ingress** (off today) cannot ride an HTTP tunnel.

## Rollout

Every step is dark until the manifest turns it on:

1. gatewayd-public edge mode with tests (routing, host check, forwarding,
   client IP).
2. The compute role (`cloudflared`, the gatewayd-public compute unit),
   registry changes, nftables, the tunnel credential, behind manifest
   `public_edge.compute_nodes` (default off). The operator stages the cell's
   tunnel token as a root-only file; the connector reads it through
   `LoadCredential=` and stays stopped until it exists.
3. The CPU and RAM hold-back, applied only where the edge is enabled.
4. Per-node discovery, alerts and `check_public_endpoint`.
5. Connector withdrawal in the drain path (with ADR-1344 P4).
6. Cutover: pilot on a test hostname, then move `*.<apps domain>` at low
   traffic. WebSocket and SSE connections drop when DNS flips. Keep the
   control-plane edge serving the wildcard until the pilot passes.

The OVH launch (ADR-1344 §8) turns it on from day one. Whether the GCP beta
moves first is decided after the pilot.

**Exit criterion:** at the cutover load test, an app request reaches no
control-plane process (verified with `pg_stat_statements` and the control
plane's request metrics), the control-plane API stays within its SLO, and
draining one compute node fails no request.

## Consequences

- Each compute edge adds three pooled Postgres connections plus a LISTEN
  session to the control plane's Postgres until PgBouncer (scaling roadmap
  Stage 1) is on.
- ADR-1521's journal batches come from each compute node's gateway; nothing
  changes in that design.
- More code runs on hosts that run tenant VMs. The edge holds no storage
  credentials, apid sockets or account API tokens, and serves only app hosts.
- Requests stay node-local unless the local gateway is down. The node that
  receives a request is chosen by Cloudflare, not by app placement; app
  affinity remains a scaling-roadmap topic.

## Rejected alternatives

- **Dedicated edge nodes.** They cost extra hosts at launch scale; compute
  nodes have idle CPU. Revisit if edge CPU competes with tenants.
- **A Cloudflare Load Balancer to per-node origins** (ADR-1344's earlier
  design). It needs a public listener and origin certificates on every node.
  A tunnel needs neither.
- **Caddy on every compute node.** Its `remote_ip` matcher would reject the
  loopback connector, and it adds a second hop and a config to keep in step.
- **ADR-083's DNS handoff.** Several edges would fight over one record.

## Open questions

- Cloudflare Tunnel limits on our plan: connectors per tunnel, request body
  size (uploads), and WebSocket idle timeouts.
- The edge CPU hold-back value, from the cutover load test.
