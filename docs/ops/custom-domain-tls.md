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
   provider firewall. The GCP public-beta policy has a narrow
   `access.custom_domain_tls` exception: only the control plane may carry
   `gregale-custom-domain-edge`, and only its two named TCP/80,443 rules may
   admit public sources. Preview the access phase of
   `scripts/ops/gcp_public_beta_converge.sh`, then apply it using the access
   cutover checks in [the GCP runbook](../runbooks/gcp-public-beta-hardening.md).
   Keep the Cloudflare origin rules; the audit still rejects other public
   origin rules, extra ports, and the custom-domain tag on compute nodes.
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

   The checked-in GCP production template already enables this mode and uses
   `edge.gregale.dev`. Publish its DNS-only A/AAAA records before deploying.
   Populate `addresses` with the control plane's reserved public addresses
   before offering apex A/AAAA instructions.

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

Alerts `FaasCustomDomainTLSAskErrors` (page), `FaasCustomDomainTLSAskOverload`,
`FaasCustomDomainTLSFleetBudget` and `FaasCustomDomainCertIssuanceFailing`
cover this path; see
`docs/runbooks/FaasCustomDomainTLS.md`.

- `gateway_tls_on_demand_ask_total{decision}` on gatewayd-public's control
  listener counts every check. `deny_error` means the store lookup failed
  (Postgres); certificates already in Caddy's memory keep working, but
  reloads, renewals and new issuance are refused until it recovers.
  New exact and wildcard hosts share three budgets (ADR-520 amendment 4):
  `deny_wildcard_budget` means the host's registered domain reached
  `OnDemandTLSRegisteredDomainNewHostsPerWeek` new hosts in 7 days across
  its exact claims and wildcards, `deny_owner_budget` that the app's organization or
  account reached `OnDemandTLSOwnerNewHostsPerWeek`, and `deny_fleet_budget`
  that the deployment reached `OnDemandTLSFleetNewHostsPer3h` in 3 hours.
  Previously admitted hosts stay free across reloads and renewals. Permission
  asks also precede successful certificate loads, so their frequency no longer
  triggers `deny_backoff`; inspect Caddy's ACME logs for actual issuance failures.
  `deny_not_allowlisted` means the wildcard's owner set a
  host allowlist (`gregale domain host get`) that does not name the
  host; it is the customer's choice, not a fault. A sustained
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
  Certificate-failure notices go to the app's current organization owners/admins,
  or to the owner for a personal app. Claim transfers discard cached diagnostics
  and any in-flight results for the previous claim.
- CAA must permit Let's Encrypt using HTTP-01 or TLS-ALPN-01 in on-demand mode.
  A record restricted to `validationmethods=dns-01` denies this path even when
  its issuer is `letsencrypt.org`. An `accounturi` restriction reports pending:
  compare it with Caddy's actual ACME account and inspect the ACME logs before
  changing the restriction. Legacy DNS-01 mode checks for `dns-01` instead.
  Legacy wildcard certificates also honor `issuewild` at the wildcard's zone;
  concrete certificates below an on-demand wildcard use `issue`.
- Failed certificate-alert deliveries release their current attempt for retry
  on the next doctor pass. Successful sends keep the 24-hour cooldown.
  Provider keys stay stable for retries of the same claim and delivery payload;
  a replacement claim has an independent key.
- Exact and wildcard admissions are in `custom_domain_tls_hosts`. Deleting a row lets
  that host be admitted again later and count against the budget again. The
  budgets count `custom_domain_tls_admissions`, which keeps 7 days of new
  hosts even after their claim is deleted; deleting its rows refills the
  budgets they counted toward.

## Disable

Remove `public_edge.custom_domains` from the manifest, re-render and converge.
The ask endpoint disappears, so Caddy refuses every customer certificate
(including reloads), and nftables returns to Cloudflare-only. Restore the
provider firewall rules afterwards. On GCP, remove the two
`gregale-custom-domains-v4/v6` rules and the dedicated instance tag, and set
`access.custom_domain_tls.enabled` to false. Keep the Cloudflare origin rules.
