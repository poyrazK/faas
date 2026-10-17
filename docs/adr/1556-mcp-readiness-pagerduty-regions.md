# ADR-1556: Regional PagerDuty readiness delivery

Status: Accepted

## Context

ADR-1555 fixes native readiness delivery to the US Events API. EU integrations need
their regional endpoint without permitting arbitrary credential-bearing URLs or
retargeting ongoing incident recovery.

## Decision

Add optional `region` (`us` or `eu`) to replacement PagerDuty configuration. Omitted
region means US, preserving existing clients and rows. Return effective region in
masked configuration. Disabling omits region and clears credentials; the disabled
policy reports US. Enabling or changing region still supplies the integration key
and expected version.

Pin the allowlisted endpoint together with encrypted credentials and private key
fingerprint at first delivery. Use that endpoint directly for trigger, reminder,
retry and recovery. Receiver ordering includes endpoint and fingerprint, and key
refresh requires both to match. Region changes affect unattempted incidents only.
No redirects or arbitrary endpoints may receive credentials.

Persist checked region in an append-only migration. Preserve existing US routing
snapshots. Rollback refuses EU configuration or pinned EU routes, including resolved
incident history. Upgrade all evaluators before enabling EU delivery; older workers
cannot honor EU claims. Expose the setting in API, dashboard, CLI and Go/Node/Python
SDKs with local provider contracts and memory/PostgreSQL routing regression coverage.

## Consequences

Customers can select the regional Events API appropriate to their integration.
Region selection does not establish platform-wide data residency. Native PagerDuty
fallback and inbound acknowledgement synchronization remain separate work. Live
regional service/key qualification still requires customer credentials.
