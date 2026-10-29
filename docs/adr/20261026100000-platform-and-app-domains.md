# ADR-20261026100000: Platform domain and app domains are separate, and app domains are a list

Date: 2026-10-26
Status: Accepted
Amends: [ADR-480](480-platform-paths-reserved-on-platform-hosts.md) (platform hosts follow the platform domain, not the apps domain), [ADR-570](570-traffic-enforcement-guarantees.md) (alias hosts route as their canonical host)
Related: [ADR-830](830-customer-response-security-headers.md) (host surfaces), [ADR-20261015](20261015-internal-static-hosting.md) (customer content on a separate registrable domain)

## Context

Tenant apps are served at `<slug>.gregale.dev`, the same registrable domain as
the site, dashboard and API, and gregale.dev is not on the Public Suffix List.
Tenant pages are therefore same-site with the dashboard: SameSite cookies do
not separate them, and a tenant page can set a `Domain=gregale.dev` cookie that
shadows the dashboard session (`faas_sid`, host-only but unprefixed). The fix
is to move tenant code to its own registrable domain (gregale.app).

The code could not do that. One setting, `FAAS_APPS_DOMAIN` / manifest
`dns.apps_domain`, was at once:

- the suffix under which a one-label host is a tenant app;
- the domain whose apex, `api.` and `operations.` hosts are platform-owned
  (ADR-480: `apid.IsPlatformHost`, `httpsec.ClassifyHost`, gatewayd-public's
  cookie stripping, platform path reservation, the GitHub webhook and
  security.txt hosts);
- the host in platform emails (password reset, magic link, verification, MFA),
  inbound webhook URLs and GitHub check links.

Deployment previews and environment hosts used a separate hard-coded
`.gregale.dev`. Setting the apps domain to gregale.app would have moved the
dashboard and API with it, broken every `<slug>.gregale.dev` URL, revoked every
custom domain whose CNAME pointed at the old apex (the DNS doctor marks it
drifted), and left previews behind.

## Decision

### 1. Five settings

| Manifest `dns.` | Environment | Default | Role |
|---|---|---|---|
| `platform_domain` | `FAAS_PLATFORM_DOMAIN` | the apps domain | apex, `api.`, `operations.`: site, dashboard, API, platform emails and webhook URLs |
| `apps_domain` | `FAAS_APPS_DOMAIN` | `gregale.dev` | primary apps domain: app URLs, PR previews, deployment aliases |
| `deploy_domain` | `FAAS_DEPLOY_DOMAIN` | `gregale.dev` | deployment previews (`deploy-<n>-<slug>`) and environment hosts (`env-…`) |
| `apps_domain_aliases` | `FAAS_APPS_DOMAIN_ALIASES` | none | more apps domains that reach the same apps |
| `apps_domain_alias_mode` | `FAAS_APPS_DOMAIN_ALIAS_MODE` | `serve` | `serve` or `redirect` (308 to the canonical host) |

The environment wins over a daemon's TOML, as it always has for
`FAAS_APPS_DOMAIN`. With only `apps_domain` set every daemon behaves exactly as
before. `pkg/hostidentity.Domains` holds the layout; `NewDomains` validates it
(valid names, app domains that do not nest, at most eight aliases).

### 2. Host classes

- **Platform host**: the platform apex, `api.` or `operations.` host (and, as
  before, localhost, IP literals, and every host when no domain is set).
  Platform paths, cookies, the GitHub webhook, `/metrics`, `/healthz`,
  security.txt and OAuth metadata follow the platform domain.
- **App host**: one label under the primary apps domain or an alias, that is
  not a platform host. The platform domain may equal the apps domain (today)
  or an alias (after a move); its platform hosts are excluded by name.
- **Custom domain**: anything else.

### 3. Aliases become their canonical host at the public edge

`Domains.CanonicalAppHost` maps `<label>.<alias>` to `<label>.<apps>`.
gatewayd-public rewrites the Host of an alias request to the canonical host
before host classification, routing and every host-keyed policy, and passes the
host the client sent in `X-Forwarded-Host`. An alias therefore reaches exactly
the app, PR preview, deployment alias or sidecar its label names, and every edge
rule, convergence fence, rule cache, response cache and traffic-policy snapshot
(all keyed by the request host) applies to it exactly as to the canonical host:
a WAF, IP or JWT rule written for `shop.gregale.app` cannot be bypassed through
`shop.gregale.dev`. The app sees the canonical Host. gatewayd-internal never
routes an alias host. A reserved label (section 6) is never an app, so
`s3.gregale.dev` and the other platform-zone hosts pass through untouched
after the old apps domain becomes the platform zone.

