# ADR-20261117185346 · Agent egress gateway: own addresses on the tenant gateway host, the politeness proxy, and per-session connection logs

- **Status:** proposed (owner decisions, 2026-11-17: "same host, own addresses", "all agent-pool egress", "phased PRs")
- **Date:** 2026-11-17
- **Amends (when accepted):**
  - ADR-20261027090014 §4.4: the agent gateway shares the tenant gateway's host and keeps its own addresses and tunnel; it carries every agent VM's egress (sandbox sessions in every mode with egress), not only the browser tier's.
  - ADR-20261112121456 §1: sandbox sessions leave the tenant gateway path once this ships.
- **Builds on:** ADR-372 (tenant egress gateway), ADR-361 (default-deny tenant egress and connection rates), ADR-373 (DNS-gated egress), ADR-371 (flow log), ADR-20261117091026 (sandbox egress domain policy and reached-domain log).

## Context

Sandbox sessions with egress leave through the ADR-372 tenant gateway, on the same public addresses as customer apps (ADR-20261112121456 §1). A site that blocks an agent's address blocks customer apps with it, and an abuse report against that address cannot be told apart from app traffic until the flow log is read.

ADR-20261027090014 §4.4 decided a dedicated agent gateway and address pool, a politeness proxy that reads the destination without decrypting, and a log that maps every connection to one run. None of it exists. Today:

- One WireGuard tunnel (`wg-tenant`) per fleet. Each node routes every guest on its tenant bridge into it by source address, and masquerades the traffic, so the gateway sees only the node.
- The gateway host runs nftables and WireGuard only: no daemon, no proxy, no log.
- The agent pool is a schedd list (`FAAS_SCHEDD_AGENT_RUN_NODES`). It keeps agent runs and sessions on those nodes, but customer apps can run there too.

## Decision

### 1. Own addresses and tunnel, on the tenant gateway host

- The tenant gateway host gains a second WireGuard interface, `wg-agent`, with its own tunnel range and its own public addresses. Agent traffic is source-NATed only to agent addresses, and app traffic only to app addresses. Neither ever carries the other's traffic.
- The addresses come from a provider whose terms allow multi-tenant egress. They are never rotated to get around a block.
- Manifest: `egress.agent_gateway` with `endpoint`, `public_key`, `tunnel_cidr`, `node_prefix`, `addresses` and `proxy` (puts the §4 proxy in the path). It may name the tenant gateway's `ssh_host`. The ansible roles that set up `wg-tenant` gain the agent interface beside it.
- One host is one failure domain: when it is down, apps and agents lose egress together. The owner accepted that to stand the gateway up sooner. A dedicated host later is a manifest change, not a design change.

### 2. Which traffic

- **Every agent VM with egress**, wherever it is placed: sandbox sessions in every network mode that has egress (`registries` and `web`). Agent runs have no network device today (they reach their model over the vsock relay); one that gains egress joins this path. Customer apps keep `wg-tenant`, even on the same node.
- Steering is per VM, not per node. vmmd marks an agent VM's traffic on the host (the ADR-119 static-egress fwmark pattern), and a policy-routing rule sends marked traffic to the `wg-agent` table.
- **Per-VM source addresses.** WireGuard routes a reply by its destination, and guests' host-side addresses repeat across nodes. So each node gets a slice of the agent tunnel range (`node_prefix`, by default a /22), and vmmd source-NATs each agent VM to its own address in the slice for the VM's life. The gateway then sees one address per VM.

### 3. Fails closed

- A node accepts an agent VM's traffic only out of `wg-agent`. A node without the interface refuses agent VMs that have egress. It never falls back to `wg-tenant` or the node's address.
- Before an agent VM with egress boots, vmmd checks the tunnel's latest handshake (younger than 180 s) and registers the VM's address with the proxy (§4). If either fails, the boot is refused with a retryable error and agentd requeues the session.
- Rollout switch: `FAAS_AGENT_EGRESS_GATEWAY` (vmmd). While it is off, agent VMs keep today's tenant path.

### 4. The politeness proxy

A new daemon, `agentproxyd`, runs on the gateway host. It is a transparent TCP proxy for agent traffic on ports 80 and 443, which nftables redirects to it. It ships in the signed release and is installed on the gateway host by its ansible role, as on a database node.

- **One address per VM.** The proxy dials out from the VM's agent address, chosen from the low bits of the VM's tunnel address. The gateway's source NAT makes the same choice for the traffic it forwards, so a VM leaves from one address whatever the port.

- **Destination.** It reads the destination from the TLS ClientHello's SNI, or from the plain-HTTP `Host` header. It never decrypts. A TLS connection without SNI, or HTTP without `Host`, is refused.
- **Identity.** vmmd registers each agent VM's address with the proxy at boot: account, session or run, node, and an expiry. It does so over the tunnel, so the WireGuard peer authenticates the node, and the proxy accepts a registration only for addresses in that node's slice. vmmd removes the registration when the VM is destroyed. A connection from an unregistered address is refused.
- **Concurrent-connection caps.** These are held in the proxy's memory. One gateway sees all agent traffic, so no shared store is needed yet:
  - per VM (`AgentEgressConnsPerVM`);
  - per account per destination registrable domain (`AgentEgressConnsPerAccountDomain`);
  - per destination registrable domain fleet-wide (`AgentEgressConnsPerDomainFleet`).

  A connection over a cap is refused at once, never queued.
