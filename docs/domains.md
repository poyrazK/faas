# Domains

Custom domains are attached to an app after it has a live deployment.

```bash
gregale domains add --app my-api --domain api.example.com
gregale domains verify api.example.com
gregale domains list
```

`domains add` prints the DNS records to publish:

| Record | Purpose |
|---|---|
| `_faas-verify.<domain>  TXT  <token>` | Proves you own the domain. |
| `<domain>  CNAME  <target>` | Sends traffic to Gregale. |
| `<domain>  A/AAAA  <address>` | Use instead of the CNAME at a zone apex (`example.com`), where a CNAME is not allowed. Only shown when the platform publishes edge addresses. |

The same list is in the API response as `dns_records`. Point the name
straight at the target. If a CDN or proxy (for example an orange-clouded
Cloudflare record) answers for your domain, Gregale cannot obtain a
certificate for it.

Once the TXT record verifies and the name points at Gregale, the certificate
is issued automatically on the first HTTPS connection, usually within a
minute, and renewed before it expires. Keep both records in place: removing
the routing record stops renewal and marks the domain `dns_drifted`. If your
zone publishes CAA records, they must allow `letsencrypt.org`.

To send a custom hostname to one project environment rather than the app's
application-wide route, pass `--environment`:

```bash
gregale domains add --app my-api --domain staging.example.com --environment staging
gregale projects environments diff shop --from production --to staging
```

Environment-bound domains follow only that environment's active project
release set. They are not used as the app's default domain, are shown in the
environment state and diff, and are not copied when cloning an environment
because their DNS and certificate ownership is external to Gregale.

`gregale domains doctor` checks DNS, certificate state, and the route without
changing the domain.

Wildcard domains (`*.example.com`, Pro and Scale) prove ownership with the
TXT record at `_faas-verify.example.com` and route with a wildcard CNAME.
Each subdomain gets its own certificate on its first HTTPS connection; at
most 40 new subdomains per wildcard get a certificate in any 7 days.
Subdomains that already have one keep it and renew normally.

The default `*.gregale.dev` hostname works without a custom domain. Domain
entitlement, certificate limits, and wildcard behavior are listed in
[plans](plans.md).
