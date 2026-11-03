# ADR 20261103163954 · App hosts point straight at the nodes; Cloudflare only answers DNS

- **Status:** proposed
- **Date:** 2026-11-03
- **Amends (when accepted):**
  - ADR-20261102081618 §1–§3, and its rejected alternative "Unproxied
    round-robin DNS": the app records become DNS-only; the edge presents a
    publicly trusted certificate, stops requiring Cloudflare's client
    certificate and takes the client address from the connection; the
    firewall admits everyone on ports 80 and 443;
  - ADR-20261030115139: for app hosts, Cloudflare is authoritative DNS only.
    Platform hosts stay proxied.
- **Related:** ADR-070 (one public ingress per node), ADR-100 (the certmagic
  issuer), ADR-119 (static outbound IP), ADR-520 (custom domains),
  ADR-1343 (the tunnel)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

ADR-20261102081618 makes every compute node an origin behind proxied
records. Its pilot (2026-11-03, `h10-hello-node` plus ten throwaway hosts
with records to both nodes) showed that Cloudflare does not spread a proxied
record's addresses:

- **One node per Cloudflare data center.** Cloudflare picks one address per
  data center for the whole record set, and the hostname does not change the
  pick:
  - from Atlanta, 320 of 320 requests to the pilot host and 300 of 300 to the
    ten other hosts reached fsn-2;
  - from Sofia, 50 of 50 reached fsn-2;
  - London and Amsterdam reached fsn-3.
- **A refused connection is retried on the other address at no visible
  cost.** With fsn-2's port 443 refusing for 45 s, 449 of 449 requests from
  Atlanta succeeded on fsn-3, with a mean of 0.17 s against 0.16 s before.
  Atlanta went back to fsn-2 once the port reopened. This answers
  ADR-20261102081618's open question for a Free zone.

So with proxied records, entry work lands on whichever node each data center
picked. The ways to steer it inside Cloudflare are paid or billed per
request (Load Balancing, Workers; against ADR-20261030115139), or
Enterprise-only (the DNS override in Origin Rules). On 2026-11-03 the owner
also ruled out BGP anycast and every network or layer-4 load balancer. A
record per app exceeds the zone quota (ADR-20261102081618).

DNS-only records hand every address to the client. Cloudflare's
documentation says a DNS-only name returns all its A records and that each
client "would decide which IP address to send the request to". Across many
clients and resolvers, entry spreads over every node with no machine
choosing.

The cost is what the proxy did for us: TLS, the web firewall and DDoS
protection (ADR-20261030115139). This record moves each of those to us or to
the hosting provider.

## Decision

### 1. App hosts get DNS-only records to every node

- **Records.** `*.<apps domain>` and alias apps domains
  (ADR-20261026100000) get one DNS-only A record per ready compute node, and
  AAAA records where nodes have public IPv6.
- **TTL.** 60 s, the shortest Cloudflare allows below Enterprise.
- **Size.** At most 16 addresses per name, which keeps an answer with both A
  and AAAA records well under the 1,232-byte EDNS size resolvers use. A
  larger cell needs pools (open question).
- **Platform hosts stay proxied.** The apex, `api.`, `operations.` and `s3.`
  keep Cloudflare in front, so the control plane keeps its protection.
- **No mixing on a name.** When a name has both proxied and DNS-only
  records, Cloudflare treats all of them as proxied. A host moves whole.
- **`edgednsd` keeps the records in step** exactly as ADR-20261102081618 §3
  says (probes, withdraw after 3 failures, restore after 5 passes, never the
  last record), writing `proxied: false`.
- **Failover.** A withdrawn node keeps receiving new connections from
  clients that still hold the old answer, for about the TTL plus resolver
  caching. Browsers and most HTTP clients try the next address when a
  connection is refused or times out, so a dead node costs a connection
  attempt rather than a failed request. A node that accepts connections and
  then fails requests keeps getting them until its record is withdrawn.

### 2. The edge serves the public directly

gatewayd-public stays the only public listener on a node (ADR-070), now on
ports 443 and 80:

