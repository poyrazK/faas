# Domains

## Declare bindings and review changes

Use a separate `domains.yaml` to add or move bindings:

```yaml
version: 1
domains:
  - domain: api.example.com
    app: my-api
  - domain: stage.example.com
    app: my-api
    environment: stage
```

```sh
gregale domain plan --file domains.yaml --out domains.plan.json
gregale domain apply --plan domains.plan.json --confirm --rollback-out domains.rollback.json
```

Planning reads one snapshot without changing bindings or probing DNS. The diff
shows creates, moves, unchanged bindings, destination deployments and source
default selections that a move clears. Apply refreshes the saved diff and commits
the set atomically. Changed claims, intervening moves, lost verification or a
changed destination deployment require a fresh review. Final publication policy
is checked at apply and can refuse a plan without changing any binding.

Files accept up to 20 unique bindings. Omitted domains stay attached. An omitted
environment selects ordinary default routing. Apps must share an account
namespace and organization, with deployment-write access to every source and
destination. New domains need normal DNS proof; this flow does not take over
another owner's claim. Plan artifacts contain no ownership tokens.

## Move a verified domain

```sh
gregale domain move api.example.com --app next-api --dry-run
gregale domain move api.example.com --app next-api --confirm --rollback-out previous-binding.json
gregale domain move api.example.com --app next-api --env production --dry-run
```

A move requires verified ownership and a serving destination deployment. It keeps
TXT proof, verification, certificate state, wildcard allowlists and issuance
budgets. Keep DNS records in place. If this was the source app's default domain,
the plan shows that selection being cleared and the source app falls back to its
platform URL. Selecting a default on the destination remains an explicit
`domain default set` action.

The rollback manifest contains previous bindings. Review it with
`domain plan --file previous-binding.json --out rollback.plan.json`, then apply
that plan. It moves domains back; it does not remove new claims or restore
defaults. After an interrupted apply, inspect current bindings before preparing
a new review. The API replays cached responses for the same authenticated request
and Idempotency-Key; a committed change without a cached response makes its old
plan stale.

A serving deployment is control-plane evidence. Use guided production cutover
for public DNS/TLS, exact-deployment response and traffic-budget checks. Moves
change neither provider DNS nor deployment traffic and do not promise
uninterrupted application behavior.

## Verification

Custom domains are attached to an app after it has a live deployment.

`gregale domain verify <domain>` checks ownership and routing immediately and
reports DNS and HTTPS progress. Add `--wait --timeout 10m` to wait until the
domain is ready; `--poll-interval 5s` controls how often to check. The command
exits successfully only after ownership, routing, and an unexpired certificate
are confirmed. JSON mode emits one final response. Repeated manual checks have
a fifteen-second cooldown; automatic background checking continues independently.

Responses include independent `readiness.ownership`, `readiness.routing`, and
`readiness.tls` states, plus an overall status such as `awaiting_ownership`,
`fix_routing`, `preparing_https`, or `ready`. `checks` explains observed DNS
values and the records to change. `verification_next_check_at` and
`verification_attempts` describe automatic TXT checks. `cert_retry_at`, when
present, is the legacy issuer's next allowed attempt; Caddy owns its retry clock.
`domain get` displays these diagnostics and all required DNS records during setup.

The first conclusive routing failure reports the problem immediately. Two
consecutive failures revoke verification and mark the domain `dns_drifted`.
Healthy and inconclusive DNS observations interrupt this confirmation sequence.

```bash
gregale domain create --app my-api --domain api.example.com
gregale domain verify api.example.com
gregale domain list
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
gregale domain create --app my-api --domain staging.example.com --env staging
gregale environment diff shop --from production --to staging
```

Environment-bound domains follow only that environment's active project
release set. They are not used as the app's default domain, are shown in the
environment state and diff, and are not copied when cloning an environment
because their DNS and certificate ownership is external to Gregale.

`gregale domain doctor` checks DNS, certificate state, and the route without
changing the domain.

## Serve several apps on one domain

A domain is attached to one app, and edge rules send parts of it to other apps
in the same account. Declare the layout in `gregale.yaml` so it deploys with
the code. A reviewed deploy (`gregale deploy`, the dashboard) applies it; a
GitHub push that changes it deploys its services but not the routes, and a
failed `gregale / <project> (project settings)` check says to run a reviewed
deploy:

