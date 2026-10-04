# Custom-domain TLS at the public edge

ADR-520. The control plane's Caddy issues and renews a Let's Encrypt
certificate for each customer custom domain on demand. Before it loads,
obtains or renews one, it asks gatewayd-public
(`http://127.0.0.1:9092/v1/internal/tls/ask?domain=<host>`, loopback only),
which answers 200 only for verified custom domains. Customer traffic reaches
the edge directly; platform hosts (`gregale.dev`, `*.gregale.dev`) keep
answering Cloudflare only.

## Enable

1. Pick the target hostname customers CNAME to, for example
   `edge.gregale.dev`. Create it as an A (and AAAA, if the edge has IPv6)
   record for the control plane's public address. It must be DNS-only: a
   CDN-proxied record cannot complete ACME validation for customer names.
2. Allow TCP 80 and 443 from every source to the control plane at the
   provider firewall. On the GCP public-beta project this conflicts with
   `forbidden_direct_origin_tcp_ports` in `deploy/gcp/public-beta-policy.json`
   and the Cloudflare-only origin rules that
   `scripts/ops/gcp_public_beta_converge.sh` converges; change that policy
   first, deliberately, or the audit will fail.
3. Add to the deploy manifest and re-render:

   ```yaml
   public_edge:
     custom_domains:
       mode: on_demand
       target: edge.gregale.dev
       addresses: [203.0.113.10]   # the edge's public IPs, for apex A/AAAA records
       acme_email: ops@gregale.dev
   ```

   ```bash
   make manifest-ansible MANIFEST=deploy/manifest/production/<fleet>.yaml
   ```

4. Converge the control plane (`bootstrap-control-plane`). This sets
   `FAAS_CUSTOM_DOMAIN_TLS=on_demand` on apid and gatewayd-public, the
   target and addresses on apid, adds Caddy's on-demand site and opens
   80/443 in nftables. Caddy validates the Caddyfile before it is written.
   Caddy's global options must be the first block in `/etc/caddy/Caddyfile`;
   if a hand-edited site sits above the `ANSIBLE MANAGED Gregale public edge`
   block, the validation fails and nothing is changed. Move that site below
   the managed block and converge again.

## Verify

```bash
curl -s -o /dev/null -w '%{http_code}\n' 'http://127.0.0.1:9092/v1/internal/tls/ask?domain=<verified custom domain>'   # 200
curl -s -o /dev/null -w '%{http_code}\n' 'http://127.0.0.1:9092/v1/internal/tls/ask?domain=unknown.example.com'      # 403
curl -sI https://<verified custom domain>/
curl -s -o /dev/null -w '%{http_code}\n' --resolve api.gregale.dev:443:<edge ip> -k https://api.gregale.dev/v1/status  # connection aborted
```

The last check must fail from any non-Cloudflare source: platform hosts
abort direct connections.

## Operate

Alerts `FaasCustomDomainTLSAskErrors` (page), `FaasCustomDomainTLSAskOverload`
and `FaasCustomDomainCertIssuanceFailing` cover this path; see
`docs/runbooks/FaasCustomDomainTLS.md`.

- `gateway_tls_on_demand_ask_total{decision}` on gatewayd-public's control
  listener counts every check. `deny_error` means the store lookup failed
  (Postgres); certificates already in Caddy's memory keep working, but
  reloads, renewals and new issuance are refused until it recovers.
  `deny_wildcard_budget` means a wildcard reached
  `OnDemandTLSWildcardNewHostsPerWeek` new hosts in 7 days. A sustained
  `deny_overload` means handshakes with unknown server names are arriving
  faster than `OnDemandTLSAskLookupsPerSecond`, usually a scan; legitimate
  names are retried on the client's next handshake. Hostnames with no
  custom-domain row are remembered for 30 s, so a domain added right after a
  scan of its name can take that long to be admitted.
- Caddy logs ACME activity (`journalctl -u caddy`). Its certificate storage
  is `/var/lib/caddy/.local/share/caddy`; losing it means every customer
  certificate is re-issued on its next handshake, subject to the CA's rate
  limits.
- apid records the outcome per domain from its port-443 probe
  (`gregale domains doctor`, `cert_status`). A probe that fails within
  15 minutes of verification reports `pending` while the edge issues.
- Wildcard admissions are in `custom_domain_tls_hosts`. Deleting a row lets
  that host be admitted again later and count against the budget again.

## Disable

Remove `public_edge.custom_domains` from the manifest, re-render and converge.
The ask endpoint disappears, so Caddy refuses every customer certificate
(including reloads), and nftables returns to Cloudflare-only. Restore the
provider firewall rules afterwards.
