# ADR 20261103163954 · App hosts point straight at the nodes; Cloudflare only answers DNS

- **Status:** proposed
- **Date:** 2026-11-03
- **Amends (when accepted):**
  - ADR-20261102081618 §1–§3, and its rejected alternative "Unproxied
    round-robin DNS": the app records become DNS-only; the edge presents a
    publicly trusted certificate, stops requiring Cloudflare's client
    certificate and takes the client address from the connection; the
    firewall admits everyone on port 443 of the entry address;
  - ADR-20261030115139: for app hosts, Cloudflare is authoritative DNS only.
    Platform hosts stay proxied.
- **Related:** ADR-070 (one public ingress per node), ADR-100 (the certmagic
  issuer), ADR-119 (static outbound IP), ADR-520 (custom domains),
  ADR-831 (edge WAF, bot challenge), ADR-1343 (the tunnel)

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
port 443:

- **Certificate.** A publicly trusted certificate for `*.<apps domain>`
  (§3), in place of the Cloudflare Origin CA certificate.
- **Optional client certificate.** The listener stops requiring
  Cloudflare's client certificate but keeps its CA and verifies one when
  given (`tls.VerifyClientCertIfGiven`). A proxied record therefore still
  works, which is how a host goes back behind Cloudflare under attack (§4).
  The pilot checks that no browser asks its user to pick a certificate.
- **Client address.** The connection's peer address is the client.
  `CF-Connecting-IP` and `X-Forwarded-For` are trusted only on a connection
  that presented Cloudflare's verified certificate, as today
  (`verifiedIngress` in `pkg/gateway/internal_proxy.go`); from anyone else
  they are removed. `FAAS_TRUSTED_INGRESS_CIDRS` lists no range a public
  client can come from.
- **Server name.** The handshake fails when the client sends no server
  name or one outside the app domains, before any HTTP work. Scans of the
  node addresses get no certificate. Today the listener serves its one
  certificate for any name.
- **Protocols.** TLS 1.2 or later. HTTP/2 and HTTP/1.1 through ALPN; behind
  Cloudflare the listener offered HTTP/1.1 only. HTTP/2 gets explicit
  limits rather than library defaults: at most 100 concurrent streams per
  connection and capped header-list and frame sizes. The stdlib's HTTP/2
  denial-of-service fixes (Rapid Reset, CONTINUATION floods) make Go patch
  releases urgent for this listener.
- **Port 80 stays closed.** Every apps domain today is under `.dev`, which
  browsers load over HTTPS only (the whole TLD is HSTS-preloaded), and the
  wildcard certificate uses DNS-01. The public server's plain listener
  accepts HTTP/2 without TLS and trusts the ingress ranges' forwarded
  headers (`cmd/gatewayd-public/main.go`), so it never faces the public.
  When custom domains need port 80, it gets a separate server that only
  redirects and answers ACME challenges.
- **HSTS** as `pkg/httpsec` already sets it.
- **Firewall.** The rendered ruleset admits port 443 from anywhere to the
  entry address only (`ip daddr`), and drops everything else on that
  address. SSH and the private platform ports are admitted only from
  private or operator ranges, never on the entry address. The renderer's
  current shape without source ranges, `tcp dport { 22,80,443 } accept`
  from anywhere (`pkg/netns/policy.go`), would put SSH on an address that
  is now in public DNS.
- **Entry address.** On OVH, entry uses an additional IP rather than the
  server's main address, so it can be moved, rotated or null-routed without
  losing management access. OVH's network firewall on that IP admits TCP
  443 only, ahead of the server. On GCP the forwarding-rule address already
  carries only 443.
- **Block list.** An operator-managed block list of address ranges is a
  named set in the rendered ruleset, written by vmmd (the only root
  daemon), with a timeout per entry. A rule added by hand does not survive:
  the host firewall's reload (`flush ruleset` in `pkg/netns/policy.go`)
  wiped one within a minute on 2026-11-03.

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
- **Key.** ECDSA P-256, whose handshake signatures cost the node far less
  CPU than RSA-2048 under a handshake flood. A new key at each renewal.
  Edges hold the key in memory only, never in a file.