- **Certificate.** A publicly trusted certificate for `*.<apps domain>`
  (§3), in place of the Cloudflare Origin CA certificate.
- **No client certificate.** `FAAS_EDGE_PUBLIC_TLS_CLIENT_CA_FILE` becomes
  optional. Without it the listener asks for no client certificate.
- **Client address.** The connection's peer address is the client.
  `CF-Connecting-IP` and `X-Forwarded-For` sent by the client are removed
  and never trusted.
- **Protocols.** TLS 1.2 or later. HTTP/2 and HTTP/1.1 through ALPN; behind
  Cloudflare the listener offered HTTP/1.1 only.
- **Port 80** answers only a redirect to https, and later ACME HTTP-01
  challenges.
- **HSTS** as `pkg/httpsec` already sets it.
- **Firewall.** The rendered ruleset admits ports 80 and 443 on the public
  address from anywhere, and still drops everything else there. An
  operator-managed block list of address ranges is part of the rendered
  ruleset. A rule added by hand does not survive: the host firewall's reload
  (`flush ruleset` in `pkg/netns/policy.go`) wiped one within a minute on
  2026-11-03.

### 3. The control plane issues the certificates

- **Issuer.** `edgednsd` already holds the cell's only Cloudflare token
  (Zone DNS: Edit). It obtains the apps-domain wildcard certificates from
  Let's Encrypt with the DNS-01 challenge, through the certmagic issuer
  ADR-100 added, with a Cloudflare `libdns` provider beside the Hetzner one.
  It renews 30 days before expiry. The token still never reaches a compute
  node or an internet-facing process.
- **One issuer per cell.** Let's Encrypt issues at most 5 certificates for
  the same set of names in 7 days, with no override. Issuing on each node
  would cap a cell at about five nodes, and DNS-01 would put the token on
  internet-facing machines.
- **Storage.** `edgednsd` stores each certificate and its key sealed, in a
  new table only it writes. An edge loads the certificate at start and on a
  `pg_notify`, and keeps serving the one it has while Postgres is down. No
  request reads Postgres.
- **Expiry.** `edge_certificate_expiry_seconds{name}` on each edge, an alert
  at 14 days and a runbook, before the first host goes live.
- **Custom domains** stay off (ADR-520). Their certificates are an open
  question.

### 4. Abuse and flood protection is ours

What Cloudflare absorbed now reaches the node. The layers:

- **Network floods: the provider.** OVH includes network anti-DDoS on its
  servers. On GCP, which is test-only, Google's network absorbs some. This
  is the one layer we do not run.
- **Connection limits in the edge.** These are kept per node, in memory,
  with no Postgres:
  - open connections per client address (IPv4 /32, IPv6 /64);
  - new connections per second per client address;
  - TLS handshakes in progress per node, sized to the CPU, so a handshake
    flood cannot starve requests;
  - requests per second per client address, answered `429` with a problem
    body.
- **Slow clients.** The existing 10 s header timeout and 1 MiB header cap,
  plus an idle timeout and a minimum request-body rate.
- **Per-account limits** stay as they are.
- **Values.** The limits live in `pkg/api/limits.go`, and the pilot sets the
  starting values. They must allow carrier-grade NAT, where thousands of
  users share one address.
- **Visibility.** `gatewayd_public_edge_refused_total{reason}` counts every
  refusal by limit.

We give up Cloudflare's web application firewall, bot management and cache,
and do not replace them. A customer who needs them can put their own CDN in
front of a custom domain, once custom domains return.

### 5. Overload spill

A node above its share of the cell's entry load refuses a fraction of new
connections, and clients retry another address, as Cloudflare did in the
2026-11-03 test. The edge refuses only while a peer reports headroom over
the edges' existing peer connections, never when every node is full. It
also closes idle keep-alive connections now and then, so held connections
move too. This is a later step with its own switch, off by default.

### 6. Switches and manifest

- `public_edge.front: cloudflare | none`, default `cloudflare`, which is
  ADR-20261102081618 as written. `none` turns on §1–§4 together: DNS-only
  records, the public certificate, no client certificate, the peer address
  as client, the open firewall and the limits.
