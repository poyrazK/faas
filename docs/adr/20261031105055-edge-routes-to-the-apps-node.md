# ADR 20261031105055 · The compute edge routes each app host to the node that runs it

- **Status:** proposed
- **Date:** 2026-10-31
- **Amends:** ADR-1343 §2 ("Node-local routing") and its §3 premise that
  "Cloudflare spreads requests over healthy connectors".
- **Related:** ADR-20261030115139 (Cloudflare without a Worker),
  ADR-20261022184531 (wake failover; the dispatch nonce this follows),
  ADR-20261030230157 (vmmd local data socket)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

Since the 2026-10-31 cutover, every app request arrives through the cell's
Cloudflare Tunnel. ADR-1343 §3 assumed Cloudflare spreads requests over the
connectors of a tunnel. It does not: it treats them as replicas for
availability. In production both connectors attach to the same Atlanta
locations, and Cloudflare sends almost every request to fsn-3's connector.
Of 151 test requests, fsn-2's connector took 1. When fsn-3's connector stopped,
fsn-2's took over: 750 of 750 requests succeeded.

ADR-1343 §2 then sends each request to the edge node's own gatewayd-internal.
That gateway routes it, and reaches the instance through the vmmd of whichever
node runs it. So all routing work happens on the connector's node.

The 300 req/s load test on 2026-10-31 measured, on fsn-3:

| Process | Cores | CPU per request |
|---|---|---|
| Edge gatewayd-public | 0.28 | ~0.9 ms |
| cloudflared | 0.21 | ~0.7 ms |
| gatewayd-internal | 0.60 | ~2.0 ms |
| vmmd | 0.32 | ~1.1 ms |

fsn-2's edge took none of it. Its VMs were reached across nodes from fsn-3's
gatewayd-internal. Adding compute nodes would not spread this work, because
the connector choice does not follow capacity.

## Decision

The compute edge sends each app host's requests to the gatewayd-internal of
the node that last served that host. It learns that node from a hint in the
response. Its own node remains the default.

1. **The edge asks for the hint.** A compute edge sets
   `X-Faas-Edge-Placement: 1` on every request it sends to a gatewayd-internal.
   Every gatewayd-public removes a client's copy first.
2. **gatewayd-internal answers with the node.** When the request asked for
   it, gatewayd-internal names the instance's node on the response it
   forwards: `X-Faas-Instance-Node: <compute_nodes.id>`. It moves the request
   header into the request context, as it does the dispatch nonce, so no later
   hop sees it. It drops a guest's copy of the response header.
3. **The edge remembers it.** The edge strips the response header and keeps
   host → node in a bounded in-memory map:
   - each entry lasts 30 s from its last response;
   - the map holds at most 50,000 hosts.
4. **The edge routes by it.** A request whose host has an entry goes to that
   node's gatewayd-internal over the private network, the same listener the
   compute pool dials. It uses the local path as today when:
   - the host has no entry;
   - the entry names the edge's own node;
   - the node is not in the pool's in-memory inventory, or is unready;
   - a wake-failover retry excludes it.

   A failed dial to the named node forgets the entry and uses the local path.
   The inventory is never read synchronously for this: the hint uses only
   what the pool already holds, so no control-plane read joins the request
   path.
5. **Any gatewayd-internal can serve any request.** The hint only saves work.
   A stale or wrong entry costs at most one extra hop, which gatewayd-internal
   already makes today.
6. **Switch.** `FAAS_EDGE_PLACEMENT_ROUTING`, off by default, like other
   data-path changes. With it off, the edge sends no placement header and
   routes exactly as before.
7. **Metric.** `gatewayd_public_edge_placement_total{result}` counts each
   request's route:
   - `remote`: routed to the named node;
   - `self`: the entry named this node;
   - `miss`: no entry;
   - `unusable`: the named node was absent, unready or excluded;
   - `failed`: the dial to the named node failed.

Units release independently. An N-1 gatewayd-internal ignores the request
header and never answers, so the edge keeps routing locally. An N-1 edge never
asks, so an N gatewayd-internal never stamps.

## Consequences

- **The routing work moves to the instance's node.** That covers
  gatewayd-internal and the vmmd hop, which becomes same-node and can use the
  local data socket (ADR-20261030230157). The connector's node keeps only the
  edge and cloudflared, about 1.6 ms of CPU per request in the test above.
- **The number of cross-node hops is unchanged at one**, for an instance on
  another node. It moves from gatewayd-internal → vmmd to edge →
  gatewayd-internal.
- **A host whose instances run on several nodes follows its latest
  response.** Load still spreads by placement across hosts, not by
  round-robin within one host.
- **Cold wakes are unchanged.** The first request of a parked app goes to the
  local gateway, which wakes it wherever schedd places it. The response names
  that node for the next requests.
- **The remote node's private listener carries public requests.** It already
  does when the compute pool is the fallback, and the nftables rules admit
  only the cell's private range.

## Rejected alternatives

- **Round-robin across every node's gatewayd-internal**, as the control
  plane's compute pool did. It spreads routing work but adds a second
  cross-node hop for any instance that is not on the chosen node.
- **One tunnel per node behind Cloudflare Load Balancing.** It is a paid
  Cloudflare feature, against ADR-20261030115139.
- **A placement view read from control-plane Postgres.** It puts the control
  plane back on the request path or adds a view to keep fresh. The hint is
  free, and is correct by the time it is used.
