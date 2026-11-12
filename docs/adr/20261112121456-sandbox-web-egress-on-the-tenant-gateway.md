# ADR-20261112121456 · Sandbox web egress ships on the tenant egress gateway

- **Status:** proposed (owner decision, 2026-11-12: "start phase 1")
- **Date:** 2026-11-12
- **Amends (when accepted):**
  - ADR-20261027090014 §4.4 and its consequence "all must exist before any web-capable agent runs": a sandbox session in `web` mode may run before the dedicated agent egress gateway and the politeness proxy exist.
  - ADR-20261027090013 §6: the `web` row.
- **Builds on:** ADR-361 (default-deny tenant egress), ADR-372 (tenant egress gateway), ADR-373 (pinned DNS, blocklist, DNS-gated egress), ADR-371 (flow log), ADR-20261027090013 §6 and §10 (sandbox registries mode and the guest MAC guard).

## Context

Every other agent sandbox provider lets a sandbox reach the web, and most agent work needs it: reading documentation, calling public APIs, running a headless browser. Gregale's sandboxes refuse `network.mode: web` because ADR-20261027090014 makes a dedicated agent egress gateway, a separate address pool, a politeness proxy, tier 1 fetch and abuse detection preconditions of any web-capable agent. None of those is built.

Customer apps already reach the web under controls built after the 2026-09-24 suspension: ports 80 and 443 only, pinned DNS with a blocklist, per-plan connection rates per VM and per destination, new-destination detection with the abuse-hold kill switch, a flow log, and, where the fleet has one, the ADR-372 gateway that keeps tenant traffic off the node's own address. A sandbox in registries mode already runs on that machinery with a name allowlist.

## Decision

1. **`web` mode ships now, on the fleet's tenant egress gateway.** It is the same egress customer apps get. The dedicated agent gateway, address pool and politeness proxy (ADR-20261027090014 §4.4) follow as phase 3 and then replace this path for sandboxes.
2. **What a web-mode session reaches:**
   - TCP 80 and 443, only to addresses its resolver answered (ADR-373 DNS gating). QUIC and every other port stay dropped.
   - Its resolver is a new bridge listener, `SandboxWebDNSPort` (5357). It answers public names only: it refuses single-label names, Gregale's internal service names, and special-use names (`localhost`, `.local`, `.internal`, `.arpa` and the like), and it applies the ADR-373 blocklist.
   - The plan's ADR-361 connection rates, per VM and per destination, and the flow log, new-destination detection and abuse hold that apply to apps.
   - Nothing on the platform side: like registries mode, no bridge service ports, no managed database route, no private service addresses, and the guest MAC guard (§10), because the tenant is root in the guest.
3. **It fails closed off the gateway.** vmmd boots a web-mode session only when the host routes tenant egress through the ADR-372 interface (`FAAS_TENANT_EGRESS_IFACE`). Otherwise the wake fails and the session ends `failed`; traffic never leaves from a node's own address. `FAAS_SANDBOX_WEB_NODE_EGRESS=1` lifts this for development and test fleets only.
4. **A per-session budget of distinct domains.** vmmd counts the registrable domains (public suffix plus one label) a session has been granted. Past `SandboxWebDistinctDomains` for the plan (Hobby 200, Pro 1,000, Scale 5,000; Free has no sessions), it grants no new domain, so the guest's connections to it are dropped and the refusal is logged. A browser touches many domains per page (CDNs, fonts, analytics), so these are higher than ADR-20261027090014 §4.7's per-run fetch limits.
5. **The acceptable-use policy of ADR-20261027090014 §4.5 applies** unchanged. Gregale does not decrypt traffic, so the identifying user agent is a policy duty, not an enforced one.
6. **Rollout.** Units release independently (docs/release-compatibility.md), so:
   - first vmmd, gatewayd-internal and schedd accept `web`;
   - then apid accepts it behind `FAAS_SANDBOX_WEB_NETWORK` (default off). An operator turns it on only for a fleet whose manifest declares `egress.tenant_gateway`, after the owner's production test (the gating rule for customer features).

## Consequences

- Sandbox web traffic shares the tenant gateway's addresses with customer apps. An abusive session can get those addresses blocked for apps too. The abuse hold and the flow log map a report to one account; separation comes with phase 3.
- No per-domain fleet-wide concurrency cap yet: the per-destination connection rate per VM is the only per-domain control until the politeness proxy exists.
- Phase 2, the browser preset (ADR-20261027090014 §4.3), runs on this mode: Chromium with its DevTools port exposed through ports at create (ADR-20261027090013 §11).

## Rejected alternatives

- **Waiting for the full ADR-20261027090014 §4.** It blocks a capability every competitor has behind four unbuilt components.
- **Opening web mode from the node's address.** That is the configuration that led to the 2026-09-24 suspension.
- **The app resolver for sandboxes.** It answers Gregale service names and bound databases, which a sandbox must not learn.