- `front: none` requires `public_edge.entry` to be `both` or `direct`.
- The limits key on the client address as §2 defines it. Under
  `front: cloudflare` that is `CF-Connecting-IP` from a connection that
  presented the client certificate, so the limits can be tested there first.

## Rollout

1. **Edge:** optional client certificate, peer address as client, HTTP/2 and
   the limits, all off by default.
2. **Certificates:** issuance in `edgednsd`, the table, loading in the edge
   and the expiry alert. Let's Encrypt's staging CA first.
3. **Ansible and the manifest:** `front`, the firewall and port 80.
4. **Pilot.** A new test host, not `h10-hello-node`, whose records are
   proxied. Its DNS-only records point at both nodes. It measures:
   - the spread, from several resolvers (1.1.1.1, 8.8.8.8, 9.9.9.9, the
     provider's) and clients (curl, Go, Chrome). Cloudflare's documentation
     does not say whether it rotates the order of answers, and some clients
     sort addresses rather than use the order received;
   - the TLS handshake CPU per new connection;
   - first-request latency from Istanbul, against the proxied path;
   - what a refused, a silent and a withdrawn node cost a browser and curl;
   - the limits under a synthetic flood from one address, removed as soon as
     measured.
5. **Wildcard.** `*.<apps domain>` moves to the DNS-only records. Rollback is
   the proxied records (ADR-20261102081618) or the tunnel CNAME, both kept
   for one release.
6. **Overload spill** (§5).

## Consequences

- **Entry spreads by client** instead of by Cloudflare data center. App work
  still runs on the app's node (ADR-20261102081618 §5).
- **Every client handshake is ours.** Cloudflare carried many users over a
  few long-lived connections. Now each client's TCP and TLS setup lands on a
  node, an estimated 0.2–0.5 ms of CPU per new connection, which the pilot
  measures. Keep-alive and HTTP/2 make that a per-connection cost, not a
  per-request one.
- **Latency.** Nearby users lose the Cloudflare hop. Distant users pay the
  TCP and TLS round trips to our data center, about 250 ms from Istanbul
  (2026-11-02), where Cloudflare finished them close to the user. Requests on
  an open connection do not pay it again.
- **Node addresses are public.** An HTTP flood from one address is bounded
  by §4. A distributed one is bounded only by the nodes' capacity and the
  provider's network protection. This is the main risk accepted.
- **No cache.** The app serves every static response itself.
- **Cloudflare becomes replaceable** by any DNS provider with an API.
  cloudflared and the tunnel go, as in ADR-20261102081618.
- **Wording.** `CLAUDE.md` and the spec describe gatewayd-public as an edge
  "behind the upstream Caddy/Cloudflare TLS boundary". That changes when
  this ships.

## Rejected alternatives

- **Proxied records** (ADR-20261102081618 as written). Entry is chosen per
  Cloudflare data center (Context).
- **Cloudflare Load Balancing or a Worker.** Paid, or billed per request
  (ADR-20261030115139).
- **The DNS override in Origin Rules.** Enterprise only.
- **BGP anycast with ECMP, a layer-4 balancer tier (IPVS, XDP) or a cloud
  network load balancer.** The owner ruled these out on 2026-11-03. Anycast
  also needs BGP from the provider, and a balancer is another tier to run.
- **One record per app.** It exceeds the zone quota (ADR-20261102081618).
- **A certificate issued by each node.** Let's Encrypt's limit of 5 per week
  for the same names, and the DNS token on internet-facing nodes.

## Open questions

- **Pools beyond 16 nodes:** several names per cell, or a subset per name
  that `edgednsd` rotates.
- **Custom domains:** HTTP-01 needs challenge state shared by every node,
  since any node can receive the challenge. The alternative is DNS-01
  through a delegated `_acme-challenge` CNAME into a zone `edgednsd`
  controls.
- **Answer order:** whether Cloudflare or common resolvers rotate it. The
  pilot measures it.
- **IPv6:** the GCP nodes have none.
