# ADR-20261027090014 · Gregale Agents as its own service: agentd, the agent node pool, web access, and the path to long-running sandboxes

- **Status:** proposed
- **Date:** 2026-10-18
- **Amended by (proposed):** ADR-20261027090018 (§4: fetch results, cache, bring-your-own providers and egress proxy, web monitors; answers open question 2); ADR-20261027090016 (§4 tier 1 tools become capability-gateway MCP tools); ADR-20261027090013 §11 (exposed ports and preview URLs move from §5's phase 2 into the first release); ADR-20261027090015 §3 and §5 (agent VMs are charged and reserved from their measured size history; per-node capacity is re-estimated from measurements).
- **Amends (when accepted):**
  - ADR-20261027090010: §4 state ownership, §5 admission, §12 web agents, and rollout step 6.
  - ADR-20261027090013: §1 owners, §4 lifecycle owner, and the `web` network mode in §6.
- **Builds on:**
  - ADR-062 (per-node schedd) and ADR-203 (role-aware node capacity);
  - ADR-1650 (per-unit releases);
  - ADR-361 and ADR-372 (tenant egress and its dedicated gateway);
  - ADR-041 (tenant abuse observability);
  - ADR-249 and ADR-989 (platform-held credentials and the relay);
  - ADR-909, ADR-1151 and ADR-1344 (measured admission, pause tiers, node fencing).
- **Decision:**
  1. **A new daemon, `agentd`, owns the agent service's lifecycle:** agent runs, sandbox sessions, deliveries, and idle freezing and expiry. apid keeps writing customer intent. schedd stays the only placement authority and the only writer to `instances`.
  2. **A new `agents` node role** (an ADR-203 extension). Agent VMs run only on agent nodes, which have their own sizing, overcommit and drain policy.
  3. **Web access ships in the first release, in three tiers:**
     - tier 0: model-provider web tools;
     - tier 1: a managed fetch and search service outside the VM;
     - tier 2: an opt-in headless browser in the VM.

     All of it sits behind a dedicated agent egress gateway, a politeness proxy and an enforced acceptable-use policy.
  4. **The service is built to grow into long-running, large sandboxes.** The session model carries `kind: ephemeral | persistent` from day one, with `persistent` refused until its own ADR.
- **Why:**
  - **The owner rejected building agents into the function mechanism** (2026-10-18). Agent lifecycles (minutes-long runs, sessions that idle between tool calls, deliveries, and later multi-day sandboxes) differ from request-driven apps.
  - **schedd's hot path is the app wake path,** and should not carry delivery retries or session idle timers.
  - **Shared app nodes can't host agents well.** They can't size for 2–4 GB runs and future 16–32 GB sandboxes without crowding out app wakes.
  - **Sellers need web access** (owner, 2026-10-18). Unmanaged agent web traffic from Gregale's own addresses is exactly what led to the 2026-09-24 suspension (ADR-361, ADR-372).
- **Consequences:**
  - One more daemon to operate: systemd unit, mTLS identity, metrics, and a release lane under ADR-1650.
  - A node role and a separate capacity pool.
  - A dedicated egress gateway and address pool, the politeness proxy, a fetch and search capability in outboundd, a browser preset, an acceptable-use policy, and abuse detection. All must exist before any web-capable agent runs for a customer.
- **Rejected alternatives:**
  - **Extending schedd with agent lifecycles.** It mixes long-lived, retry-heavy state into the wake path.
  - **Agents on app nodes using the flexible reserve** (the original ADR-20261027090010 §5). App wakes and agent sizing contend.
  - **Agents as functions.** The owner rejected this.
  - **Decrypting tenant HTTPS** to inspect URLs and status codes. It means holding customer data in transit, with real liability. Tier 1 gives full control where it is needed.
  - **Rotating egress addresses to avoid blocks.** That is evasion. Separating addresses only contains damage.
  - **Deferring web access.** The owner requires it.

## Context

ADR-20261027090010, ADR-20261027090011, ADR-20261027090012 and ADR-20261027090013 placed agent run and session state in schedd, and admitted runs on app nodes above the app-wake reserve. They also blocked web-browsing agents until a dedicated egress gateway, per-destination politeness and an acceptable-use policy existed.

On 2026-10-18 the owner set three directions:
- agents are a separate service, not part of the function mechanism;
- long, large sandboxes are a future product on the same service;
- web browsing is needed in the first release.

## Decision

### 1. Service boundary and ownership

| Component | Owns (only writer) | Talks to |
|---|---|---|
| apid | Agent definitions and versions, run and session intents, tokens, gating, plan and tenant limits | Postgres (intents plus `pg_notify`) |
| **agentd** (new, on control-plane nodes) | `agent_runs` and `sandbox_sessions` transitions after `queued`; input preparation; OOM reruns; `agent_deliveries`; idle freezing and expiry; workflow `agent:` steps | Postgres; githubd (checkouts); schedd (`scheddgrpc`, mTLS) to request and release instances; outboundd (deliveries) |
| schedd (per agent node, ADR-062) | Admission and placement on its node; `instances` | vmmd |
| vmmd | Firecracker: restore and boot, relay, output stream, pause, memory meter, teardown | guest-init over vsock |
| outboundd and the AI gateway | Credentialed outbound calls; model keys and budgets; tier 1 web fetch and search (§4.2) | Providers, through the agent egress gateway |

- **Replicas.** agentd runs as several replicas claiming work with `FOR UPDATE SKIP LOCKED` under lease tokens (the ADR-171 pattern), so it needs no leader. It releases independently through the per-unit lane (ADR-1650).
- **Picking a node.** agentd chooses an agent node from the published capacity view and asks that node's schedd to admit, the way apid routes app writes to a node's schedd.
- **Unchanged.** Existing ownership rules stand: apid never calls vmmd; agentd never touches `instances` or Firecracker.
- **As built in rollout step 2 (`cmd/agentd`, `pkg/agentd`).**
  - **Claims and leases.** Replicas claim with `FOR UPDATE SKIP LOCKED` under a 30 s lease renewed every 10 s, and every replica sweeps expired leases. A run moves to `running` before its VM boots, through the `placed` / `proceed` handshake (ADR-20261027090010 §6).
  - **One schedd per replica.** Each replica dials a single schedd target (`FAAS_AGENTD_SCHEDD_TARGET`). That schedd places the run on the agent pool, which today is its `FAAS_SCHEDD_AGENT_RUN_NODES` allowlist. Choosing a node's schedd from the capacity view comes with the `agents` role (step 3).
  - **Not yet.** The `agentd_*` metrics, the systemd unit, the mTLS identity and the release lane are not built. agentd is idle unless `FAAS_AGENTD_AGENT_RUNS=1`.

### 2. The agent node pool

- **Role and sizing.** A new node role, `agents`. Sizing comes from `api.DeriveNodeSizingForRole` (ADR-203). The node reserves daemon and builder headroom, with no app-wake reserve.
- **Invariant 2 still holds per node.** Under measured admission (ADR-909), measured memory ≤ the node's admission ceiling, and the sum of reservations ≤ 2× that ceiling. Active-vCPU admission follows ADR-1151 §3. Runs and sessions paused while waiting count no active vCPU (ADR-20261027090012, ADR-20261027090013).
- **Hardware.** Agent nodes carry more local NVMe for workspaces and the per-repository checkout cache.
- **Mixed role first.** Until the fleet has at least two dedicated agent nodes, a `compute+agents` role reserves a fixed share of a node for agent VMs. App wakes keep the rest.
- **Drain.**
  - Runs drain by finishing or rerunning elsewhere, which is safe because nothing was delivered (ADR-20261027090010 §8).
  - Sessions drain by persisting their workspace and expiring.
  - Node loss uses ADR-1344 fencing.

### 3. Capacity, limits and performance

**Per account** (shared by runs and sessions; Free is 0):

| Limit | Hobby | Pro | Scale |
|---|---:|---:|---:|
| Concurrent runs and sessions (`AgentRunConcurrentPerAccount`) | 2 | 5 | 20 |
| vCPU per run (`AgentRunVCPUMax`, new) | 1 | 2 | 2 |
| RAM per run (`AgentRunRAMMBMax`) | 1,024 | 2,048 | 4,096 |

The other limits are as in ADR-20261027090010 and ADR-20261027090013. Higher limits come by contract, on dedicated agent nodes.

**Per agent node.** These are estimates for a 64 GB, 20-thread node; memory use per run is not measured.
- About 50–90 concurrent model-bound runs, at roughly 0.5–1 GB of private memory each.
- About 10–20 CPU-heavy runs at full speed.
- About 2,400 runs per hour for 90-second runs.

**Scaling** means adding agent nodes, which takes days. Bursts queue fairly per tenant, show their position, and are refused past the queue bound. Gregale does not promise instant elastic bursts.

**Performance targets** are unchanged from ADR-20261027090010: under 1 s to a ready VM, under 3 s for input, under 5 s of platform overhead.

### 4. Web access (first release)

#### 4.1 Tier 0: provider web tools (the default)

Model providers' own web search and fetch tools run on the provider's network, with no egress from Gregale. The AI gateway passes them through where the target supports it (ADR-996 notes which translated targets refuse hosted tools). Presets enable them by default.

#### 4.2 Tier 1: managed fetch and search (outside the VM)

**Where it runs.** outboundd gains two relay capabilities, `web.fetch` and `web.search`. A run reaches them through the relay. They execute on the control-plane side and leave through the agent egress gateway (§4.4).

**`web.fetch`:**
- GET only. No cookies, credentials or forms.
- Content-type allowlist (HTML, text, JSON, PDF) and a size cap.
- Honors robots.txt.
- Per-domain rate limits: per tenant and fleet-wide.
- Exponential backoff on 429 and 403.
- Caching with a short TTL, scoped per tenant (never shared across tenants).
- Every request carries:
  - a user agent `GregaleAgent/1.0 (+https://gregale.dev/agents; seller=<slug>)`;
  - HTTP message signatures, so sites can recognize and allow the agent.
- Every request is logged with run, tenant, seller, URL, status and bytes.

**`web.search`** uses the seller's own search-API key as a managed integration (ADR-249). A Gregale-provided search option is a later decision.

**Coverage.** This tier covers research, reports and enrichment, so the managed scenarios (ADR-20261027090010 §2) use it rather than tier 2.

#### 4.3 Tier 2: headless browser in the VM (opt-in)

- **Preset.** A `browser` preset layer on the agent base: Chromium and Playwright, with Playwright MCP for MCP-speaking harnesses.
- **Opt-in.** An agent version declares `web: browser`. It needs at least 2 GB of RAM, so it is Pro and above. It counts as CPU-heavy capacity (§3).
- **Traffic.** TCP 80 and 443 from the VM through the agent egress gateway. QUIC is dropped by ADR-361, so browsers fall back to TCP. No other ports.
- **Control at connection level only:**
  - the politeness proxy (§4.4);
  - per-run budgets for new connections per second (ADR-361 rates), distinct domains, and bytes.
- **No decryption.** Gregale never decrypts the traffic, so URLs and status codes stay invisible on this tier. The preset sets an identifying user agent. Changing it is an acceptable-use breach rather than something the platform can enforce.

#### 4.4 The agent egress gateway and politeness proxy

- **A dedicated gateway.** Agent nodes route tenant egress through their own WireGuard gateway and address pool (the ADR-372 mechanism), separate from app-tenant egress and from platform addresses.
- **Fails closed.** If the gateway is down, agent egress stops. It never falls back to a node's address.
- **Provider choice.** The gateway is hosted with a provider whose terms allow multi-tenant egress.
- **No rotation to evade blocks.** Addresses are never rotated to get around blocks.
- **The politeness proxy** runs on the gateway. It is a transparent TCP proxy that reads the destination from the TLS SNI or the plain-HTTP `Host`, without decryption. It enforces:
  - concurrent-connection caps per tenant and per destination domain, plus a fleet-wide cap per domain, held in shared counters;
  - a complaint-fed blocklist;
  - the existing ADR-361 deny ranges.
- **Logging.** Every connection is logged with node, run, tenant, seller, destination, bytes and duration, so an abuse report maps to one run within minutes.

#### 4.5 Acceptable-use policy and enforcement

**Prohibited:**
- CAPTCHA solving or bypass;
- credential stuffing and account creation on third-party sites;
- bulk scraping beyond the per-domain limits;
- ignoring site terms or robots directives (tier 1 enforces robots);
- purchases or form submissions on a user's behalf without that user's explicit action. No payment credentials ever enter a run.

**Enforcement:**
- an abuse contact;
- suspension of a tenant (ADR-226) or of a seller account;
- complaint-driven blocklisting;
- retention of connection logs for 30 days.

#### 4.6 Detection

ADR-041's tenant-abuse observability is extended with agent egress signals:
- per-run distinct domains, connection rate and bytes;
- per-domain fleet concurrency;
- tier 1 4xx and 429 share;
- blocklist hits.

Alerts fire before a provider would notice.

#### 4.7 Web limits

Free is 0.

| Limit | Hobby | Pro | Scale |
|---|---:|---:|---:|
| `AgentWebFetchesPerDay` (tier 1) | 500 | 10,000 | 100,000 |
| `AgentBrowserAllowed` (tier 2) | no | yes | yes |
| `AgentWebDistinctDomainsPerRun` | 20 | 100 | 300 |
| `AgentWebPerDomainConnsPerTenant` | 2 | 4 | 8 |
| `AgentWebPerDomainConnsFleet` | 50 | 50 | 50 |

New connections per second per VM keep ADR-361's per-plan rates.

### 5. Growth path to long-running, large sandboxes

The same service (agentd, the agent node pool, vmmd, the relay, meters, tenants) grows in phases. Each later phase gets its own ADR.

| Phase | Adds |
|---|---|
| 1 (now) | One-shot runs. Ephemeral sessions of up to 24 h, with workspaces persisted as archives. Web tiers 0–2 |
| 2 | `persistent` sessions:<br>• workspace volumes (block devices backed up to object storage), superseding ADR-382 for sessions<br>• memory-preserving pause (single-use per-VM snapshots, then diffs)<br>• shapes of 16–32 GB and 8–16 vCPU on agent nodes<br>• multi-day lifetimes<br>• migration on drain (vmmd restart adoption and snapshot moves)<br>• reattach (preview URLs and ports moved to the first release for ephemeral sessions: ADR-20261027090013 §11) |
| 3 | GPU sandboxes on a separate pool and VMM (for example Cloud Hypervisor), with a separate ADR |

`sandbox_sessions.kind` exists from phase 1. `persistent` is refused, with a clear error, until phase 2.

### 6. Amendments

**ADR-20261027090010:**
- **§4:** agentd, not schedd, owns every `agent_runs` transition after `queued`. schedd owns only `instances`.
- **§5:** runs are admitted on the agent node pool (§2), not on the flexible reserve above app nodes' wake reserve.
- **§12:** web access follows §4 of this ADR. `AgentWebEgress` is replaced by the tier rules here.
- **Rollout step 6:** "agentd claim and lease, schedd admission on the agent pool, teardown and terminal states."

**ADR-20261027090013:**
- **§1 and §4:** agentd, not schedd, owns session transitions, freezing and expiry. schedd places and owns instances.
- **§6:** the `web` network mode follows §4 of this ADR.
- **New:** sessions carry `kind` (§5).

## Metrics (§12 additions)

- `agentd_claims_total{kind,outcome}`
- `agentd_queue_depth{kind}`
- `agent_pool_capacity_mb{node}`
- `agent_pool_active_vcpu{node}`
- `agent_web_fetch_total{outcome}`
- `agent_web_conns_total{tier,outcome}`
- `agent_web_domain_conns{domain_bucket}`
- `agent_web_blocklist_hits_total`

The `agent_web_*` series feed ADR-041's alerts.

## Rollout

These steps slot into ADR-20261027090010's sequence, all on local branches until the owner asks for PRs, behind the agent switches:

1. This ADR.
2. The agentd skeleton: claims, leases, state transitions, the schedd request and release path.
3. The `agents` node role and sizing, with the mixed role first.
4. The agent egress gateway and address pool, and the politeness proxy.
5. outboundd `web.fetch` and `web.search`, with the acceptable-use policy text and abuse detection.
6. The browser preset (tier 2).
7. Customer-facing web access, enabled only after steps 4–6 and an owner test on production.

## Open questions

1. The hosting provider for the agent egress gateway addresses.
2. Whether to offer a Gregale-provided search API, or bring-your-own only.
3. How long to retain connection logs (30 days proposed) under the DPA.
4. The browser preset's size on drive0: Chromium adds hundreds of MB to every agent node's shared base.
5. The exact share reserved for agents under the mixed role.
