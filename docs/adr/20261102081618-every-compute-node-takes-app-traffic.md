# ADR 20261102081618 · Every compute node takes app traffic straight from Cloudflare

- **Status:** proposed
- **Date:** 2026-11-02
- **Amends (when accepted):**
  - ADR-1343 §1, §3 and §6: no tunnel; each compute edge gets a public TLS
    listener, and withdrawing a node removes its DNS record;
  - ADR-20261030115139 §2: the app wildcard becomes proxied address records
    that a control-plane daemon keeps in step with the node registry;
  - ADR-20261031105055: the routing hint names every node an app runs on,
    and gatewayd-internal prefers its own node's instances.
- **Related:** ADR-070 (one public ingress per node), ADR-119 (static
  outbound IP), ADR-520 (custom domains stay on the control plane),
  ADR-20261026100000 (alias apps domains)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

Every app request enters through the cell's Cloudflare Tunnel (ADR-1343).
Cloudflare treats a tunnel's connectors as replicas, not as a pool, so one
connector takes almost all traffic:

- fsn-3's connector had served 310,559 requests and fsn-2's 935, as of
  2026-11-02;
- 60 of 60 test requests that day entered on fsn-3.

ADR-20261031105055 moved routing work to the app's node, but entry work
stays on fsn-3. At 300 req/s on 2026-11-02, the CPU per request was:

| Work | CPU per request | Runs on |
|---|---|---|
| cloudflared | 0.83 ms | fsn-3 only |
| Edge gatewayd-public | 0.97 ms | fsn-3 only |
| gatewayd-internal | 2.1 ms | the app's node |
| vmmd | 1.6 ms | the app's node |

So the cell's request rate is capped by one node's CPU, however many nodes
it has. The alternatives that keep the tunnel cost money or quota (see
Rejected alternatives). ADR-20261030115139 also set the rule that Cloudflare
is for safety only: TLS, the web firewall and DDoS protection. A tunnel is
more than that, and nothing else we run needs it.

A proxied record already points at a public origin without a tunnel:
`api.gregale.dev` points at the control plane. From Istanbul on 2026-11-02,
30 alternating requests on new connections measured, from TLS done to the
first byte:

- through the tunnel: median 197 ms;
- to the direct origin: median 192 ms.

## Decision

### 1. DNS spreads requests over every node

- `*.<apps domain>` becomes one proxied A record per ready compute node,
  each holding that node's public IPv4 address. AAAA records are added where
  nodes have public IPv6. Cloudflare picks one of the addresses for each
  connection to the origin.
- Alias apps domains (ADR-20261026100000) get the same record set.
- Platform hosts and custom domains keep their records (ADR-20261030115139
  §2, ADR-520).
- An explicit record still beats the wildcard. A pilot or a rollback can
  move one hostname, as before.
- **Retries.** Cloudflare retries a request once, on another address, when
  the connection or TLS setup fails (errors 521, 522, 523, 525, 526). It
  never retries after the request was sent, so POSTs stay safe. A refused
  connection is retried at once. A node that does not answer the TCP SYN is
  retried only after 19 s. Cloudflare's blog says this retry is the default
  on Pro, Business and Enterprise zones; the docs do not cover Free zones.
- **No health checks.** Cloudflare does not health-check the addresses
  behind a record. §3 removes a bad node's record instead.

### 2. gatewayd-public terminates TLS

gatewayd-public stays the only public listener on a node (ADR-070). Its
compute edge gains a TLS listener on port 443:

- **Certificate.** A Cloudflare Origin CA certificate for the apps domains,
  sealed like other node secrets. The zone runs SSL "Full (strict)".
- **Client certificate.** The listener requires the zone's Authenticated
  Origin Pulls certificate. This is a zone-level client certificate that we
  upload, not Cloudflare's shared one, so another Cloudflare zone cannot reach
  our nodes.
- **Client IP.** `CF-Connecting-IP` is trusted only on connections that
  presented that client certificate. This replaces the loopback trust in
  `FAAS_TRUSTED_INGRESS_CIDRS`.
- **Everything else stays as it is:** the host check (`421`), forwarding
  credential-bound routes to the control plane, placement routing, and the
  drain grace.
- **Firewall.** nftables admits port 443 only from Cloudflare's published
  address ranges. The list lives in the edge role's defaults, and a check
  compares it with Cloudflare's published list. Everything else on the public
  address is dropped.
- **GCP.** An external IP on the network interface would move all of the
  node's egress off Cloud NAT, tenant traffic included. On GCP the address is
  inbound-only: a forwarding rule sends it to a target instance. Bare-metal
  nodes have public addresses already, and ADR-119 governs their egress.

