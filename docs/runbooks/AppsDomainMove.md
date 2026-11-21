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

1. Set gregale.app's SSL/TLS mode to Full (strict) and turn on Always Use
   HTTPS.
2. Create a Cloudflare Origin CA certificate for `gregale.app` and
   `*.gregale.app`. Install it on the control plane as
   `/etc/caddy/tls/cloudflare-origin-gregale.app.{pem,key}` (root:caddy,
   0444 and 0440, like the gregale.dev pair). The control-plane deploy
   refuses to run without it once the domains split. gregale.dev keeps its
   existing certificate.
3. Point the new zone's app hosts where gregale.dev's point today. On the
   OVHcloud fleet app traffic goes straight to the compute nodes' entry
   addresses (front: none), not through a tunnel: copy gregale.dev's
   DNS-only `*` record (and add `@` if wanted) into gregale.app, DNS-only.
   TLS fails on these names until edgednsd's `*.gregale.app` certificate is
   stored after the release; no customer has the URLs yet.
   (`deploy/scripts/cloudflare-app-edge-tunnel.sh` names the GCP nodes;
   do not run it against the OVHcloud fleet.)
4. Make sure edgednsd's Cloudflare token (the `EDGEDNSD_CF_TOKEN` deploy
   secret) has Zone > DNS: Edit on gregale.app, and note the zone id.

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
    names: ["direct-pilot.gregale.app"]   # the managed pilot name
    certificate:
      names: ["*.gregale.app"]
      ca: letsencrypt                     # proven on the gregale.dev pilot
      serve: letsencrypt
      transparency:
        domain: gregale.app
        allow_names: ["gregale.app", "*.gregale.app"]   # Cloudflare Universal SSL
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
2. Watch edgednsd order the `*.gregale.app` certificate
   (FaasEdgeCertificate.md) and check `https://<slug>.gregale.app/` serves
   with a trusted certificate. With `ca` and `serve` already on
   letsencrypt the edges keep serving the stored `*.gregale.dev`
   certificate until the new one lands, so none leaves DNS.
3. Move the old gregale.dev app hosts behind Cloudflare, before the stored
   `*.gregale.dev` certificate expires (edgednsd no longer renews it; it is
   short-lived): in the gregale.dev zone switch the `*` record to proxied
   and point it at the control plane (the same target as `api.`). Caddy on
   the control plane already serves `*.gregale.dev` with the Origin CA
   certificate, and gatewayd-public maps each alias host to its gregale.app
   app. Delete the pilot's DNS-only records too: edgednsd reconciles only
   the names it still manages, so `direct-pilot.gregale.dev` (comment
   `edgednsd (ADR-20261102081618)`) stays pointing at the nodes otherwise.
4. Revoke the old `*.gregale.dev` Let's Encrypt certificate once no edge
   serves it. From here no compute node holds a gregale.dev key.

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
step 3.3, roll back the same way and leave `*.gregale.dev` proxied to the
control plane until edgednsd has stored a new `*.gregale.dev` certificate,
then return the record to DNS-only.