```yaml
routes:
  - name: api
    match_host: shop.example.com
    match_path: /api/**
    to: api              # app slug, or its workload name in a project deploy
    strip_prefix: /api   # optional: /api/items reaches the app as /items
  - name: hooks
    match_host: shop.example.com
    match_path: /hooks/**
    to: webhooks
    match_methods: [POST]
```

Requests that match no route go to the domain's own app, for example the web
frontend. Each route becomes a `kind=route` edge rule on its target app, plus a
`kind=rewrite` rule when `strip_prefix` is set; `gregale edge rule list --app
api` shows them. Route names are stable per target app: later deploys update a
route with the same name and remove manifest-owned routes that are no longer
declared. Rules you created yourself are never adopted or deleted; a manifest
route with the same kind, host, path and methods as one of them fails the
deploy until you remove one. Omitting `routes` leaves managed routes unchanged,
`routes: []` clears them, and `--no-triggers` skips them.

In a single-app deploy every route must target the app being deployed. In a
project deploy (`gregale deploy --project`) a route may name any workload, and
only selected workloads are reconciled. `match_path` is a glob (`/api/**`),
not an OpenAPI template. Routes are YAML-only. On an organization's apps and
projects, a deploy that adds, changes or removes routes needs the owner or
admin role, as creating edge rules does; a deploy that leaves them unchanged
needs only deploy access.

Without a manifest, create the same rules directly:

```bash
gregale edge rule create --app api --kind route --match-host shop.example.com \
  --match-path '/api/**' --route-target-slug api
gregale edge rule create --app api --kind rewrite --match-host shop.example.com \
  --match-path '/api/**' --rewrite-from /api --rewrite-to /
```

Wildcard domains (`*.example.com`, Pro and Scale) prove ownership with the
TXT record at `_faas-verify.example.com` and route with a wildcard CNAME.
Gregale checks the wildcard CNAME through `gregale-wildcard-check.example.com`,
which only the wildcard record answers, so other explicit records such as `www`
can point anywhere. Each subdomain gets its own certificate on its first HTTPS
connection; at most 40 new subdomains per wildcard get a certificate in any 7
days.
Subdomains that already have one keep it and renew normally.

## Wildcard host allowlist

Anyone can connect with a random name below your wildcard, and each new
name uses one of those 40 certificates. If you know which names you serve,
list them, and the edge refuses a certificate for any other name:

```bash
gregale domain host set '*.example.com' app api eu.shop
gregale domain host get '*.example.com'
gregale domain host clear '*.example.com'
```

A name is the part before the wildcard's suffix (`eu.shop` for
`eu.shop.example.com`); the full host works too. `set` replaces the list, up
to 100 names; `clear` removes it, so every name may get a certificate again.
Routing does not change; an unlisted name just cannot complete an HTTPS
handshake. A certificate the
edge already holds for a name you remove keeps serving until its next reload
or renewal, which is then refused. The domain doctor's probe name is always
allowed. API: `GET`, `PUT` and `DELETE /v1/domains/{domain}/hosts`. A
wildcard you take over from an unverified claim starts with no list.

Once a wildcard verifies, names below it belong to its account: another
account cannot add `api.example.com` or `*.shop.example.com` while
`*.example.com` is verified elsewhere (`409`). An exact domain you add below
your own wildcard takes over that name once it verifies; until then the name
is unrouted. An unverified domain releases its name after 7 days.

If another account has added your domain but not verified it, adding it
returns `409` with a TXT record to publish: the name
`_faas-verify.<domain>` (`_faas-verify.example.com` for `*.example.com`)
and a value tied to your account. Publish it and add the domain again. The
other account's unverified claim moves to your app, and verification
completes as usual. A verified domain cannot be taken this way. Tenant
hostnames on a tenant surface work the same, and a surface may hold at most
as many unverified hostnames as its plan allows verified ones.

The default `*.gregale.dev` hostname works without a custom domain. Domain
entitlement, certificate limits, and wildcard behavior are listed in
[plans](plans.md).
