# Edge rules

Edge rules change how the gateway handles your app's requests before they
reach the app: route, rewrite or redirect them, set headers and CORS, check
tokens, IP addresses and countries, cap body sizes, rate-limit, validate,
cache, and more. A rule that refuses a request answers it at the edge, so the
app never wakes for it.

```sh
# Redirect an old path, permanently.
gregale edge-rules create --app my-api --kind redirect \
  --match-host api.example.com --match-path /docs --redirect-status 308 --redirect-to https://docs.example.com/

# Allow at most 5 searches a second, with bursts of 10.
gregale edge-rules create --app my-api --kind throttle \
  --match-host @app --match-path '/search/**' \
  --throttle-requests-per-second 5 --throttle-burst 10

gregale edge-rules list --app my-api
```

Every flag is listed under [`gregale edge-rules create`](cli#edge-rules-create).
The API is `POST /v1/apps/{slug}/edge-rules`, `GET`, `PATCH` and `DELETE
/v1/edge-rules/{id}`.

## Matching

A rule applies to a request when every selector it sets matches.

| Field | Matches |
|---|---|
| `match_host` (required) | An exact host (`api.example.com`), `*` for any host, a glob such as `*.example.com` (subdomains, not the apex), or `@app` for every host the rule's app serves, including custom domains you add later. Hosts compare case-insensitively. |
| `match_path` | A path glob; see below. Defaults to `/`, which matches only the root path. |
| `match_methods` | HTTP methods. Empty matches any method. |
| `match_headers` | Up to 10 request headers that must be present with an exact value. Names compare case-insensitively; any value of a repeated header can match. |
| `match` | An optional condition over more request fields, ANDed with the selectors above. |

A rule only ever applies to hosts your account owns: another account's rule
never runs on your traffic, even with `match_host` `*`. A host pattern other
than `@app` covers every host of your account that it matches, including
hosts of your other apps. To keep a rule to one app, use `@app` or the app's
own hostnames.

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

Rules that refuse or limit requests (`ip`, `geo`, `jwt`, `limit`, `throttle`,
`validate`, `maintenance`, `waf`, `challenge`) also match the path's
normalized form, with `.` and `..` segments and repeated slashes resolved, and
compare it case-insensitively. `/public/../admin/x`, `//admin/x` and
`/ADMIN/x` are all covered by a rule on `/admin/*`, because many frameworks
serve them as `/admin/x`.

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

`in_list` names a reusable list on your account, so one list of office IPs or
blocked countries can serve many rules. A list has a kind (`ip`, `country`,
`host`, `string` or `asn`) that must fit the field it is used with. Editing a
list reaches every rule that uses it within seconds, and a list cannot be
deleted while a rule uses it.

```sh
gregale edge-rule-lists create office-ips --kind ip --item 203.0.113.0/24 --item 2001:db8::1
gregale edge-rule-lists update office-ips --add 198.51.100.7
```

Lists are available on Hobby and above; see [Limits](#limits).

## Which rule applies

Rules are evaluated in `priority` order, lowest first (0 to 10000, default
100); rules with the same priority run in the order they were created. For
each kind, the first matching rule acts and the rest of that kind are skipped.
Rules of different kinds combine: a request can be rewritten by one rule,
get headers from another and be rate-limited by a third.

A rule in `mode: log` is matched and counted but never acts, and it does not
stop an enforced rule of the same kind from acting. Start a risky rule in log
mode, check what it matched with `gregale edge-rules events --outcome logged`,
then switch it to `enforce`. `gregale edge-rules review` lists log-mode rules
that look ready.

A disabled rule (`enabled: false`) is skipped. A rule with `expires_at` stops
applying at that time, which suits temporary blocks:

```sh
gregale edge-rules create --app my-api --kind ip --match-host @app --match-path '/**' \
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
| [`retry`](#retry) | Tunes replay of requests that failed in transport | Hobby+ |
| [`circuit_breaker`](#circuit_breaker) | Tunes when a failing instance is taken out | Hobby+ |
| [`async`](#async) | Accepts the request and runs it later | Hobby+ |
| [`respond`](#respond) | Answers with fixed JSON (preview apps only) | All |
| `waf` | Checks requests against the OWASP Core Rule Set; see [Web application firewall](security#web-application-firewall) | Pro+ |
| `challenge` | Asks browsers for a proof of work; see [Browser challenge](security#browser-challenge) | Hobby+ |

`ip`, `geo`, `throttle` and blocking `waf` rules can also put a client they
keep refusing in a penalty box for the whole app; see
[Penalty box](security#penalty-box).

### route

```json
{"kind": "route", "match_host": "api.example.com", "match_path": "/legacy/**",
 "action": {"target_app_slug": "legacy-api"}}
```

The matched request is served by `target_app_slug`, which must be an app of
the same account. The path is unchanged; add a `rewrite` rule to change it.

### rewrite

```json
{"kind": "rewrite", "match_host": "@app", "match_path": "/api/**",
 "action": {"from": "/api", "to": "/v1"}}
```

`from` is replaced with `to` when the path starts with it: `/api/users`
becomes `/v1/users`. A `to` of `/` strips the prefix (`/api/users` becomes
`/users`, `/api` becomes `/`), and a `from` of `*` puts `to` in front of every
path. The client still sees its original URL. `from` is a plain prefix, so
pair it with a `match_path` that selects the requests you mean.

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

`Host`, `Content-Length`, `Transfer-Encoding`, `Connection` and any
`X-Faas-` header are reserved in any letter case; a rule that names one is
rejected. Request headers are changed before authentication. Use `set`, not
`add`, for a header your app relies on, so a client cannot supply its own
value alongside it. For common security headers,
[Security headers on your app](security#security-headers-on-your-app) sets a
recommended rule.

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
`allow_credentials`. `max_age_seconds` is at most 86400. Instead of inline
fields, `cors_preset_id` can name a CORS preset shared by several rules
(`/v1/cors-presets`). `gregale cors allow` creates a simple rule.

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

### geo

```json
{"kind": "geo", "match_host": "@app", "match_path": "/**",
 "action": {"deny": ["KP", "IR"]}}
```

`allow` and `deny` hold ISO 3166-1 alpha-2 codes and work like `ip`; refusals
get 403 `geo_denied`. A request whose country cannot be resolved (a private
address, an address in no country) passes: geo rules fail open.

### limit

```json
{"kind": "limit", "match_host": "@app", "match_path": "/upload",
 "action": {"max_body_bytes": 5242880}}
```

A request body over `max_body_bytes` (1 byte to 25 MiB) gets 413
`request_too_large`. A request whose `Content-Length` is over the cap is
refused before any of the body is read. `max_body_bytes_streaming`
(optional, at least `max_body_bytes`, at most 100 MiB) sets the size cap for
a streamed response on the route.

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

`validate_mode` decides what a failure does: `block` (default) answers 422
`request_validation_failed` with each error in `errors`; `observe` lets the
request through and counts it; `warn` also adds `X-Validation-Warning: <rule
id>` to the response. A `Content-Type` outside `content_types` gets 415.

The body is read into memory to validate it, up to `max_body_bytes`. Unset,
the cap is 25 MiB, or your plan's request body cap if that is lower. Set
`reject_on_unknown_fields` to reject properties the schema does not declare,
and `apply_while_streaming` to validate requests the gateway would otherwise
stream to the app.

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
gets 504 `request_budget_exceeded`. A client may lower or raise the execution
budget within the plan ceiling with the `x-faas-budget-ms` header, or the
header named in `allow_override_header`; it never extends
`total_deadline_ms`.

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
or `Accept-Encoding` to the cache key.

Requests with credentials (`Authorization`, cookies) are never cached or
served from the cache, and neither are responses with `Set-Cookie` or
`Cache-Control: no-store` or `private`. `gregale cache purge` removes cached
responses.

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
it for every instance, and selectors do not split it by route.

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
invocations.

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
While a change is being applied, requests to the hosts it covers get 503
`capacity_unavailable` ("Edge policy update in progress") with
`Retry-After: 1`. If the gateways cannot be reached, the change is refused
with 503 and nothing changes; retry it. Changes to one account's rules are
applied one at a time; other accounts' changes never wait for yours.

Two people editing the same app's rules at once cannot overwrite each other:
send the `ETag` from `GET /v1/apps/{slug}/edge-rules` as `If-Match`, and a
change made since answers 412.

### Versions and rollback

Every change records a version of the app's whole rule set.

```sh
gregale edge-rules history --app my-api              # versions
gregale edge-rules history --app my-api --version 12 # the rules in version 12
gregale edge-rules rollback --app my-api --to 12
```

A deployment also records the rules it went live with.

## Testing and observing

- `gregale edge-rules trace` shows which rules a request would match and
  what each would do, and can compare a proposed change; see
  [Reusable edge-rule trace scenarios](edge-rule-trace).
- Log mode counts what a rule would do without acting.
- `gregale edge-rules stats` shows match counts per rule for the last hour,
  day or week.
- `gregale edge-rules events` shows sampled requests each rule matched: up to
  10 per rule per minute, kept for your plan's event window.
- `gregale edge-rules summary` shows what the edge refused for an app, by
  reason and status.
- `gregale edge-rules suggest` proposes rules from your recent traffic, and
  `gregale edge-rules recipes` lists ready-made rules that start in log mode.

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

A rule past a count limit is refused with 402
`plan_edge_rule_kind_quota_reached` (or `plan_limit_edge_rules` for the rule
total), and a kind your plan does not include with 402
`plan_edge_rule_kind_not_allowed`.

## Responses from the edge

| Status and code | Sent by |
|---|---|
| 3xx | `redirect` |
| 202 | `async` |
| 204 | `cors` preflight |
| 401 `unauthorized` | `jwt`; `throttle` with `missing_key_policy: reject` |
| 403 `ip_denied` | `ip` |
| 403 `geo_denied` | `geo` |
| 403 `forbidden` | blocking `waf` |
| 403 `penalty_box` | a client in a penalty box, with `Retry-After` |
| 403 `challenge_required` | `challenge` on a request that is not a page load |
| 413 `request_too_large` | `limit`, `validate` |
| 415 `unsupported_media_type` | `validate` |
| 422 `request_validation_failed` | `validate` in `block` mode |
| 429 `rate_limited` | `throttle` |
| 503 `edge_rule_maintenance` | `maintenance` |
| 503 `capacity_unavailable` | a rule change being applied, rules the gateway cannot load, an unreachable JWKS |
| 504 `request_budget_exceeded` | `budget` |

Errors use the [problem format](errors); `gregale edge-rules events
--request-id <id>` finds the rule behind a refused request.
