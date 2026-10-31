# FaasCustomDomainTLS

Alerts: `FaasCustomDomainTLSAskErrors` (page), `FaasCustomDomainTLSAskOverload`
(warn), `FaasCustomDomainTLSFleetBudget` (warn),
`FaasCustomDomainCertIssuanceFailing` (warn).
Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`, group
`faas_custom_domain_tls`. Design: ADR-520; operations:
`docs/ops/custom-domain-tls.md`.

The control plane's Caddy issues customer-domain certificates on demand. Before
it loads, obtains or renews one, it asks gatewayd-public
(`/v1/internal/tls/ask` on the loopback control listener, 127.0.0.1:9092), which
counts every answer in `gateway_tls_on_demand_ask_total{decision}`. apid probes
each verified domain on port 443 and counts transitions to a failed certificate
in `apid_cert_issuance_failed_total`.

## FaasCustomDomainTLSAskErrors

gatewayd-public's store lookups fail (`decision="deny_error"`). Caddy keeps
serving certificates it already holds in memory, but any reload, new issuance
or renewal is refused, so new or restarted connections to affected hosts fail.

```bash
curl -s http://127.0.0.1:9092/metrics | grep gateway_tls_on_demand_ask_total
journalctl -u faas-gatewayd-public --since -30m | grep 'on-demand tls'
systemctl status postgresql faas-gatewayd-public
```

Fix the Postgres path for gatewayd-public (connection limits, PgBouncer,
credentials). No edge action is needed; Caddy asks again on the next handshake.

## FaasCustomDomainTLSAskOverload

Handshakes with unknown server names exceed `OnDemandTLSAskLookupsPerSecond`
(`decision="deny_overload"`), usually an internet scan of random SNI values.
Unknown names are negative-cached for `OnDemandTLSAskNegativeCacheSeconds`.

```bash
journalctl -u caddy --since -30m | grep -c 'certificate is not allowed'
```

Legitimate hosts are retried on their next handshake. If the source is a few
addresses, block them at the provider firewall. Do not raise the limit to
absorb a scan; it protects Postgres.

## FaasCustomDomainTLSFleetBudget

The ask endpoint refused new hosts below wildcards because the deployment
admitted `OnDemandTLSFleetNewHostsPer3h` new hosts in 3 hours
(`decision="deny_fleet_budget"`, ADR-520 amendment 1). The cap keeps the
shared ACME account under Let's Encrypt's 300 new orders per 3 hours; while
it holds, no tenant can add a wildcard host. Exact domains and hosts admitted
before are unaffected.

Find who drew the budget:

```sql
select owner_id, registered_domain, count(*)
  from custom_domain_tls_admissions
 where admitted_at > now() - interval '3 hours'
 group by 1, 2 order by 3 desc limit 20;
```

One owner or registered domain near its own budget usually means random
server names below its wildcard (a scan or griefing): the per-owner and
per-registered-domain caps stop each source, and many sources together reach
the fleet cap. Block the sources at the provider firewall if they are few.
The budget refills as admissions age past 3 hours. Do not delete ledger rows
to refill it unless the CA's own order count has room
(`journalctl -u caddy | grep -i ratelimit`).

## FaasCustomDomainCertIssuanceFailing

Five or more custom domains failed issuance within an hour. Check the shared
causes first:

```bash
journalctl -u caddy --since -2h | grep -iE 'obtain|acme|rateLimited|challenge'
gregale domains doctor <one affected domain>
```

- `rateLimited` from the CA: wait for the window; the wildcard budgets
  (`OnDemandTLSRegisteredDomainNewHostsPerWeek`,
  `OnDemandTLSOwnerNewHostsPerWeek`, `OnDemandTLSFleetNewHostsPer3h`) and
  Let's Encrypt's limits apply.
- Challenge failures for every domain: TCP 80/443 must be open to every
  source at the provider firewall and in nftables (`faas_custom_domain_tls`).
- Failures for a few domains only: their DNS points elsewhere, a CDN proxies
  them, or CAA forbids `letsencrypt.org`. The doctor's remediation tells the
  customer which.