- **Exposure.** Unlike the Origin CA certificate, which only Cloudflare
  trusted, this key is publicly trusted and sits on every compute node. A
  guest escape on any node exposes it for every app host until the next
  renewal. In-memory keys and per-renewal rotation bound that; the
  key-loss runbook revokes and reissues.
- **CAA.** The apps domain publishes `issue` and `issuewild` records naming
  `letsencrypt.org` with `accounturi` set to `edgednsd`'s ACME account
  (RFC 8657), so no other CA or account can issue for it. Today
  `docs/domains.md` mentions CAA only for customers' domains.
- **Transparency.** A watch on Certificate Transparency logs for the apps
  domain pages on any certificate `edgednsd` did not issue.
- **Expiry.** `edge_certificate_expiry_seconds{name}` on each edge, an alert
  at 14 days and a runbook, before the first host goes live.
- **Custom domains** stay off (ADR-520). Their certificates are an open
  question.

### 4. Abuse and flood protection is ours

What Cloudflare absorbed now reaches the node. The layers:

- **Network floods: the provider.** OVH includes network anti-DDoS on its
  servers. On GCP, which is test-only, Google's network absorbs some. This
  is the one layer we do not run.
- **Connection floods: the kernel.** The rendered ruleset limits new
  connections per second and open connections per source address on the
  entry address (nftables sets with timeouts), so a flood is dropped
  before the edge spends a TLS handshake on it. Public connections share
  the conntrack table with tenant NAT, and a flood that fills it breaks
  every guest's egress: `nf_conntrack_max` is sized for public entry, an
  alert fires as the table fills, and SYN cookies stay on. These limits
  sit well above the edge's.
- **Connection limits in the edge.** These are kept per node, in memory,
  with no Postgres:
  - open connections per client address (IPv4 /32, IPv6 /64);
  - new connections per second per client address;
  - TLS handshakes in progress per node, sized to the CPU, so a handshake
    flood cannot starve requests;
  - requests per second per client address, answered `429` with a problem
    body;
  - requests per second per hostname, checked before routing or a wake.
    Every node takes every app host, so a flood on one app reaches every
    edge; this ceiling answers that app `429` and keeps its neighbours
    served.
