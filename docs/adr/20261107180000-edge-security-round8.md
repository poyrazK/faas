# ADR-20261107180000: Edge security round 8 — direct entry's certificates, DNS steering and handshake floods

- Status: accepted
- Date: 2026-11-07
- Amends: ADR-20261103163954 (app hosts point straight at the nodes; §3
  certificates, §4 abuse and flood protection), ADR-20261102081618 (edgednsd),
  ADR-20261104221012 (round 7), ADR-20261023104734 (runtime roles)

## Context

A review of the direct-entry certificate path (#6555, #6564) and the edge
around it, with the pilot live in production (`front: none`,
`direct-pilot.gregale.dev`, edgednsd ordering `*.gregale.dev`), found:

1. **R8-1: compute nodes could write the certificate table.** Every compute
   node logs in as `faas_compute`, a `faas_runtime` member, and `faas_runtime`
   gets row DML on every new table by default. A compromised node could insert
   a rogue certificate's chain into `edge_tls_certificates` (for example with
   `ca = letsencrypt-staging`, which no edge serves). The Certificate
   Transparency watch treats any key in that table as edgednsd's, so it would
   stay silent about the very certificate it exists to catch: with no CAA
   records (ADR-20261103163954 as amended 2026-11-04), the watch is the only
   control against a node answering a CA's TLS-ALPN-01 challenge for the hosts
   it serves.
2. **R8-2: one inserted row stopped renewal fleet-wide.** Edges and edgednsd
   trusted a row's `names`, `not_after` and `key_kid`, and the live query
   returned only the newest row per set of names. A row with the real names,
   the current recipient and a far `not_after` but a key no edge can open made
   every edge keep its old certificate, made a restarted edge never ready, made
   edgednsd never renew, and silenced the renewal-overdue alert, which read the
   row's date. The outage came at the real certificate's expiry.
3. **R8-3: a newest row the edge cannot open hid older ones**, so during a
   recipient rotation a restarting edge could stay unready.
4. **R8-4: any compute node could point the app DNS records at itself.**
   edgednsd takes each node's drain flag from `compute_nodes`, which compute
   nodes write (they register themselves through one shared role). Draining
   every other node left the records on the attacker's node, which already
   holds the wildcard key: a man in the middle for all app traffic instead of
   its share.
5. **R8-5: the firewall backstop counted IPv6 per address** while the edge
   counts per /64, so one client with a /64 never met it and could fill its
   65,536-entry sets, past which no new source is limited by the kernel.
6. **R8-6: the wildcard on every compute node also covers the platform
   hosts.** The apps domain is the zone apex, so `*.gregale.dev` is valid for
   `api.`, `s3.` and `operations.gregale.dev`. Not decided here (below).
