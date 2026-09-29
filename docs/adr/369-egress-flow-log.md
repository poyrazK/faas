# ADR-369 · Egress flow log for abuse attribution

- **Status:** accepted
- **Date:** 2026-09-29
- **Relates to:** ADR-361 (tenant egress hardening)
- **Context:** When Google suspended the production project on 2026-09-24, it gave no detail beyond "traffic from your project". The platform had no record of which tenant opened which outbound connection, so the operators could not name the workload themselves. The only flow data was schedd's conntrack snapshot: the 32 busiest endpoints per instance every 10 seconds, held in memory. Short connections and slow scans slip through it, and nothing survives a restart. Provider and abuse-desk reports name an address and a time, often weeks later.
- **Decision:**
  1. **Capture in the kernel.** Each instance netns keeps a dynamic `egress_flows` set per family, keyed by destination address and TCP port (`ipv4_addr . inet_service`, 10-minute idle timeout, 65,535 entries). Every guest-originated TCP new flow updates it. The rule runs after the per-VM rate, flood and non-TCP drops and before the allowlist accept, so it records TCP flows that passed the rate limits, including allowlisted ones; the port policy may still drop some of them.
  2. **Record new pairs.** vmmd's 15-second poll lists the set (`nft -j list set`) for every live instance and diffs it with the previous tick. It inserts the new (destination, port) pairs into `egress_flow_log` in one statement, with the time, node, account, app and instance. A pair is written again only after it has aged out of the set, i.e. after 10 minutes without a new flow to it, so steady traffic produces a few rows per destination per hour. A failed insert is logged and not retried; the log is evidence, not accounting, and must not back up the poll. Namespaces created before this change have no set and read as empty. `vmmd_egress_flow_log_rows_total` counts rows written.
  3. **Retention.** Rows are kept `EgressFlowLogRetentionDays` (30), long enough for a report that arrives weeks later. meterd deletes older rows hourly in bounded batches.
  4. **Lookup.** `GET /v1/admin/egress-flows` (operator, MFA) and `gregale admin egress-flows` filter by remote address or CIDR, account and a `[from, to)` window of at most the retention period, newest first, up to 1,000 rows. A GiST index on the address serves both exact and CIDR lookups.
- **Consequences:**
  - An abuse report ("address A hit B at time T") maps to one account and instance in one query. The node's public or egress-gateway address identifies the node.
  - Storage grows with distinct destinations per instance per 10 minutes, bounded by 30 days.
  - UDP and ICMP are not recorded; ADR-361 drops all guest-originated UDP except pinned DNS, and all ICMP.
  - The log records destinations, not payload sizes or durations.
  - A flood spread across many instances of one account is still not detected automatically. Per-destination rates are enforced per instance (ADR-361 decision 9), and this log records destinations, not rates.
- **Rejected alternatives:**
  - **NFLOG to userspace for every new flow.** Complete and precise, but it needs a per-namespace netlink listener in vmmd and a new dependency. The set-and-diff approach reuses the poll that already runs.
  - **Persisting the conntrack snapshot.** It is capped at 32 endpoints and misses short flows.
  - **Host-side logging after NAT.** It loses the instance identity.
