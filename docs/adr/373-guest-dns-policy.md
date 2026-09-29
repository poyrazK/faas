# ADR-373 · Guest DNS policy: blocklist and DNS-gated egress

- **Status:** accepted
- **Date:** 2026-09-29
- **Relates to:** ADR-361 (tenant egress hardening; decision 3 pins guest DNS), ADR-170 (bridge resolver)
- **Context:** Since ADR-361 every guest DNS query, to any destination, is DNAT'd to the node's bridge resolver in gatewayd-internal. That gives the platform the one place where a guest learns where things are. The resolver forwards every non-service name upstream unchanged, so a guest can still look up mining pools, malware command-and-control or phishing kits' back ends. It can also skip DNS entirely and connect to raw addresses: scanning address ranges or reaching a pool by IP.
- **Decision:**
  1. **Blocklist.** The bridge resolver answers NXDOMAIN for any name on the blocklist, matched by domain suffix on label boundaries, instead of forwarding it.
     - **Built-in list:** well-known mining pools (category `miner`).
     - **Operator feeds:** `FAAS_DNS_BLOCKLIST_FILE` adds one `domain [category]` per line (malware, C2, phishing). A configured file that can't be read fails gatewayd-internal startup rather than silently dropping the feed.
     - **Signals:** each refusal is logged with the name, category and calling app (resolved from the source address, as for service calls), and counted in `gatewayd_dns_blocked_total{category}`. `FaasGuestDNSBlocked` warns.
  2. **DNS-gated egress** (all plans; ADR-031 allowlisted destinations exempt). A guest may open new TCP flows only to addresses it resolved through the bridge resolver recently.
     - **The gate.** Each tenant netns keeps an `egress_resolved` set per family (per-element timeouts, 65,535 entries). After the allowlist accept, a guest-originated new TCP flow to an address not in the set is dropped and counted in `faas_egress_unresolved`, the `unresolved` class of `vmmd_egress_denied_total`.
     - **Who is gated.** The gate applies to every tenant VM that gets the ADR-361 policy: app instances, app tasks and builders, plus job VMs once #3887 lands. Prepared namespaces carry it too.
     - **How addresses are allowed.** When an upstream answer returns, the resolver calls the node's vmmd (`AllowResolvedEgress`) with the query's source address and the answer's A/AAAA records **before** replying, so the guest never sees an address it cannot reach yet. vmmd maps the source to the instance by its host-side address, so the resolver needs no instance identity; builders, tasks and jobs are covered the same way.
     - **Lifetime.** vmmd adds the addresses with the answer's smallest TTL, clamped to [10 min, 1 h]. The floor covers clients that cache answers past their TTL (the JVM, connection pools); the ceiling bounds how long a stale address stays open. It skips addresses already allowed for more than half the TTL, so repeated lookups cost no nft call.
     - **Seeding.** vmmd remembers each app's recent resolutions on the node (up to 4,096) and seeds a new instance's set with them at wake, so a restored snapshot can reconnect to addresses its guest cached. A connection that races the seed retries its SYN.
     - **Failure posture.** If the vmmd call fails, the resolver still returns the answer and the connection fails closed at the gate. A vmmd that predates the RPC returns `Unimplemented`, which is ignored; it has no gate either.
     - **Emergency switch.** `FAAS_EGRESS_DNS_GATING=off` on a node's vmmd turns gating off for VMs created afterwards and keeps every other egress control. It is for a node whose resolver hook is broken.
- **Consequences:**
  - A guest cannot resolve a blocked pool or feed domain.
  - DoH and DoT are closed: DoT (853) is dropped by ADR-361's port policy, and the addresses a DoH endpoint returns were never resolved through the bridge, so they are unreachable.
  - Raw-address scanning and IP-literal mining are closed.
  - Apps that connect to hard-coded IP addresses must put them in their egress allowlist (Hobby and up).
  - A lookup costs one extra local RPC, plus an nft call when an address is new or near expiry.
  - The blocklist is exact-suffix, not pattern matching; feeds must list domains.
  - Blocked lookups are logged but not persisted beyond the log.
- **Rejected alternatives:**
  - **Response-policy zones in a separate resolver daemon.** Another host service to run, for what a suffix map in the existing resolver does.
  - **Blocking at the nft layer by address.** Pool addresses change daily; names are the stable identifier.
