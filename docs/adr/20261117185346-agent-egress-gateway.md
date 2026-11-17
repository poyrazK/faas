# ADR-20261117185346 · Agent egress gateway: own addresses on the tenant gateway host, the politeness proxy, and per-session connection logs

- **Status:** proposed (owner decisions, 2026-11-17: "same host, own addresses", "all agent-pool egress", "phased PRs")
- **Date:** 2026-11-17
- **Amends (when accepted):**
  - ADR-20261027090014 §4.4: the agent gateway shares the tenant gateway's host and keeps its own addresses and tunnel; it carries every agent VM's egress, not only the browser tier's.
  - ADR-20261112121456 §1: sandbox sessions leave the tenant gateway path once this ships.
- **Builds on:** ADR-372 (tenant egress gateway), ADR-361 (default-deny tenant egress and connection rates), ADR-373 (DNS-gated egress), ADR-371 (flow log), ADR-20261117091026 (sandbox egress domain policy and reached-domain log).

## Context

Sandbox sessions and agent runs leave through the ADR-372 tenant gateway, on the same public addresses as customer apps (ADR-20261112121456 §1). A site that blocks an agent's address blocks customer apps with it, and an abuse report against that address cannot be told apart from app traffic until the flow log is read.

ADR-20261027090014 §4.4 decided a dedicated agent gateway and address pool, a politeness proxy that reads the destination without decrypting, and a log that maps every connection to one run. None of it exists. Today:

- One WireGuard tunnel (`wg-tenant`) per fleet. Each node routes every guest on its tenant bridge into it by source address, and masquerades the traffic, so the gateway sees only the node.
- The gateway host runs nftables and WireGuard only: no daemon, no proxy, no log.
- The agent pool is a schedd list (`FAAS_SCHEDD_AGENT_RUN_NODES`). It keeps agent runs and sessions on those nodes, but customer apps can run there too.

## Decision

### 1. Own addresses and tunnel, on the tenant gateway host

- The tenant gateway host gains a second WireGuard interface, `wg-agent`, with its own tunnel range and its own public addresses. Agent traffic is source-NATed only to agent addresses, and app traffic only to app addresses. Neither ever carries the other's traffic.
- The addresses come from a provider whose terms allow multi-tenant egress. They are never rotated to get around a block.
- Manifest: `egress.agent_gateway` with `endpoint`, `public_key`, `tunnel_cidr`, `node_prefix` and `addresses`. It may name the tenant gateway's `ssh_host`. The ansible roles that set up `wg-tenant` gain the agent interface beside it.
- One host is one failure domain: when it is down, apps and agents lose egress together. The owner accepted that to stand the gateway up sooner. A dedicated host later is a manifest change, not a design change.

### 2. Which traffic

- **Every agent VM**, wherever it is placed: sandbox sessions in every network mode that has egress (`registries` and `web`), and agent runs. Customer apps keep `wg-tenant`, even on the same node.
- Steering is per VM, not per node. vmmd marks an agent VM's traffic on the host (the ADR-119 static-egress fwmark pattern), and a policy-routing rule sends marked traffic to the `wg-agent` table.
- **Per-VM source addresses.** WireGuard routes a reply by its destination, and guests' host-side addresses repeat across nodes. So each node gets a slice of the agent tunnel range (`node_prefix`, by default a /22), and vmmd source-NATs each agent VM to its own address in the slice for the VM's life. The gateway then sees one address per VM.

### 3. Fails closed

- A node accepts an agent VM's traffic only out of `wg-agent`. A node without the interface refuses agent VMs that have egress. It never falls back to `wg-tenant` or the node's address.
- Before an agent VM with egress boots, vmmd checks the tunnel's latest handshake (younger than 180 s) and registers the VM's address with the proxy (§4). If either fails, the boot is refused with a retryable error and agentd requeues the session.
- Rollout switch: `FAAS_AGENT_EGRESS_GATEWAY` (vmmd). While it is off, agent VMs keep today's tenant path.

### 4. The politeness proxy

A new daemon, `agentproxyd`, runs on the gateway host. It is a transparent TCP proxy for agent traffic on ports 80 and 443, which nftables redirects to it.

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
- It writes JSON lines to the journal. The log shipper already carries those off the host. The gateway host gets no database access.
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

1. **PR 1:** the manifest stanza, ansible (`wg-agent` on the gateway host and the nodes), vmmd's per-VM marking and source NAT, and the handshake check. Behind `FAAS_AGENT_EGRESS_GATEWAY`. Without the proxy, the gateway forwards agent traffic directly.
2. **PR 2:** `agentproxyd`, with registration, caps, blocklist and metrics. vmmd registers VMs and refuses a boot it cannot register.
3. **PR 3:** the connection log, its shipping, and the runbook entry for abuse reports.
4. **Before the switch is on:** the owner provisions the agent addresses on the gateway host. A metal check covers a web session and an agent run through `wg-agent`, a refusal with the tunnel down, and each cap's refusal. Then one node, then the fleet.

## Rejected alternatives

- **A dedicated gateway host now.** That is the stronger isolation, and the owner chose speed. It remains a manifest change.
- **Routing by node.** Customer apps can run on agent-pool nodes, so routing a whole node would move app traffic onto agent addresses.
- **Masquerading on the node, as `wg-tenant` does.** The gateway would see only the node, and could not cap or log per session.
- **Decrypting to read URLs.** Gregale never decrypts agent traffic (ADR-20261027090014 §4.3). SNI and `Host` are enough for caps and logs.
- **A shared counter store.** One gateway sees all agent traffic. A second gateway brings this back as a decision.