- **Unknown hosts do not cost a Postgres read per request.** A gateway
  cached only routes it found (`pkg/gateway/pgbackend.go`); concurrent
  requests shared one lookup only for the same name. So every request to a
  new random name under the wildcard was a read on the control plane's
  Postgres, and only Cloudflare's HTTP DDoS rules stood in front of that.
  Each gateway now remembers the store's "no such route" for a short time
  and answers it `404` from memory; every route invalidation forgets those
  answers, so a new app is never hidden. A read of a host the gateway has
  never seen route spends a per-gateway budget, and past it the request is
  answered `503` with `Retry-After` instead of read; hosts seen routing
  skip it (#6491). The compute edge's public-read check
  (`pkg/objectstorage`) gets the same budget.
- **Requests the edge forwards.** The compute edge sends object-storage
  uploads and public reads on to the control plane's private listener
  (`newControlPlaneForwarder` in `cmd/gatewayd-public/edge_mode.go`). The
  per-address limits apply before the forwarder, and it has its own cap on
  requests in flight, so direct clients cannot push more at the control
  plane than Cloudflare let through.
- **Slow clients.** The existing 10 s header timeout and 1 MiB header cap,
  plus an idle timeout and a minimum request-body rate. The public
  server's read timeout is the whole request envelope
  (`api.CustomerRequestEnvelopeTimeout`, minutes long), so the body rate is
  required before the first host goes live, not later.
- **Per-account limits** stay as they are.
- **Values.** The limits live in `pkg/api/limits.go`, and the pilot sets the
  starting values. They must allow carrier-grade NAT, where thousands of
  users share one address.
- **Visibility.** Cloudflare's analytics go with the proxy. Each edge
  exports `gatewayd_public_edge_refused_total{reason}` for every refusal by
  limit, the handshake rate and failures, requests for unknown hosts and
  sampled top source addresses. An "under attack" runbook covers reading
  them and the steps below.
- **Going back behind Cloudflare.** Two levels, each a runtime change
  rather than a release, rehearsed before the wildcard moves:
  - **One host.** `edgednsd` writes a proxied record for the attacked
    name. A record for the exact name wins over the wildcard, so the host
    is behind Cloudflare again within about the TTL. This works because
    the edge still verifies Cloudflare's certificate (§2) and Cloudflare's
    ranges still reach port 443.
  - **Every host, or the addresses themselves.** The wildcard goes back to
    proxied records and the firewall to Cloudflare's ranges
    (`front: cloudflare`), and entry moves to spare addresses: an attacker
    who already holds the node addresses ignores DNS.

Customers keep ADR-831's edge rules: WAF signatures, the bot challenge,
per-IP throttles and IP and geo rules. They run in the gateway before a VM
wakes and key on the client address, which is now the connection's own. We
give up Cloudflare's free managed rules, its HTTP DDoS rules and its cache,
and do not replace them. A customer who needs more can put their own CDN in
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
  records, the public certificate, the optional client certificate, the
  peer address as client, port 443 open on the entry address and the
  limits.
- `front: none` requires `public_edge.entry` to be `both` or `direct`.
- The limits key on the client address as §2 defines it. Under
  `front: cloudflare` that is `CF-Connecting-IP` from a connection that
  presented the client certificate, so the limits can be tested there first.

## Rollout

1. **Edge:** optional client certificate, peer address as client, the
   server-name check, HTTP/2 with its limits, the per-address and
   per-hostname limits, the cap on forwarded requests and the minimum body
   rate, all off by default. Gateways remember misses and budget reads of
   unseen hosts (#6491, on by default).
2. **Certificates:** issuance in `edgednsd` with an ECDSA key, the table,
   in-memory loading in the edge, the expiry alert, the CAA records and the
   Certificate Transparency watch. Let's Encrypt's staging CA first.
3. **Ansible and the manifest:** `front`, the firewall scoped to the entry
   address with SSH off it, the kernel connection limits, conntrack sizing
   and its alert, the block-list set, and the OVH entry address with its
   network firewall.
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
     measured;
   - a flood of random names under the pilot's domain, which must cause no
     Postgres reads;
   - that no browser prompts for a client certificate;
   - going back behind Cloudflare: a proxied record for the pilot host, and
     the time until clients reach Cloudflare.
5. **Wildcard.** `*.<apps domain>` moves to the DNS-only records once both
   levels of going back behind Cloudflare (§4) are rehearsed and the "under
   attack" runbook exists. Rollback is the proxied records
   (ADR-20261102081618) or the tunnel CNAME, both kept for one release.
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
  by §4. A distributed one is bounded by the nodes' capacity and the
  provider's network protection until an operator moves the host, or every
  host, back behind Cloudflare (§4). This is the main risk accepted.
- **A publicly trusted wildcard key on every node** (§3). A node
  compromise now yields a certificate browsers trust for every app host,
  where the Origin CA key it replaces was trusted only by Cloudflare.
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
- **Client-certificate prompt:** if a browser asks its user to pick a
  certificate because the listener requests one, Cloudflare instead reaches
  a second listener on its own port, which requires the certificate, and
  443 asks for none.
- **Port 80 for an apps domain outside an HSTS-preloaded TLD:** such an
  alias apps domain (ADR-20261026100000) needs the redirect-only server of
  §2 before it moves to DNS-only records.
