# Moving apps to gregale.app

Apps move from `<slug>.gregale.dev` to `<slug>.gregale.app`; the site,
dashboard and API stay on gregale.dev. Every code path is already in place
(ADR-20261026100000) and changes nothing until the manifest does. Direct
entry then serves gregale.app only, so no compute node holds a
browser-trusted key for the dashboard or API (ADR-20261112114642, R8-6).

Old app URLs keep working: gregale.dev stays an alias that reaches the same
app, under the same edge rules, until you switch it to redirect.

## 0. As soon as the domain is bought

1. Add the `gregale.app` zone to the Cloudflare account and point the
   registrar at its name servers.
2. Submit gregale.app to the Public Suffix List private section, with the
   `_psl` TXT record the list asks for (ADR-20261026100000 amendment 1).
   Browsers pick it up only in later releases, weeks to months out, so do
   this first. It makes each app its own site: one tenant can no longer set
   cookies another tenant's app receives.

## 1. Cloudflare and the control plane (no traffic yet)

1. Create a Cloudflare Origin CA certificate for `gregale.app` and
   `*.gregale.app`. Install it on the control plane as
   `/etc/caddy/tls/cloudflare-origin-gregale.app.{pem,key}` (mode 0600,
   owner caddy). gregale.dev keeps its existing certificate.
2. Add the Worker route and tunnel ingress for the new zone:

   ```sh
   APPS_DOMAIN=gregale.app APPS_DOMAIN_ALIASES=gregale.dev \
     deploy/scripts/cloudflare-app-edge-tunnel.sh
   ```

3. If edgednsd will manage the new zone, give its Cloudflare token
   (`FAAS_EDGEDNS_CF_TOKEN_FILE`) Zone > DNS: Edit on gregale.app and note
   the zone id.

## 2. The manifest

```yaml
dns:
  platform_domain: gregale.dev     # site, dashboard, API, emails, webhooks
  apps_domain: gregale.app         # new app URLs, PR previews, aliases
  deploy_domain: gregale.app       # deploy-<n>-<slug> and env- hosts
  apps_domain_aliases: [gregale.dev]
  # apps_domain_alias_mode: serve  # the default; redirect comes in step 5

public_edge:
  # ...unchanged...
  dns:
    zone_id: <gregale.app zone id>
    names: ["*.gregale.app"]
    certificate:
      names: ["gregale.app", "*.gregale.app"]
      ca: letsencrypt-staging      # step 3 moves it to letsencrypt
      transparency:
        domain: gregale.app
```

`gregalectl manifest validate --file <manifest>` refuses any certificate name, or DNS-only
name, under gregale.dev once the domains differ. Keep a gregale.dev host
behind Cloudflare with `public_edge.dns.proxied` if one must stay managed.

Render and check before rolling out:

```sh
make manifest-ansible MANIFEST=<manifest>
grep -h 'faas_platform_domain\|gatewayd_apps_domain\|faas_apps_domain_aliases\|faas_edgednsd_certificate_names' \
  deploy/ansible/.generated/inventory/host_vars/*.yml
```

## 3. Roll out

1. Release in the usual order (data, control plane, edge and nodes,
   runtime). New URLs, emails and the dashboard now use gregale.app for
   apps; gregale.dev app URLs keep serving.
2. Watch edgednsd order the staging certificate (FaasEdgeCertificate.md),
   then set `certificate.ca: letsencrypt`, release, and once the production
   certificate is stored set `certificate.serve: letsencrypt` and release
   again. Moving `ca` and `serve` together would take every edge out of DNS
   until the new order lands.
3. Remove the old gregale.dev direct records by hand. edgednsd reconciles
   only the names it still manages, so the pilot's DNS-only records (comment
   `edgednsd (ADR-20261102081618)`, e.g. `direct-pilot.gregale.dev`) stay
   pointing at the nodes. Delete them in the gregale.dev zone, or replace
   them with the proxied tunnel record.
4. Revoke the old `*.gregale.dev` Let's Encrypt certificate once no edge
   serves it, then move the Certificate Transparency watch's allow list if
   needed. From here no compute node holds a gregale.dev key.

Check a few apps on both names:

```sh
curl -sI https://<slug>.gregale.app/ | head -1
curl -sI https://<slug>.gregale.dev/ | head -1      # same app, via the alias
curl -s https://<slug>.gregale.app/ -o /dev/null -w '%{ssl_verify_result}\n'
```

and run the journeys (`scripts/e2e-journeys.sh`).

## 4. Customers

Custom domains keep working: the DNS doctor accepts a CNAME to the old apex.
Tell customers that app URLs are now under gregale.app and that stored
values naming gregale.dev (Application Auth external URL and JWT issuer,
CORS origins, OAuth redirect URIs registered elsewhere, webhooks) keep
working only while gregale.dev is an alias.

## 5. Later: stop serving tenant code on gregale.dev

After customers have moved, set `apps_domain_alias_mode: redirect` and
release: gregale.dev app hosts answer 308 to gregale.app. Then prefix the
session cookie with `__Host-` (a separate change; it signs everyone out
once). Removing the alias entirely needs a data migration for the stored
values above.

## Rollback

Before step 3.3, revert the manifest's `dns:` and `public_edge.dns:` blocks
and release: apps go back to gregale.dev, which never stopped serving. After
the old direct records are gone, roll back the same way and restore them (or
keep gregale.dev behind Cloudflare until a new certificate is ordered).
