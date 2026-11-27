# ADR-20261127100000: Operator egress denials and abuse report

Status: Accepted

Extends ADR-20261115175927 (operator console routes) and
ADR-20261124093000 (operator gateway traffic report, whose windowed
Prometheus read this follows).

## Context

Tenant egress is filtered on every compute node (spec §11: deny 25/465/587,
RFC1918, link-local and metadata ranges, plus allowlists, port policy, rate
limits and flood guards), and several daemons emit abuse signals. The
operations console can search the flow log for one app
(`GET /v1/admin/egress-flows`, ADR-371) and show or set an account's abuse
hold (`/v1/admin/accounts/{id}/risk`). Nothing shows the fleet: which apps
are being denied, for what, on which node, and whether any abuse signal has
fired. Today an operator learns this only from an alert.

The data is already in Prometheus. These were checked against the
production fleet's Prometheus on 2026-11-27 over 7 days; every query
answered in under 150 ms:

- `vmmd_egress_denied_total{app,class,node}`: vmmd's per-app nftables drop
  counters. Classes are `smtp`, `rfc1918`, `metadata`, `allowlist`,
  `port_policy`, `rate_limit`, `flood` and `unresolved`. About 6.1k drops
  then, from 13 apps, mostly `unresolved` and `rfc1918`.
- `vmmd_egress_new_destinations_total{app}`: first contact with a
  destination (about 1.9k).
- `vmmd_egress_flow_log_rows_total`: rows written to the flow log.
- `schedd_egress_abuse_recycles_total{app,reason}`,
  `schedd_account_abuse_holds_total{reason}` and
  `schedd_cpu_abuse_signals_total{plan,action}`.
- `gatewayd_dns_blocked_total{category}`: the guest DNS resolver's blocks.
- `imaged_abuse_scan_findings_total{category,action}`: image scan findings.
- `vmmd_egress_deny_total{cidr}` and `imaged_oci_egress_deny_total{cidr}`:
  host denylist drops by tenant traffic and by image pulls.

Each family has an alert (`FaasTenantEgressAbuse`, `FaasAccountAbuseHold`,
`FaasTenantCPUAbuse`, `FaasGuestDNSBlocked`, `FaasImageAbuseBlocked`,
`FaasImageAbuseFlagged`).

## Decision

### One operator session read, Prometheus only

| Route | Returns |
| --- | --- |
| `GET /v1/admin/obs/egress?window=24h` | `ObsEgressReport` |

It is operator-only (operator session read), sends `no-store` and is
excluded from spec compliance. `window` is a Go duration from 5 minutes to
7 days (`ObsEgressWindow*` in `pkg/api/limits.go`), default 24 hours; any
other value is a 400. It needs no migration, store query or new metric.

`pkg/apid/egressdenials` (ADR-982) runs 16 queries concurrently at one
pinned instant. The budget is `ObsEgressPrometheusBudget` (3 s) for windows
up to 24 hours and `ObsEgressLongWindowBudget` (10 s) beyond. The source is
`observed`, `partial` (the failed queries are named), `unavailable` or
`not_configured`. Counts are `increase()` over the window rounded to whole
numbers; increase extrapolates, so they are estimates, not ledger values.
Every daemon registers every family, so the queries name each emitter's own
prefix and drop empty `app` labels.

### Contents

- **Totals:**
  - denials in the window and in the last hour;
  - apps with any denial;
  - new destinations;
  - abuse recycles, abuse holds and CPU abuse signals;
  - blocked DNS lookups;
  - blocked and flagged image scan findings;
  - flow log rows (absent when the query failed).
- **Denials** by class and by node.
- **Apps:** the union of the top `ObsEgressAppsMax` (25) apps by denials,
  the top 25 by new destinations, and every recycled app. Recycled apps
  come first, then by denials, capped at 25. Each row has its per-class
  breakdown, and its slug and account from the store. Labels that are not
  app UUIDs are dropped.
- **Abuse signals:**
  - recycles per app and reason;
  - holds by reason;
  - CPU signals by plan and action;
  - DNS blocks by category;
  - scan findings by category and action;
  - denylist drops by source (`firewall`, `image_pull`) and CIDR.

Lists are capped at `ObsEgressRowsMax` (100).

## Consequences

- **Console:** gets an Egress page that links each app to its flow log
  search and each account to its abuse hold panel.
- **Alerts unchanged:** the page adds no alert or metric.
- **No addresses:** destinations stay in the flow log, which keeps its own
  access rules; the report carries counts, classes and denylist CIDRs only.