### 3. A control-plane daemon keeps the records in step

`edgednsd` runs on the control plane. It is a new daemon with no listener
other than its metrics port.

- **Token.** It holds a Cloudflare token limited to Zone DNS: Edit on the
  apps zones, sealed at rest. The token never reaches a compute node or an
  internet-facing process. That is why this is not part of gatewayd-public.
- **Desired records.** One record per compute node that is active in the
  registry, not drained, has a public address in the manifest, and whose edge
  answers `/readyz` on its control port. That is the same probe
  `faas-check-compute-edges` uses, sent over the private network every 2 s.
- **Withdraw.** A node's record is removed after 3 failed probes in a row,
  or as soon as the node is drained. It is restored after 5 passing probes in
  a row.
- **Fail static.** The last record is never removed. When `edgednsd` or the
  control plane is down, the records stay as they are and traffic keeps
  flowing; only failover stops.
- **Off the request path.** The daemon reads `compute_nodes` and calls the
  Cloudflare API only when a node's state changes.
- **Metrics:** `edge_dns_records{zone}`,
  `edge_dns_changes_total{zone,action}` and
  `edge_dns_probe_failures_total{node}`.

### 4. Drain and restart

ADR-1343 §6's first step, "stop the connector", becomes "remove the node's
record". On a drain, release or reboot:

1. `edgednsd` removes the record. A drained node is withdrawn without
   waiting for probes.
2. The edge keeps serving for the propagation window that step 0 measures,
   then closes its listener.
3. On new connections a closed port answers with a reset, so Cloudflare
   retries them on another address at once. Responses during the drain grace
   carry `Connection: close`, so Cloudflare's open connections move to other
   nodes as well.

A plain restart of the edge gets the same refused-then-retried behaviour.

### 5. An app that runs on several nodes

With random entry, most requests reach an app through another node's edge.
ADR-20261031105055 learns one node per host, and gatewayd-internal rotates
over every instance on every node, so an app with instances on two nodes:

- **bounces between nodes.** The hint follows whichever node answered last,
  and a request can cross twice: in at fsn-2, forwarded to fsn-3, whose
  gateway picks the instance on fsn-2.
- **splits its counters.** Each gateway counts only its own in-flight
  requests. So it asks for more instances late, and each gateway lets a VM
  take its full per-instance limit.

The fixes, which the switches below gate:

- **a. The answer names every node.** When the edge asked,
  gatewayd-internal adds `X-Faas-Instance-Nodes: <node>=<ready instances>,…`
  for the app, at most 16 nodes. It still stamps `X-Faas-Instance-Node` for
  N-1 edges.
- **b. The edge spreads across them.** The edge keeps that set per host
  (same 30 s TTL and 50,000-host bound) and picks a node per request in
  proportion to its count. A failed dial drops that node from the host's set.
- **c. Local first.** A gatewayd-internal reached by an edge (the request
  carried the placement mark) rotates over its own node's ready instances.
  It uses other nodes' instances only when it has none ready, or all of them
  are at their per-instance limit. Requests from the control plane's compute
  pool keep the global rotation.
- **d. Scale from the share.** For edge traffic, a gateway estimates the
  app's whole demand from its share:
  `desired = ⌈in-flight here ÷ per-instance concurrency × ready instances
  in total ÷ ready instances here⌉`. The arrival-rate target is scaled by the
  same factor. A gateway with no local instances keeps today's formula.
  schedd still caps the total (invariant 1).

Together these mean each VM takes edge traffic through its own node's
gateway, so the per-instance limit holds except while a node overflows. A
request makes at most one cross-node hop, and scale-out sees all of the
demand.

- **Switches.** `FAAS_EDGE_PLACEMENT_NODE_SET` on the edge turns on b.
  `FAAS_GATEWAY_EDGE_LOCAL_FIRST` on gatewayd-internal turns on a, c and d.
  Turn on the edge's first: with c alone, the hint would settle on one node
  and starve the others.
- **Independent of the rest.** These changes do not depend on §1–§4, and they
  matter under the tunnel too, once an app spans nodes. On 2026-11-02
  production had 4 running instances for 4 apps, so no app spanned nodes yet.

### 6. Switches and manifest

- `public_edge.entry: tunnel | both | direct`, default `tunnel`.
  - `both` runs cloudflared and the TLS listener side by side, for the
    migration and its rollback.
  - `direct` removes cloudflared.
- `compute_nodes[].public_ipv4` and `public_ipv6` give the addresses.
- The edge's TLS listener has its own switch, `FAAS_EDGE_PUBLIC_TLS_ADDR`,
  unset by default; the role sets it from `entry`.
