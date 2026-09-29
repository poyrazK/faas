# ADR-370 · Guest DNS policy: blocklist and DNS-gated egress

- **Status:** accepted
- **Date:** 2026-09-29
- **Relates to:** ADR-361 (tenant egress hardening; decision 3 pins guest DNS), ADR-170 (bridge resolver)
- **Context:** Since ADR-361 every guest DNS query, to any destination, is DNAT'd to the node's bridge resolver in gatewayd-internal. That gives the platform the one place where a guest learns where things are. The resolver forwards every non-service name upstream unchanged, so a guest can still look up mining pools, malware command-and-control or phishing kits' back ends. It can also skip DNS entirely and connect to raw addresses: scanning address ranges or reaching a pool by IP.
- **Decision:**
  1. **Blocklist.** The bridge resolver answers NXDOMAIN for any name on the blocklist, matched by domain suffix on label boundaries, instead of forwarding it.
     - **Built-in list:** well-known mining pools (category `miner`).
     - **Operator feeds:** `FAAS_DNS_BLOCKLIST_FILE` adds one `domain [category]` per line (malware, C2, phishing). A configured file that can't be read fails gatewayd-internal startup rather than silently dropping the feed.
     - **Signals:** each refusal is logged with the name, category and calling app (resolved from the source address, as for service calls), and counted in `gatewayd_dns_blocked_total{category}`. `FaasGuestDNSBlocked` warns.
  2. **DNS-gated egress** (all plans; ADR-031 allowlisted destinations exempt). A guest may open TCP only to addresses it resolved through the bridge resolver recently. This is recorded in an amendment when it lands.
- **Consequences:**
  - A guest cannot resolve a blocked pool or feed domain.
  - DoH and DoT are already closed: DoT (853) is dropped by ADR-361's port policy. A DoH endpoint on 443 still answers, but once DNS gating is enforced the addresses it returns are unreachable.
  - The blocklist is exact-suffix, not pattern matching; feeds must list domains.
  - Blocked lookups are logged but not persisted beyond the log.
- **Rejected alternatives:**
  - **Response-policy zones in a separate resolver daemon.** Another host service to run, for what a suffix map in the existing resolver does.
  - **Blocking at the nft layer by address.** Pool addresses change daily; names are the stable identifier.