- **Blocklist.** A list of registrable domains fed by complaints, synced to the gateway host by ansible from the platform repository. A listed domain is refused for every agent.
- **Unchanged:** the ADR-361 deny ranges, the ports, DNS gating and per-VM connection rates on the node, and the session's domain policy (ADR-20261117091026). The proxy adds caps; it relaxes nothing.

### 5. The connection log

- The proxy logs every connection with: node, account, session or run, source address, destination name and address, the port, bytes each way, duration, and why it ended or was refused.
- It writes one JSON line per connection, message `agent_egress_connection`, to its own journal namespace on the gateway host (`LogNamespace=agentproxyd`). The gateway host gets no database access.
- *Amended in PR 3:* the draft said the log shipper already carries the journal off the host. It does not: the gateway host is outside the private network and runs no shipper, so the log stays on the gateway. Its namespace is persistent and kept 30 days (the ADR-371 flow log's retention), apart from the host journal's 7-day bound. Shipping it off the host needs a path to Loki and a logs leaf for the gateway; the amendment of 2026-11-19 below decides both.
- So an abuse report naming a public address and a time maps to one session within minutes: the proxy log gives the session directly, and vmmd's flow log (ADR-371) and the session's domain log (ADR-20261117091026 §4) confirm it.

### Limits (`pkg/api/limits.go`)

| Limit | Value |
|---|---:|
| `AgentEgressConnsPerVM` | 256 concurrent |
| `AgentEgressConnsPerAccountDomain` | 64 concurrent |
| `AgentEgressConnsPerDomainFleet` | 512 concurrent |
| `AgentEgressHandshakeMaxAgeSec` | 180 |

These are starting values. A headless browser opens about six connections per host across a few dozen hosts. Revisit them against the proxy's refusal counts after the first week.

### Metrics (§12)

- `agent_egress_connections_total{outcome}`: the proxy's accepted connections, and its refusals by reason (`cap_vm`, `cap_account_domain`, `cap_domain_fleet`, `blocklist`, `unregistered`, `no_sni`).
- `agent_egress_active_connections`.
- `agent_egress_registrations`.
- `agent_egress_gateway_handshake_age_seconds{node}`.

## Rollout

1. **PR 1:** the manifest stanza, ansible (`wg-agent` on the gateway host and the nodes), vmmd's per-VM marking and source NAT, the policy rules (mark → agent table, then unreachable), and the handshake check. Behind `FAAS_AGENT_EGRESS_GATEWAY` (vmmd). Without the proxy, the gateway forwards agent traffic directly.
2. **PR 2:** `agentproxyd`, with registration, caps, blocklist and metrics. vmmd registers VMs and refuses a boot it cannot register.
3. **PR 3:** the connection log, its retention on the gateway, and the runbook entry for abuse reports (`docs/runbooks/agent-egress-gateway.md`).
4. **Before the switch is on:** the owner provisions the agent addresses on the gateway host. A metal check covers a web session and a registries session through `wg-agent`, a refusal with the tunnel down, and each cap's refusal. Then one node, then the fleet.
5. **PR 4 (amendment 2026-11-19):** `wg-obs`, Alloy and the logs leaf on the gateway hosts, the connection log's 30-day Loki stream, the scrape jobs and alerts, and the runbook reading Loki. Behind the operator variable `faas_gateway_obs_enabled`; the owner moves the Loki bucket's lifecycle rule to 32 days.

## Amendment 2026-11-19: the gateways' logs and metrics leave the host

PR 3 left the connection log and `agentproxyd`'s metrics on the gateway: the
gateway host is outside the private network, so it runs no journal shipper,
holds no logs leaf, and Prometheus cannot reach it. An abuse report needs SSH
to the gateway and `journalctl`, and nothing alerts when the proxy dies or
starts refusing. The ADR-372 tenant gateway has the same gap: its firewall
unit, WireGuard and login records stay on it too. This amendment covers both
gateways, whether they share one host or not.

### A1. An observability tunnel, `wg-obs`

- Each gateway host gains a third WireGuard interface, `wg-obs`, with one
  peer: the control-plane host that runs Loki and Prometheus (the first
  `control_plane` host). The control plane is the only peer, the gateway the
  only thing on the other end.
- **The control plane dials out.** It has the gateway's public address as
  the endpoint and keeps the tunnel up with a 25 s keepalive. The gateway
  learns the control plane's address from the handshake. The control plane
  opens no inbound port to the internet, which matters on a control plane
  behind NAT.
- A small private range of its own (`faas_gateway_obs_tunnel_cidr`, default
  `10.255.254.0/29`): the control plane is `.1`, each gateway host `.2`
  upward. It must not overlap the tenant or agent tunnel, or the private
  network; the converge checks the addresses already on each host.
- **What crosses it.** Gateway → control plane: Loki's push port (3100),
  and nothing else. The control plane's firewall accepts 3100 on `wg-obs`
  from the tunnel range. Control plane → gateway: Alloy's metrics (9080) and
  `agentproxyd`'s metrics (9473), accepted by the gateway's own
  `inet faas_gateway_obs` table from the control plane's tunnel address only.
  The gateway forwards nothing into or out of `wg-obs`; the tenant and agent
  tables already drop tunnel traffic to anything but the public interface,
  so agent or tenant traffic can never reach the control plane this way.
- Keys are minted on each host and never leave it, as for the egress tunnels.
  The converge reads each host's public key and renders the peers.

### A2. Alloy and a logs leaf on the gateway

- The gateway runs Alloy (ADR-20261023185953), the same role and pipeline as
  a compute node, pushing to Loki with mTLS over `wg-obs`. Its leaf is the
  compute node's shipper leaf (`pkg/pki` `FeaturePlatformLogs`,
  `promtail/loki-client`), issued on the control plane into a per-host
  staging directory and copied in, exactly as for a compute node. The
  gateway never holds the CA. `pki_renew.yml` renews it with the rest.