7. **R8-7: compute edges did not refuse Cloudflare's shared origin-pull CA.**
   Round 6 refused it on the control plane only. Staged by mistake (Cloudflare
   ships it as `origin-pull-ca.pem`, the same file name as the zone's own), it
   lets any Cloudflare zone pass as verified ingress.
8. **R8-8: one client address could block every new TLS handshake on a
   node.** The handshake gate (128 per CPU, 10 s each, full means reset) had no
   per-address share. One address could hold every slot with connections that
   never send a ClientHello, and a connection from Cloudflare's ranges held no
   per-address slot at all until its handshake settled.
9. **R8-9: one client could hold every forward to the control plane**
   (uploads, forwarded bucket reads, OTLP) with slow bodies: the 256-request
   cap had no per-address share.
10. **R8-10: edgednsd probed the address in `compute_nodes.target_url`**, which
    compute nodes can rewrite: a second way to pull peers out of DNS, and a
    fixed-path request the control plane would send anywhere on its network.
11. **R8-11: edgednsd's managed host names were not reserved app slugs**, so a
    customer could claim `direct-pilot` (or a later managed name) and serve
    their content on the platform's host.

## Decision

1. **Only the control plane writes `edge_tls_certificates`.** A contract
   migration and the postgres role's `runtime-roles.sql` revoke insert, update
   and delete from `faas_runtime` and grant insert and delete to
   `faas_shared`, the control plane's `faas` OS user that edgednsd runs as
   (or the schema owner before the runtime-role contract). Compute nodes keep
   read access. With the writes gone, the Transparency watch's "a key we
   stored" again means "a key edgednsd stored" (R8-1, R8-2).
2. **The edge trusts the certificate, not the row.** `edgecert.Served` takes
   the names it serves and the expiry it reports from the parsed leaf, refuses
   a leaf that is not valid now, and verifies a Let's Encrypt chain against the
   system roots. The live query returns every valid row, newest first (at most
   `api.EdgeCertificateLiveMax`), and the edge serves every one it can open,
   picking per name the one that lasts longest, so an unopenable newest row
   never hides an older good one (R8-2, R8-3).
3. **A registry drain alone cannot empty the records.** edgednsd honors the
   drains of nodes whose edges still pass their probes only while at least
   half of the passing nodes stay; otherwise it keeps those nodes this tick,
   logs it and sets `edge_dns_drain_refused_nodes`. A drained node that stops
   passing still goes on its probes, and an ordinary drain of one node in
   three is unaffected (R8-4). This bounds, rather than removes, what a
   writable registry can do: per-node database identities would remove it and
   are outside this record.
4. **edgednsd probes the manifest's private address**
   (`FAAS_EDGEDNS_NODE_PRIVATE_ADDRESSES`, rendered from each compute host's
   `address`), never `target_url`; a node without one stays unready (R8-10).
5. **The firewall counts an IPv6 source by its /64** in the public-entry
   rate and connection sets (`ip6 saddr and ffff:ffff:ffff:ffff::`), as the
   edge does (R8-5).
6. **Compute edges refuse Cloudflare's shared origin-pull CA**, in
   gatewayd-public at start and in the `gatewayd_public_edge_service` role
   before the restart (R8-7).
7. **Handshakes have a per-address share and a ClientHello deadline.** One
   address (IPv4, or IPv6 /64) holds at most
   `api.EdgeTLSHandshakesInFlightPerAddrMax` (32) handshakes in progress, an
   address in Cloudflare's ranges `EdgeTLSHandshakesInFlightPerProxyAddrMax`
   (64) — counted from accept, not after settling — and a connection must send
   its ClientHello within `EdgeTLSClientHelloTimeout` (3 s); the rest of the
   handshake keeps 10 s (R8-8).
8. **Forwards to the control plane have a per-address share**,
   `api.EdgeControlPlaneForwardsInFlightPerAddrMax` (16) of the 256 (R8-9).
9. **apid reserves the labels of edgednsd's managed hosts.** The manifest
   renders the one-label names under the apps domain into
   `FAAS_RESERVED_APP_SLUGS`; apid refuses them as new apps' names. Like the
   fixed reserved list, this binds allocation only: an app that already has
   the name keeps routing (R8-11).

## Not decided

- **R8-6, the wildcard on every compute node.** It covers every app and the
  platform hosts under the apex (`api.`, `s3.`, `operations.`). The options
  recorded in ADR-20261104221012 stand (per-node certificates, keyless signing
  from the control plane, short-lived certificates), joined by moving apps to
  their own domain (the open gregale.app / Public Suffix List decision), which
  would take the platform hosts out of the edges' certificate. Owner decision.
- **R8-12, vmmd identity.** schedd verifies vmmd's certificate for
  `vmmd.faas`, a name every node's certificate carries, at the address in
  `compute_nodes.target_url`, which compute nodes can rewrite. A compromised
  node could therefore receive another node's VM-control calls. Fixing it
  needs per-node identities signed on the control plane and a schedd check of
  the node name it meant to reach; that is a PKI change outside the edge and
  gets its own record.

## Consequences

- The contract migration runs in a full release; edgednsd on the control
  plane keeps writing (owner or `faas_shared`). An older edge reading the
  table is unaffected.
- The edgednsd role needs `faas_edgednsd_node_private_addresses`; the
  manifest renders it, and the role refuses to start edgednsd without it.
- Operators see `handshakes_per_address` and `forward_in_flight_per_address`
  in the edge's refusal counter, and `edge_dns_drain_refused_nodes` when a
  mass drain is held back.
- The nft expression form `ip6 saddr and <mask>` in a dynamic set key is
  checked by the host's `nft -c` at converge; this record was written without
  a Linux host to run it.
