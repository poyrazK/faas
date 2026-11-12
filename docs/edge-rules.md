# Edge rules

Edge rules change how the gateway handles your app's requests before they
reach the app: route, rewrite or redirect them, set headers and CORS, check
tokens, IP addresses and countries, cap body sizes, rate-limit, validate,
cache, and more. A rule that refuses a request answers it at the edge, so the
app never wakes for it.

```sh
# Redirect an old path, permanently.
gregale edge rule create --app my-api --kind redirect \
  --match-host api.example.com --match-path /docs --redirect-status 308 --redirect-to https://docs.example.com/

# Allow at most 5 searches a second, with bursts of 10.
gregale edge rule create --app my-api --kind throttle \
  --match-host @app --match-path '/search/**' \
  --throttle-requests-per-second 5 --throttle-burst 10

gregale edge rule list --app my-api
```

Every flag is listed under [`gregale edge rule create`](cli#edge-rules-create).
The API is `POST /v1/apps/{slug}/edge-rules`, `GET`, `PATCH` and `DELETE
/v1/edge-rules/{id}`.

## Matching

A rule applies to a request when every selector it sets matches.

| Field | Matches |
|---|---|
| `match_host` (required) | An exact host (`api.example.com`), `*` for any host, a glob such as `*.example.com` (subdomains, not the apex), or `@app` for every host the rule's app serves, including custom domains you add later. Hosts compare case-insensitively. |
| `match_path` | A path glob; see below. Defaults to `/`, which matches only the root path. |
| `match_methods` | HTTP methods: `GET`, `HEAD`, `POST`, `PUT`, `PATCH`, `DELETE`, `OPTIONS`, `CONNECT`, `TRACE`, in any letter case. Empty matches any method; `*` is rejected, so leave the field out instead. |
| `match_headers` | Up to 10 request headers that must be present with an exact value. Names compare case-insensitively; any value of a repeated header can match. |
| `match` | An optional condition over more request fields, ANDed with the selectors above. |

Your app is always served at its own `<slug>.gregale.dev` address, and at any
companion hostnames, as well as at its custom domains. A rule that protects
the app (`jwt`, `ip`, `geo`, `waf`, `challenge` and the other gates) should
use `@app`: one that names a single host leaves the app's other hosts open,
and `gregale edge rule create` warns when it does.

In an organization, members who can read an app read its rules, history,
events and traces; owners and admins create, change and delete them, roll
them back and switch attack mode, whoever created the app. The organization
also has its own lists and CORS presets: name one of its apps with `?app=<slug>`
on `/v1/edge-rule-lists` and `/v1/cors-presets` (`--app` on `gregale edge
rule-list`), or use the app's dashboard page. Members who can read the app
read them, and owners and admins change them. They are separate from each
member's personal lists and presets, which the organization never sees, and
the organization's plan sets their limits. A company app's rules name only
its organization's lists and the app's own presets, and a personal app's
rules only personal ones. Lists live with the account that holds the app's
rules, so a list created through one member's company app is not available
to an app another member created; create it with `--app` for that app.

A rule only ever applies to hosts your account owns: another account's rule
never runs on your traffic, even with `match_host` `*`. A host pattern other
than `@app` covers every host it matches among the apps with the same owner
as the rule's app: a rule on one of your personal apps covers your other
personal apps' hosts, and a rule on an organization's app covers that
organization's apps' hosts. It never crosses between the two, although apps
you create for an organization stay under your account. To keep a rule to one
app, use `@app` or the app's own hostnames.

A PR preview applies its production app's `@app` rules as well as its own, so
a `jwt`, `ip`, `geo`, `waf` or `challenge` rule on production also gates the
preview's hosts. `route` and `async` rules are the exception: a preview serves
its own code. An inherited `throttle` rule shares production's buckets.

Edge rules do not run for managed realtime connections
(`/__gregale/realtime/`), which authenticate with their own tokens, for
requests to a production fork, which carry the fork's token, or for the
`robots.txt` and `favicon.ico` answers the gateway serves itself when your
manifest configures them. The health-check answer and a parked app's `HEAD /`
answer come after the access rules (`ip`, `geo`, `jwt`, `maintenance`,
`challenge` and attack mode), so an uptime monitor of a gated app must be
admitted by those rules.

Edge rules apply to requests that arrive at your public hosts. A call from one
of your apps to another over the internal service network goes straight to the
target and skips the target's edge rules, `jwt`, `ip`, `waf`, `challenge` and
`throttle` included. Which apps may make such calls is set by the service
policy and `x-gregale-allow-callers` (see
[Internal services](networking.md#internal-services)); check credentials in the
app itself when a caller must prove more than which app it is.

Work the platform delivers on your account's behalf also skips edge rules:
`gregale invoke` and other synchronous or asynchronous invocations
(`POST /v1/apps/{slug}/invoke`), queue messages, delayed tasks, crons and
exclusive operations. These requests are authorized by your account's
credentials and scopes when they are created, not by the public edge, so an
`ip`, `geo`, `jwt` or `throttle` rule does not stop them. An app that must
reject them checks in its own code.

### Path globs

| Pattern | Matches |
|---|---|
| `/` | The root path only. |
| `/login` | Exactly `/login`. |
| `/api/*` | One or more segments beneath `/api`: `/api/v1`, `/api/v1/users`. Not `/api` itself. |
| `/api/**` | `/api` and everything beneath it. |
| `/**` or `*` | Every path. |
| `/users/?*` | One non-empty segment: `/users/7`, not `/users/7/avatar`. |

Other characters follow Go's `path.Match`: `*` matches within one segment,
`?` matches one character, `[...]` a character class. OpenAPI templates such
as `/users/{id}` are not globs; the API rejects them and suggests the glob
(`/users/?*`).

Before any rule runs, the gateway resolves `.` and `..` segments (also when
percent-encoded) and collapses repeated slashes, keeping a trailing slash,
and forwards that path to your app: `/public/../upload` and `//upload` are
`/upload` to every rule and to the app. A path already in that form is
forwarded unchanged. Backslashes, `;` parameters and letter case are left
as sent.

Rules that refuse or limit requests (`ip`, `geo`, `jwt`, `limit`, `throttle`,
`validate`, `maintenance`, `waf`, `challenge`) also match the path's
normalized form, with `.` and `..` segments and repeated slashes resolved, and
compare it case-insensitively. `/public/../admin/x`, `//admin/x` and
`/ADMIN/x` are all covered by a rule on `/admin/*`, because many frameworks
serve them as `/admin/x`. They also match the path with each segment's `;`
parameters removed, so `/admin;jsessionid=1/x` is covered too, as Java servlet
containers and Spring serve it as `/admin/x`. For the same reason, such a rule
that lists `GET` in `match_methods` also covers `HEAD`, which servers answer
with the `GET` handler, and one that lists `DELETE`, `PUT` or `PATCH` also
covers a request that asks the app to run it as that method through an
`X-HTTP-Method-Override`, `X-HTTP-Method` or `X-Method-Override` header or a
`_method` query parameter. A `_method` field in a form body is not read at
the edge: if your app honours one, leave `match_methods` off the rule. An app
that only allows its declared routes (`only_allow_declared_routes`) gets the
same treatment: a POST that asks to run as `DELETE` needs both `POST` and
`DELETE` declared for its path.

A `match` condition on these rules reads the same forms: it holds when it
holds for the raw path or any normalized form above, and on a `HEAD` request
also when it holds for `GET`. A `jwt` rule on `/**` with
`{"not": {"field": "path", "op": "prefix", "value": "/public"}}` therefore
still checks `/public/../admin`, and a `path prefix /admin` condition covers
`//admin` and `/ADMIN`. Other kinds compare conditions exactly.

A gate scoped by `match_headers`, or by a `header:`, `cookie:` or `query:`
field in `match`, applies only when the client sends that value, and the
client chooses what it sends: one that leaves the header out is not checked.
Scope gates by path, method, `client_ip`, `country` or `asn`;
`gregale edge rule create` warns when a gate reads client-chosen fields.

### Conditions

`match` is a tree of `all`, `any` and `not` nodes over leaves of the form
`{"field": ..., "op": ..., "value" | "values" | "list": ...}`:

```json
{"all": [
  {"field": "country", "op": "in", "values": ["US", "CA"]},
  {"not": {"field": "client_ip", "op": "in_list", "list": "office-ips"}},
  {"field": "header:x-beta", "op": "eq", "value": "1"}
]}
```

| Fields | Operators |
|---|---|
| `method`, `path`, `host`, `client_ip`, `country`, `asn`, `header:<name>`, `cookie:<name>`, `query:<name>` | `eq`, `ne`, `in`, `not_in`, `prefix`, `suffix`, `contains`, `exists`, `missing`, `regex` (RE2), `in_list`; `cidr` on `client_ip` |
| `ua_family` (`browser`, `mobile`, `bot`, `tool`, `other`), `verified_bot`, `ai_crawler`, `ai_crawler_purpose`, `verified_ai_crawler` | `eq`, `ne`, `in`, `not_in`, `exists`, `missing` |

A tree is at most 4 levels deep with 32 nodes; a leaf has at most 64 values;
values and regexes are at most 256 bytes. `asn` accepts `13335` or
`AS13335`. A client IP the gateway cannot trust, and a country or ASN it
cannot resolve, read as missing. `verified_bot` and the AI crawler fields are
described under [AI crawlers](security#ai-crawlers).

### Lists

`in_list` names a reusable list on your account (or your organization's, for a
rule on one of its apps), so one list of office IPs or blocked countries can
serve many rules. A list has a kind (`ip`, `country`,
`host`, `string` or `asn`) that must fit the field it is used with. Editing a
list is applied like a rule change (see [Changing rules](#changing-rules)),
and a list cannot be deleted while a rule uses it. A rule that names a list
your account does not have is rejected. If a rule's list cannot be loaded,
requests to the rule's hosts get 503 rather than skipping the rule.

```sh
gregale edge rule-list create office-ips --kind ip --item 203.0.113.0/24 --item 2001:db8::1
gregale edge rule-list update office-ips --add 198.51.100.7
gregale edge rule-list create team-ips --app company-api --kind ip --item 203.0.113.0/24
```

Lists are available on Hobby and above; see [Limits](#limits).

Edge rules do not apply to a public bucket mount's paths (`--serve-at`):
those reads are answered before the rules run. See
[object storage](object-storage.md#public-buckets).

## Which rule applies

Rules are evaluated in `priority` order, lowest first (0 to 10000, default
100); rules with the same priority run in the order they were created. For
each kind, the first matching rule acts and the rest of that kind are skipped.
Rules of different kinds combine: a request can be rewritten by one rule,
get headers from another and be rate-limited by a third.

A rule in `mode: log` is matched and counted but never acts, and it does not
stop an enforced rule of the same kind from acting. Start a risky rule in log
mode, check what it matched with `gregale edge rule events --outcome logged`,
then switch it to `enforce`. `gregale edge rule review` lists log-mode rules
that look ready.

A disabled rule (`enabled: false`) is skipped. A rule with `expires_at` stops
applying at that time, which suits temporary blocks:

```sh
gregale edge rule create --app my-api --kind ip --match-host @app --match-path '/**' \
  --ip-deny 192.0.2.0/24 --expires-in 2h --name "block scraper"
```

### Order of kinds

The gateway applies the kinds in this order. A rule that answers the request
ends it, and later kinds do not run.

1. `route` picks the app that serves the request.
2. The [penalty box](security#penalty-box) refuses clients a rule has boxed.
3. App maintenance mode, then `maintenance`.
4. `redirect`.
5. `rewrite`, then `headers` (request headers now, response headers on the way out).
6. `cors` (preflight requests are answered here).
7. The app's ingress allowlist and internal-only setting, then `ip`, then `geo`.
8. `jwt`.
9. `limit`, then the plan's body size cap.
10. `throttle`.
11. `waf`.
12. `validate`.
13. `challenge`.
14. `respond`.
15. `cache` and `async`.
16. `budget`, `retry` and `circuit_breaker` while the request is forwarded.

`ip`, `geo`, `jwt`, `throttle`, `waf` and `challenge` match the path the
client sent, and the rewritten path when a `rewrite` changed it: a gate on
`/api/**` keeps applying after a rewrite takes `/api` away, and a gate written
for the rewritten path still applies. When the two paths match different
rules of a kind, both apply, the client path's first. The kinds after them
(`limit`, `validate`, `respond`, `cache`, `async`) see the rewritten path.

Refusals are ordered from cheapest to most expensive, so a request blocked by
IP or country never costs a token check or a body read, and a request
refused for its size never uses a throttle token.

## Rule kinds

Each rule has one `kind` and an `action` object whose fields depend on it.
`kind` cannot be changed after creation; `PATCH` replaces `action` whole.

| Kind | Does | Plans |
|---|---|---|
| [`route`](#route) | Serves the request from another of your apps | All |
| [`rewrite`](#rewrite) | Changes the path the app sees | All |
| [`redirect`](#redirect) | Answers with a redirect | All |
| [`headers`](#headers) | Sets, adds or removes request and response headers | All |
| [`cors`](#cors) | Answers preflights and adds CORS headers | All |
| [`jwt`](#jwt) | Requires a valid bearer JWT | Hobby+ |
| [`ip`](#ip) | Allows or denies client IP ranges | Hobby+ |
| [`geo`](#geo) | Allows or denies countries | All |
| [`limit`](#limit) | Caps the request body size | All |
| [`throttle`](#throttle) | Rate-limits a route | All |
| [`validate`](#validate) | Checks the body, path, query and headers against JSON Schema | All |
| [`maintenance`](#maintenance) | Answers 503 for a route | All |
| [`budget`](#budget) | Sets a route's time budget | All |
| [`cache`](#cache) | Caches responses at the edge | Hobby+ |
| [`retry`](#retry) | Tunes replay of requests that failed in transport | Hobby+, on request* |
| [`circuit_breaker`](#circuit_breaker) | Tunes when a failing instance is taken out | Hobby+, on request* |
| [`async`](#async) | Accepts the request and runs it later | Hobby+, preview* |
| [`respond`](#respond) | Answers with fixed JSON (preview apps only) | All |
| `waf` | Checks requests against the OWASP Core Rule Set; see [Web application firewall](security#web-application-firewall) | Pro+ |
| `challenge` | Asks browsers for a proof of work; see [Browser challenge](security#browser-challenge) | Hobby+ |

\* `retry` and `circuit_breaker` are not yet generally available, and `async`
is in preview. Each is enabled per account: until it is enabled for yours,
creating such a rule returns 403 `capability_not_released`. Contact support
to have one enabled.

`ip`, `geo`, `throttle` and blocking `waf` rules can also put a client they
keep refusing in a penalty box for the whole app; see
[Penalty box](security#penalty-box).

### route

```json
{"kind": "route", "match_host": "api.example.com", "match_path": "/legacy/**",
 "action": {"target_app_slug": "legacy-api"}}
```

The matched request is served by `target_app_slug`, which must be an app of
the same account and owner: one of your personal apps for a rule on a personal
app, or an app of the same organization for a rule on an organization's app.
The path is unchanged; add a `rewrite` rule to change it.
Renaming the target app updates the route rules that name it. A deploy
token, or another credential bound to one app, may write a route rule only
to that app itself; routing to another app needs an account API key.

The rest of a routed request runs as the target app's: its `@app` rules
apply, and the source app's `@app` rules do not, except the source app's
attack mode, which challenges routed requests too. Rules written for the host
itself (an exact host, `*` or a glob) still apply. To gate routed paths too,
put the gate on the host, or on the target app's `@app`.

### rewrite

```json
{"kind": "rewrite", "match_host": "@app", "match_path": "/api/**",
 "action": {"from": "/api", "to": "/v1"}}
```

`from` is replaced with `to` when the path starts with it: `/api/users`
becomes `/v1/users`. A `to` of `/` strips the prefix (`/api/users` becomes
`/users`, `/api` becomes `/`), and a `from` of `*` puts `to` in front of every
path. The client still sees its original URL. `from` is a plain prefix, so
pair it with a `match_path` that selects the requests you mean. Access gates
(`ip`, `geo`, `jwt`, `throttle`, `waf`, `challenge`) match both the original
and the rewritten path; see [Order of kinds](#order-of-kinds).

### redirect

```json
{"kind": "redirect", "match_host": "old.example.com", "match_path": "/**",
 "action": {"status_code": 308, "to": "https://new.example.com${path}", "template": true}}
```

`status_code` is 301, 302, 307 or 308; use 307 or 308 to keep the method and
body. `headers` adds headers to the redirect response. Without `template`,
`to` is used as written. With `template: true`, `${name}` in `to` and in
header values expands to a request value: `host`, `path`, `method`, `query`,
`client_ip`, `country`, `asn`, `request_id`, `header:<name>`,
`query:<name>` or `cookie:<name>` (`$$` is a literal `$`). A templated
target must start with `/` or `http(s)://`, and only `${host}` may appear
before the path.

### headers

```json
{"kind": "headers", "match_host": "@app", "match_path": "/**",
 "action": {
   "request_headers":  [{"name": "X-Client-Country", "value": "${country}", "action": "set", "template": true}],
   "response_headers": [{"name": "Strict-Transport-Security", "value": "max-age=31536000", "action": "set"},
                        {"name": "Server", "action": "remove"}]}}
```

Each side holds up to 32 operations. `set` replaces a header, `add` appends a
value and `remove` deletes it. Names must be valid HTTP header names of at
most 64 characters, and values at most 4096 characters without control
characters. `template` expands `${name}` values as for `redirect`; each
expanded value is capped at 1 KiB and loses control characters.

`Host`, `Content-Length`, `Transfer-Encoding`, `Connection`,
`X-Forwarded-For`, `Forwarded` and any `X-Faas-` header are reserved in any
letter case; a rule that names one is rejected. The client address that IP,
`geo`, `throttle` and `challenge` rules use, and that your app receives in
`X-Faas-Client-Ip` and `X-Real-IP`, is the one the request arrived with,
whatever a `headers` rule does. A `Set-Cookie` value, here or in a
`redirect` rule's headers, loses a `Domain` attribute that names the shared
apps or platform domain, as your app's own cookies do, so the cookie stays
with the host that set it; your custom domain's `Domain` is kept. Request headers are changed before authentication. Use `set`, not
`add`, for a header your app relies on, so a client cannot supply its own
value alongside it. For common security headers,
[Security headers on your app](security#security-headers-on-your-app) sets a
recommended rule.

#### Credentials in request headers

A request header that carries a credential, such as the token your origin
expects, is stored encrypted and never shown again:

```json
{"name": "Authorization", "action": "set", "value": "Bearer <token>", "secret": true}
```

Only the gateway that applies the rule can read the value. Rules, versions,
exports, the dashboard and agent tools show `"secret": true` and a
`secret_id` instead of the value. `Authorization`, `Proxy-Authorization`,
`Cookie` and any header whose name contains `token`, `secret`, `password`,
`api-key`, `apikey`, `signature` or `credential` are kept this way without
`secret`. To keep a stored value when you change a rule, send the op back with
its `secret_id` and no `value`; to replace it, send a new `value`. Sealed
values are at most 4096 bytes and work on request headers only, not with
`template` or `remove`.

A credential-named value saved before sealing arrived is shown with an empty
`value` and `"redacted": true`. The platform seals such values in your rules
and their saved versions on its own, which adds a rule version with the same
headers; sending the op back unchanged seals the value right away. During a
platform upgrade a new value can briefly be stored this way too, until every
edge server can read sealed values.

Environment edge policies seal credential values the same way. In an
environment managed from Git, values stay as your repository defines them;
responses still leave them out.

### cors

```json
{"kind": "cors", "match_host": "api.example.com", "match_path": "/**",
 "action": {"allow_origins": ["https://app.example.com", "https://*.example.com"],
            "allow_methods": ["GET", "POST"], "allow_headers": ["Authorization", "Content-Type"],
            "allow_credentials": true, "max_age_seconds": 600}}
```

A matched `OPTIONS` preflight is answered with 204 at the edge, without
waking the app; other requests from an allowed origin get the CORS response
headers. An origin is
`*`, `https://host[:port]`, `https://*.host` for subdomains, or
`https://host:*` for any port. `*` cannot be combined with
`allow_credentials`, and neither can a subdomain wildcard under a Gregale
domain (`https://*.<your apps domain>`): other customers' apps are served
there. A wildcard directly under a public suffix (`https://*.com`,
`https://*.co.uk`, `https://*.github.io`, `https://*.vercel.app`) cannot be
combined with `allow_credentials` either, because anyone can register a name
there; use a wildcard under your own domain (`https://*.example.com`). List
your app origins instead. `max_age_seconds` is at most 86400.
Instead of inline fields, `cors_preset_id` can name a CORS preset shared by
several rules (`/v1/cors-presets`): an account-wide preset or one scoped to
the rule's app. Editing a preset is applied like a rule change to every rule
that uses it, and a preset rules use cannot be deleted. `gregale edge cors allow`
creates a simple rule.

### jwt

```json
{"kind": "jwt", "match_host": "@app", "match_path": "/api/**",
 "action": {"issuer": "https://idp.example.com/", "jwks_url": "https://idp.example.com/.well-known/jwks.json",
            "algorithms": ["RS256"], "audience": ["my-api"], "required_claims": {"tenant": "acme"}}}
```

A request needs `Authorization: Bearer <token>` signed by a key from
`jwks_url` (HTTPS) with one of `algorithms` (`RS256`, `RS384`, `RS512`,
`ES256`, `ES384`, `ES512`), the given `issuer`, one of `audience` when set,
and each of `required_claims` as a string claim with exactly that value. A missing or invalid token
gets 401. The key's `alg` must match the token's, and encryption keys are not
used for signatures.

The app receives the caller the rule verified as `X-Faas-Edge-Jwt-Subject`
(the token's `sub`) and `X-Faas-Edge-Jwt-Issuer` (the rule's `issuer`). Only
the gateway sets them: a client's own copies never reach the app, and a value
longer than 1 KiB or one HTTP cannot carry is not forwarded. A request an
[`async`](#async) rule queues gets them too.

A new or edited rule needs `audience` or at least one `required_claims`
entry. Without one, every token the issuer signs would pass, and a shared
issuer (GitHub Actions, Google) signs tokens for anyone who asks. Rules saved
before this check keep working until you edit them.

`require_exp` defaults to true: a token without an `exp` claim is rejected,
because it never expires. Set `require_exp: false` to accept such tokens.
Rules saved before this default keep their saved value.

`mcp` turns the rule into an MCP resource-server policy, and
`platform_tenant_external_ref_claim` maps a claim to a
[platform tenant](platform-tenants). If the gateway cannot load the JWKS, the
request gets 503 rather than passing unchecked.

### ip

```json
{"kind": "ip", "match_host": "@app", "match_path": "/admin/**",
 "action": {"allow": ["203.0.113.0/24"], "deny": ["203.0.113.66/32"]}}
```

With `allow` set, only clients inside those ranges pass. `deny` is checked
after `allow`, so a denied address inside an allowed range is still refused.
Refusals get 403 `ip_denied`. The client IP is the one the platform's proxy
chain vouches for, not a `X-Forwarded-For` value the client sent. For lists
of ranges you reuse, use a `match` condition with `in_list`.

The CDN in front of Gregale caches responses by URL, so a response one
client was allowed to see could otherwise reach clients the rule refuses.
Responses to requests an `ip`, `geo` or `jwt` rule covers, and to requests
that carried a verified key or token, are sent with
`Cloudflare-CDN-Cache-Control: private` and `CDN-Cache-Control: private`, so
the CDN does not store them; how browsers cache them is unchanged.

### geo

```json
{"kind": "geo", "match_host": "@app", "match_path": "/**",
 "action": {"deny": ["KP", "IR"]}}
```

`allow` and `deny` hold ISO 3166-1 alpha-2 codes and work like `ip`; refusals
get 403 `geo_denied`. An address the country database places in no country
(a reserved range, or one allocated after the database was built) is in no
list: a rule with only `deny` lets it through, and a rule with `allow`
refuses it with 403 `geo_denied`. If the database cannot be read, a matched
geo rule answers 503 rather than skip the check. Countries come from the
DB-IP country database the platform installs and updates monthly, not from
headers the client or a CDN sends.

### limit

```json
{"kind": "limit", "match_host": "@app", "match_path": "/upload",
 "action": {"max_body_bytes": 5242880}}
```

A request body over `max_body_bytes` (1 byte to 25 MiB) gets 413
`request_too_large`. A request whose `Content-Length` is over the cap is
refused before any of the body is read. `max_body_bytes_streaming`
(optional, at least `max_body_bytes`, at most 100 MiB) is the request body cap
instead of `max_body_bytes` when your app has streaming turned on: every
request to such an app streams except WebSocket upgrades, whatever its
`Accept` header, so on a streaming app this is the cap that applies. Leave it
unset to keep `max_body_bytes` there too. It also caps the streamed response.

### throttle

```json
{"kind": "throttle", "match_host": "@app", "match_path": "/login",
 "action": {"requests_per_second": 0.1, "burst": 5, "key_by": "composite",
            "key_fields": ["ip", "header:x-username"], "count_statuses": [401]}}
```

A token bucket refilled at `requests_per_second` and holding `burst` tokens.
A request that finds the bucket empty gets 429 `rate_limited`; responses on
the route carry `X-RouteRateLimit-Limit`, `-Remaining`, `-Reset` and
`-Policy`. Both values must stay within your plan's rate limit (see
[Limits](#limits)): a throttle rule can only tighten it.

`key_by` decides who shares a bucket:

| `key_by` | One bucket per |
|---|---|
| empty or `none` | route (every caller shares it) |
| `ip` | client IP (IPv6 by /64) |
| `country` | client country |
| `api_key` | API key |
| `consumer_id` | API consumer, across its rotated keys |
| `jwt_subject` | JWT `sub` |
| `jwt_claim` | value of the claim named in `jwt_claim_name` |
| `composite` | combination of `key_fields` (up to 4 of `ip`, `country`, `api_key`, `consumer_id`, `jwt_subject`, `jwt_claim`, `method`, `path`, `header:<name>`) |

A rule keeps at most `max_keys_per_rule` buckets (default and maximum: your
plan's cap); callers past it share one overflow bucket, which still limits
them. When the identity a key needs is missing (no API key, no token),
`missing_key_policy: shared` (default) puts those requests in one shared
bucket and `reject` answers 401.

With `count_statuses`, every request is admitted while the bucket has a
token, but only responses with those statuses use one. `[401]` limits failed
sign-ins without limiting successful ones.

Buckets are counted across every gateway that serves your app, so the limit
holds for the app as a whole, not per gateway. With `count_statuses`,
gateways admit against the last shared balance they saw, so a burst can
briefly go past the limit.

### validate

```json
{"kind": "validate", "match_host": "@app", "match_path": "/users/?*", "match_methods": ["PUT"],
 "validate_mode": "block",
 "action": {"schema": {"type": "object", "required": ["email"],
                       "properties": {"email": {"type": "string", "format": "email"}}},
            "content_types": ["application/json"],
            "parameters": {"path_template": "/users/{id}",
                           "path": {"type": "object", "properties": {"id": {"type": "integer"}}}}}}
```

`schema` is a JSON Schema (Draft 2020-12, at most 64 KiB, no external
`$ref`) for the body. `parameters` holds schemas for path, query and header
values, which are converted to the declared types before checking.
`path_template` requires `match_path` to be the template with each
placeholder as `?*`. A rule needs `schema`, `parameters` or both.

Numeric checks preserve the exact JSON value, including integers above
2^53. To bound validation arithmetic, each numeric token is at most 4096
bytes and its decimal exponent is between -4096 and 4096. These limits
apply to schemas and request values; values beyond them are validation failures.
Documents containing many large-exponent numbers also have an aggregate numeric
work allowance, so splitting a large expansion across many values cannot bypass
the bound.

`validate_mode` decides what a failure does: `block` (default) answers 422
`request_validation_failed` with each error in `errors`; `observe` lets the
request through and counts it; `warn` also adds `X-Validation-Warning: <rule
id>` to the response. A `Content-Type` outside `content_types` gets 415.

The body is read into memory to validate it, up to `max_body_bytes`. Unset,
the cap is 25 MiB, or your plan's request body cap if that is lower. Each
gateway bounds the request bodies it holds in memory at once, and one
customer's apps hold at most a third of that bound and one client address at
most four bodies, so other customers' uploads cannot use it up. A body sent
without `Content-Length` is held as it arrives. When a bound is reached, a
request gets 503 `capacity_unavailable` with `Retry-After`. Set
`reject_on_unknown_fields` to reject properties the schema does not declare,
and `apply_while_streaming` to validate a WebSocket handshake too. A body that
names a property twice in one object, or in two spellings that differ only in
case (`role` and `ROLE`), fails with `expected: unique_properties`: Go and
ASP.NET Core bind property names case-insensitively, so the second spelling
would reach your app unchecked. Query parameters are read as apps read them,
split on `&` only, so `?id=1;--` is checked as `id=1;--`; a query with a
percent-escape that does not decode fails with `expected: query_string`. Path,
query and header parameters are always checked, and so is any body sent with
`Connection: Upgrade`: servers that do not speak the named protocol run such a
request as usual.

### maintenance

```json
{"kind": "maintenance", "match_host": "@app", "match_path": "/billing/**",
 "action": {"retry_after_seconds": 600, "message": "Billing is down for an upgrade until 14:00 UTC."}}
```

Matched requests get 503 `edge_rule_maintenance` with `Retry-After` (0 means
the default of 60 seconds; at most 86400) and `message` (at most 512 bytes)
as the problem detail. To take the whole app down, use the app's maintenance
mode instead.

### budget

```json
{"kind": "budget", "match_host": "@app", "match_path": "/reports/**",
 "action": {"budget_ms": 120000, "total_deadline_ms": 150000}}
```

`budget_ms` is how long the app may take once the request has been uploaded,
the app woken and capacity found. `total_deadline_ms` (optional) bounds the
whole request from arrival, including upload, wake and queueing. Both are
capped by your plan: 30 s on Free, 60 s on Hobby, 5 minutes on Pro and
15 minutes on Scale. A request past its budget before the response starts
gets 503 `request_budget_exceeded`. A client may lower the execution budget
with the `x-faas-budget-ms` header. To let callers also raise it, within the
plan ceiling, name a header in `allow_override_header` that only your trusted
callers send; any caller can send `x-faas-budget-ms`, so on its own it never
raises the rule's budget. Neither header extends `total_deadline_ms`.

### cache

```json
{"kind": "cache", "match_host": "@app", "match_path": "/catalog/**",
 "action": {"max_age_seconds": 60, "stale_while_revalidate_seconds": 30, "stale_if_error_seconds": 300}}
```

`GET` and `HEAD` responses with a 2xx or 3xx status are kept for
`max_age_seconds` (at most 3600; 0 means 60) and served without waking the
app. After that,
`stale_while_revalidate_seconds` (at most 300) serves the old response while
one request refreshes it, and `stale_if_error_seconds` (at most 300; 0
turns it off) serves it when the app fails. `vary_on` adds `Accept-Language`
or `Accept-Encoding` to the cache key. Responses over 1 MiB are not cached,
and each gateway keeps at most 16 MiB of one app's responses: past that, the
app's least recently used responses are dropped first.

Requests with credentials (`Authorization`, `Proxy-Authorization`, `X-API-Key`, cookies) are never cached or
served from the cache, and neither are responses with `Set-Cookie` or
`Cache-Control: no-store` or `private`. `gregale edge cache purge` removes cached
responses. Changing an edge rule also clears the cached responses of the
apps it covers; a `*` or glob host pattern clears all of your account's
apps.

### retry

```json
{"kind": "retry", "match_host": "@app", "match_path": "/**",
 "action": {"max_attempts": 2, "budget_percent": 10}}
```

When an instance dies before answering, the gateway replays the request on
another healthy instance. Only transport failures are replayed: an app that
answered 5xx has run the request, and replaying it would repeat its side
effects. `max_attempts` counts the original (2 or 3). `POST` and `PATCH` are
replayed only with `allow_non_idempotent: true` and an `Idempotency-Key`
header your app honours. A replay also needs at least `min_remaining_ms`
(default 250) of the request's budget left, and stays within
`budget_percent` of the app's requests (default 10%) so an outage cannot
double your traffic. `backoff_ms` delays the replay.

### circuit_breaker

```json
{"kind": "circuit_breaker", "match_host": "@app", "match_path": "/**",
 "action": {"failure_threshold": 0.5, "min_requests": 20, "window_seconds": 30}}
```

Every app already has a breaker that stops sending requests to an instance
that keeps failing; this rule only tunes it. An instance is taken out when at
least `min_requests` requests in `window_seconds` failed at
`failure_threshold` or more (a ratio). It gets one probe request after
`open_seconds`, then twice as long after each failed probe, up to
`max_open_seconds`. The breaker is per app: the highest-priority rule tunes
it for every instance, and selectors do not split it by route. Independently
of the breaker, an instance that fails at the transport layer gets no
traffic for 30 seconds, so an `open_seconds` below 30 shows only after
repeated failures. A rule change reaches the breaker within 30 seconds.

### async

```json
{"kind": "async", "match_host": "@app", "match_path": "/jobs/**", "match_methods": ["POST"],
 "action": {"retry_policy": {"max_attempts": 5}, "max_age_seconds": 3600}}
```

The gateway stores the request and answers 202 with
`{"id": ..., "status_url": "/v1/invocations/<id>"}` without waking the app;
the app receives the original method, path, JSON body and safe headers later.
`on_success` and `on_failure` name webhook subscriptions that receive the
outcome. `retry_policy` and `max_age_seconds` override the app's defaults
within your plan's limits. The app's workload must accept request
invocations. The stored request keeps no `Authorization` or `Cookie` header;
when a `jwt` rule verified the caller, the app receives it at delivery (and on
replays) as `X-Faas-Edge-Jwt-Subject` and `X-Faas-Edge-Jwt-Issuer`.

### respond

```json
{"kind": "respond", "match_host": "@app", "match_path": "/api/me",
 "action": {"status_code": 200, "body": {"id": "demo", "name": "Preview user"}}}
```

Answers with `status_code` (200 to 599) and a JSON `body` of at most 64 KiB,
so a preview frontend can be built before its endpoint exists. Only preview
apps accept this kind.

## Changing rules

Creating, changing or deleting a rule returns once the gateways serving your
app have the new rules, and from then on no gateway serves the old ones.
While a change is being applied, your hosts keep serving: each gateway uses
the rules it has until it takes the new ones, so for a second or two some
requests run under the old rules and some under the new. Each request runs
under one set from start to finish. The one exception is a `route` rule
whose target app is being renamed: until the change lands, its requests get
503 `traffic_policy_unavailable` with `Retry-After: 1` rather than being
served by the app the host belongs to. If the gateways cannot be reached,
the change is refused with 503 and nothing changes; retry it. Changes to one account's rules are
applied one at a time; other accounts' changes never wait for yours.

A deploy token or a project-limited agent key manages only rules for its
own apps' hosts. It cannot change, delete or roll back over a rule whose
`match_host` covers other hosts (`*`, a wildcard, another app's host); use an
account API key for those. See [deploy tokens](deploys.md#deploy-tokens).

Two people editing the same app's rules at once cannot overwrite each other:
send the `ETag` from `GET /v1/apps/{slug}/edge-rules` as `If-Match`, and a
change made since answers 412.

### Versions and rollback

Every change records a version of the app's whole rule set.

```sh
gregale edge rule history --app my-api              # versions
gregale edge rule history --app my-api --version 12 # the rules in version 12
gregale edge rule rollback --app my-api --to 12
```

A rollback checks every restored rule as a new rule is checked: a version is
refused if one of its rules uses a kind your plan no longer includes, a
feature your account no longer has, a list that was deleted, or a route to an
app that is now internal. A deployment also records the rules it went live
with.

Deleting an app keeps its rules, so restoring the app brings them back, but
they stop applying within 30 seconds, including rules on `*` or a wildcard
that covered your other apps' hosts. A restored app's rules apply again within
the same time.

## Testing and observing

- `gregale edge rule trace` shows which rules a request would match and
  what each would do, and can compare a proposed change; see
  [Reusable edge-rule trace scenarios](edge-rule-trace). It includes the
  `*` and host-pattern rules of your other apps with the same owner that
  cover the host, as the gateway applies them; `GET
  /v1/apps/{slug}/edge-rules?covering=true` lists them with each rule's
  `app_id`.
- Log mode counts what a rule would do without acting.
- `gregale edge rule stats` shows match counts per rule for the last hour,
  day or week.
- `gregale edge rule events` shows sampled requests each rule matched: up to
  10 per rule per minute, kept for your plan's event window.
- `gregale edge rule summary` shows what the edge refused for an app, by
  reason and status.
- `gregale edge rule suggest` proposes rules from your recent traffic, and
  `gregale edge rule recipes` lists ready-made rules that start in log mode.

## Limits

| | Free | Hobby | Pro | Scale |
|---|---|---|---|---|
| Rules per app | 5 | 25 | 100 | 500 |
| `geo` rules per app | 1 | 5 | 25 | 100 |
| `throttle` rules per app | 1 | 5 | 25 | 100 |
| `cache` rules per app | 0 | 1 | 5 | 20 |
| `retry` rules per app | 0 | 3 | 10 | 25 |
| `circuit_breaker` rules per app | 0 | 3 | 10 | 25 |
| `challenge` rules per app | 0 | 3 | 10 | 25 |
| `waf` rules per app | 0 | 0 | 5 | 20 |
| `jwt`, `ip` and `async` rules | No | Yes | Yes | Yes |
| Throttle rate, requests per second | 5 | 20 | 100 | 500 |
| Throttle burst | 20 | 100 | 500 | 2000 |
| Throttle buckets per rule | 100 | 1000 | 5000 | 10000 |
| Lists per account | 0 | 5 | 20 | 100 |
| Items per list | 0 | 100 | 1000 | 10000 |
| Event history | 24 hours | 72 hours | 7 days | 7 days |

A rule past a count limit is refused with 403
`plan_edge_rule_kind_quota_reached` (or `plan_limit_edge_rules` for the rule
total), a kind your plan does not include with 402
`plan_edge_rule_kind_not_allowed`, and a kind not enabled for your account
with 403 `capability_not_released`.

After a downgrade, rules your new plan would not allow keep applying, so a
gate you set up does not open on its own; new rules of those kinds are
refused until the plan allows them again. Delete the ones you no longer need.

## Responses from the edge

| Status and code | Sent by |
|---|---|
| 3xx | `redirect` |
| 202 | `async` |
| 204 | `cors` preflight |
| 401 `unauthorized` | `jwt`; `throttle` with `missing_key_policy: reject` |
| 403 `ip_denied` | `ip` |
| 403 `geo_denied` | `geo` |
| 403 `waf_blocked` | blocking `waf` |
| 403 `penalty_box` | a client in a penalty box, with `Retry-After` |
| 403 `challenge_required` | `challenge` on a request that is not a page load |
| 413 `request_too_large` | `limit`, `validate` |
| 415 `unsupported_media_type` | `validate` |
| 422 `request_validation_failed` | `validate` in `block` mode |
| 429 `rate_limited` | `throttle` |
| 503 `edge_rule_maintenance` | `maintenance` |
| 503 `capacity_unavailable` | a rule change being applied, rules the gateway cannot load, an unreachable JWKS |
| 503 `traffic_policy_unavailable` | a rule on the host the gateway cannot enforce; the detail names it, and `gregale edge rule list` warns about it |
| 503 `request_budget_exceeded` | `budget` |

Errors use the [problem format](errors); `gregale edge rule events
--request-id <id>` finds the rule behind a refused request.