- It ships the gateway units (WireGuard, the two firewall units,
  `faas-agentproxyd`) and the host security streams (ADR-20261107203000):
  a public, internet-facing host is where login and privilege records matter
  most.
- **The connection log** is read from `agentproxyd`'s own journal namespace
  as a separate stream, `job="agent-egress-connections"`. The stored line is
  the proxy's JSON. Only `outcome` (a closed set) becomes a label; account,
  sandbox, addresses and names stay in the line (ADR-143).
- **Retention.** Loki keeps that stream 30 days (`retention_stream`), the
  same as the gateway's journal namespace and the ADR-371 flow log; every
  other stream keeps the 14-day default. The bucket's lifecycle backstop
  moves from 16 to 32 days; until the owner changes it, Loki holds 16 days of
  the connection log and the gateway journal stays the record for older
  reports.
- **The gateway journal stays the source of truth.** Alloy is asynchronous
  and bounded, as on every host (ADR-143): a tunnel or Loki outage longer
  than its retries drops entries from Loki, never from the gateway's
  30-day namespace. The runbook reads Loki first and falls back to the
  gateway.

### A3. Metrics and alerts

- `agentproxyd` serves its metrics on the gateway's `wg-obs` address instead
  of loopback. The gateway sets `net.ipv4.ip_nonlocal_bind`, so the proxy
  and Alloy start whether or not `wg-obs` is up: the observability tunnel
  can never stop agent egress.
- Prometheus scrapes two jobs from file-based discovery that the converge
  writes on the control plane: `promtail-egress-gateway` (Alloy on each
  gateway host) and `agentproxyd`. The existing shipper alerts cover the
  gateway's Alloy by job.
- New alerts:
  - `FaasAgentProxyDown` (page): `agentproxyd` unscrapeable for 3 minutes.
    Agent web egress is refused while it is down (§3, fails closed).
  - `FaasEgressGatewayUnreachable` (warn): a gateway host's Alloy
    unscrapeable for 10 minutes — the host or `wg-obs` is down, and its logs
    are not leaving it.
  - `FaasAgentEgressRefusalSpike` (warn): refusals by `cap_*`, `blocklist`,
    `no_sni` or `bad_destination` above 20 % of connections, and at least
    1 per second, for 10 minutes.
  - `FaasAgentEgressUnregisteredRefusals` (warn): `unregistered` refusals
    above 0.2 per second for 10 minutes — registrations are not arriving.
- The release gates that require every Prometheus target up leave the two
  gateway jobs out: no release pipeline deploys the gateways, so a gateway
  must not block a platform release. Their alerts cover them.

### Rejected for this amendment

- **Joining the gateways to the fleet overlay.** The gateway faces hostile
  traffic; on the overlay a compromise of it could reach every node's
  private services. One peer and one port is the whole surface.
- **Pushing over the agent or tenant tunnel.** Their peers are the compute
  nodes, not the control plane, and both carry tenant-controlled traffic.
  Making the control plane a peer of either puts it on a tenant path.
- **A public, mTLS-only Loki endpoint.** It puts Loki on the internet for
  one or two hosts.
- **Prometheus remote-write from the gateway.** It needs Prometheus's
  write receiver open; scraping over the tunnel needs nothing new on the
  control plane but the file-discovery jobs.

## Rejected alternatives

- **A dedicated gateway host now.** That is the stronger isolation, and the owner chose speed. It remains a manifest change.
- **Routing by node.** Customer apps can run on agent-pool nodes, so routing a whole node would move app traffic onto agent addresses.
- **Masquerading on the node, as `wg-tenant` does.** The gateway would see only the node, and could not cap or log per session.
- **Decrypting to read URLs.** Gregale never decrypts agent traffic (ADR-20261027090014 §4.3). SNI and `Host` are enough for caps and logs.
- **A shared counter store.** One gateway sees all agent traffic. A second gateway brings this back as a decision.