- `edgednsd` starts in dry run: it logs and exports the changes it would make.

## Rollout

0. **Live test, on the pilot.** The GCP project has no room for throwaway
   VMs, so the test runs at step 4 on the real nodes. Until step 5 only the
   pilot hostname takes the direct path; everything else still enters
   through the tunnel. So refusing or silencing one node's port 443 there
   affects only the pilot's traffic. The operator applies the records with
   their own token, and reads the zone's plan from the dashboard first. The
   test answers:
   - whether our zone's plan gets the retry;
   - how evenly Cloudflare spreads requests, seen from two client locations;
   - what a refused, a silent and a hanging origin each cost a client;
   - whether Cloudflare keeps choosing a silent origin;
   - how long a removed record keeps receiving requests;
   - the first-request latency after 5, 15 and 60 idle minutes, from a
     distant client;
   - the TLS CPU cost per request.

   If the zone does not get the retry, upgrading it to Pro, a flat monthly
   fee, is decided here.
1. **The edge's TLS listener**, client certificate check and client-IP rule
   (§2), off by default.
2. **Ansible and the manifest** (§6): addresses, nftables, certificate
   sealing, `entry: both`.
3. **`edgednsd`** in dry run, then live for the pilot name only.
4. **Pilot.** One app hostname gets explicit records to every node, and
   step 0 runs against it. Each failure injected there is removed as soon as
   it is measured.
5. **Wildcard.** The script swaps `*.<apps domain>` from the tunnel CNAME
   to the managed records. Rollback is the CNAME again; the tunnel is still
   running under `both`.
6. **One release later**, `entry: direct`: remove cloudflared, the tunnel
   and its token, and update the drain runbook.
7. **§5**, in parallel with 1–6. The edge part goes first, then the
   gateway part.

## Consequences

- **Entry work spreads.** Each node does about 1 ms of entry work per
  request it receives, and the work grows with the node count. cloudflared's
  0.83 ms per request and its 38–45 MB of RAM per node go away. TLS on the
  node costs an estimated 0.05–0.1 ms per request on open connections, which
  step 0 measures.
- **Latency stays the same in steady state.** The medians above were within
  5 ms of each other. The risk is cold connections: a Cloudflare location
  that has no open connection to a node pays a TCP and TLS handshake to it,
  about 250 ms from Istanbul. The tunnel never pays this.
- **Hops.** With N nodes, (N−1)/N of requests take one private hop
  (0.34 ms between fsn-2 and fsn-3) to the app's node. Today the share
  depends on how many apps live off fsn-3; it was 67% in the 2026-11-02 load
  test. On GCP, traffic between zones is billed.
- **Compute nodes get public addresses.** Port 443 is open only to
  Cloudflare, and only with our client certificate. A flood aimed at a leaked
  address still reaches the node's link, which Cloudflare cannot absorb.
- **Failover is ours.** Cloudflare tracks a tunnel's connector health
  itself. Here `edgednsd` does, within about 6 s plus propagation. Until a
  silent node's record is gone, requests that land on it wait 19 s before
  the retry. With `edgednsd` down, failover stops but traffic does not.
- **Cloudflare becomes swappable.** The nodes are plain HTTPS origins, so
  another proxy could stand in front of them.
- **Records.** One per node per apps zone, within the DNS quota of any plan.
- **Capacity.** ADR-1343 §4's CPU hold-back stays until the pilot measures
  the edge's new cost.

## Rejected alternatives

- **One DNS record per app, pointing at its node's tunnel.** A request would
  enter on the app's node. But a zone holds 200 records on Free (1,000 for
  zones created before September 2024) and 3,500 on Pro and Business, and a
  record per app exceeds that. It also needs a failover controller.
- **A tunnel per node behind Cloudflare Load Balancing.** It has health
  checks and steering, but it is a paid, Cloudflare-only feature, against
  ADR-20261030115139.
- **Keep one entry node.** It is the cheapest, but the cell's ceiling stays
  one node's CPU. It is what we run until step 5.
- **Unproxied round-robin DNS.** Clients would reach nodes directly, with
  no DDoS protection or web firewall.
- **Caddy in front of gatewayd-public.** It adds a hop and a second public
  listener, as ADR-1343 found. gatewayd-public's listener is enough for one
  certificate and one client-certificate check.
- **TLS on the control plane's Caddy, forwarding to the nodes.** That puts
  the control plane back on the request path.

## Open questions

- The zone's plan and SSL mode, read from the dashboard before step 2.
- Whether Cloudflare's per-hostname client certificates are needed, or the
  zone-level certificate is enough while the apps zones hold only app
  hosts.