Edge rules stored under an alias name (`match_host` written before the move)
keep applying: gatewayd-internal also reads the rules for each alias name of
the canonical host and merges them in priority order, and a mutation of such a
rule fences and invalidates the canonical host too. With no alias configured
neither path does anything.

In `redirect` mode the public edge answers an alias host with a 308 to the
canonical host (method and body preserved) and never serves tenant code there.

### 4. URLs Gregale hands out

App URLs are built under the primary apps domain; deployment previews and
environment hosts under the deploy domain; platform emails, inbound webhook
URLs, GitHub dashboard links and CSRF origins under the platform domain.

### 5. Custom domains survive a move

The DNS doctor accepts a CNAME to the configured target
(`FAAS_CUSTOM_DOMAIN_TARGET`) or to the apex of any owned domain, so customers
whose records point at the old apps apex are not revoked. Instructions still
name the configured target or, without one, the primary apps apex. The
on-demand TLS ask refuses every owned domain and the names under it. The 504
transport encoding accepts a `CF-Worker` subrequest from any owned zone.

### 6. Reserved slugs

`s3`, `ai`, `edge`, `get`, `identity`, `registry`, `origin` and `app-edge` are
real hosts in the platform zone and are now reserved app slugs.

### 7. Delivery

Daemons read the layout from the environment; gatewayd-internal and apid also
accept `platform_domain`, `deploy_domain`, `apps_domain_aliases` and
`apps_domain_alias_mode` keys in their TOML. `make manifest-ansible` renders the
manifest's `dns.*` fields (and `dns.apps_domain` as `gatewayd_apps_domain`) into
every host's vars. A shared `99-faas-domain-layout.conf` drop-in delivers them to
apid, schedd, gatewayd-internal, gatewayd-public (both edges), githubd, imaged
and builderd; it writes nothing for an unset field. The public edge serves the platform
zone with its existing certificate and every other owned zone (apps,
deployment-preview and alias domains) with that zone's Origin CA certificate at
`/etc/caddy/tls/cloudflare-origin-<domain>.{pem,key}`.
`cloudflare-app-edge-tunnel.sh` adds tunnel ingress for `APPS_DOMAIN_ALIASES`.

## Moving apps to gregale.app

1. Buy gregale.app; add the Cloudflare zone, an Origin CA certificate for
   `gregale.app` and `*.gregale.app`, the Worker route and tunnel ingress
   (`APPS_DOMAIN=gregale.app APPS_DOMAIN_ALIASES=gregale.dev
   deploy/scripts/cloudflare-app-edge-tunnel.sh`).
2. Install the gregale.app certificate on the public edge as
   `/etc/caddy/tls/cloudflare-origin-gregale.app.{pem,key}`; gregale.dev, now
   the platform domain, keeps the existing certificate. Set
   `platform_domain: gregale.dev`, `apps_domain: gregale.app`,
   `deploy_domain: gregale.app` and `apps_domain_aliases: [gregale.dev]`,
   render, and roll out. New URLs use gregale.app; old ones keep working.
3. After customers have moved, set `apps_domain_alias_mode: redirect`. Tenant
   code is no longer served on gregale.dev.
4. Prefix the session cookie with `__Host-` (separate change).

Stored values that name an old host keep working only while that domain stays
an alias: Application Auth's external URL and JWT issuer, public build URLs
baked into images, hosting receipts, edge-rule `match_host` (read under the
canonical host, section 3), CORS origins, dev-watch hosts, and GitHub
deployment links. Removing an alias needs a data migration for those.

## Consequences

- One code path; production behaviour is unchanged until the manifest changes.
- Traffic-policy analysis (ADR-570) enumerates hosts under the primary apps
  domain only. Alias hosts mirror them and are not separate claims.
- An app behind an alias sees the canonical Host and the client's host in
  `X-Forwarded-Host`; an app that builds absolute URLs from Host emits the new
  domain.
- With aliases configured, an edge-rule cache miss reads the store once more
  per alias.
- The deployment-preview suffix is read from a process-wide layout set while
  each daemon wires itself (`hostidentity.Configure`); it was a constant.

## Rejected alternatives

- **Domains in the database, changed at runtime.** A move needs DNS, TLS and
  Cloudflare changes that are deploy-time anyway, and every request would pay
  for a lookup the router does not need.
- **Redirect only, no alias serving.** A 308 breaks clients that do not follow
  redirects for API calls; serve mode gives customers time to move.
- **Rewriting stored hosts in place.** Customers registered some of them
  outside Gregale (OAuth issuers, webhooks, CNAMEs); an alias keeps them valid.
