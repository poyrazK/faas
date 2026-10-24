# Security

Gregale isolates workloads in Firecracker-backed microVMs and applies tenant, network, and resource boundaries at the platform edge. You still own application authorization, dependency hygiene, and data classification.

Use scoped, expiring tokens; MFA for interactive accounts; secret storage for credentials; and digest-pinned images for production. Review audit events after identity, billing, registry, or egress changes. Do not put secrets in source, logs, alert URLs, support tickets, or image layers.

## App deploy guard

Every app exposes a read-only security posture report at
`GET /v1/apps/{slug}/security`. An administrator can set the deploy-time
policy through the same MFA-protected surface (or with
`gregale app <slug> security --security-policy=enforce`):

- `off` reports findings without affecting deploys (the default).
- `warn` keeps deploys flowing while making the policy explicit for staged
  rollout.
- `enforce` rejects new deploys while high-severity configuration findings
  remain, such as anonymous access or credentialed wildcard CORS.

The report also checks every currently live image for usable, digest-matched,
recent scanner evidence and flags missing, incomplete, stale, or blocking
results with `image_scan_*` finding codes.
These live-image findings stay visible during replacement deploys so a clean
image can be admitted to remediate the currently serving one.

For `enforce` apps, an image promotion also requires a fresh, complete scan
whose recorded image reference matches the deployment. Failed, incomplete,
unmatched, or high/critical/unknown-severity scan results fail the deployment
before snapshotting. This proves that the scanned artifact is the one being
promoted. Enforce mode also requires a trusted signature for OCI images, so a
clean image cannot be promoted when its publisher provenance is unknown. This
does not replace dependency patching or application review.

When a trusted publisher is removed or its key no longer validates a live
image, imaged rechecks the active deployment and quarantines the enforce-mode
app instead of grandfathering the old provenance. A fresh image signed by an
active trusted publisher is required before recovery.

Use `gregale app <slug> security --posture` in CI before enabling enforcement.

## Optional pre-auth source limit

The gateway can rate-limit each client address after hostname routing and
before consumer-key lookup, JWT verification, body admission, or VM wake.

New apps start with this guard in `observe` mode at 10 requests/s and burst 20
per source, clamped to the plan's request rate and burst
([ADR-829](adr/829-default-pre-auth-observe.md)). Observe never rejects a
request, so check the recorded would-block traffic before switching to
`enforce`. `GET /v1/apps/{slug}/pre-auth-observations?range=24h` includes a
`suggestion` (`ready`, `review`, or `insufficient_data`), also shown on the
dashboard's pre-auth page. From the CLI:

```sh
gregale app my-api --pre-auth enforce                 # prints the 24h check first
gregale app my-api --pre-auth-rps 20 --pre-auth-burst 40
gregale app my-api --pre-auth off
```

To choose different values, or to opt out with `{"mode":"off"}`, set
`pre_auth_rate_limit` when creating the app or through
`PATCH /v1/apps/{slug}`:

```json
{"pre_auth_rate_limit":{"mode":"observe","requests_per_second":2,"burst":4}}
```

`observe` records requests that would exceed the source limit without
rejecting them. Change `mode` to `enforce` to return `429` with
`Retry-After: 1` and `x-faas-rate-limit-scope: pre-auth`. Set `mode` to `off`
to disable the guard. Apps created before ADR-829 keep their stored setting;
on those apps the field is absent and the guard is disabled until set.
The rate and burst must be positive and no greater than the app plan's
request rate and burst.
If the app moves to a lower plan, the gateway clamps an existing setting to
the new plan ceiling.
To be notified when the guard blocks or would block a burst of requests,
enable the webhook-only [`pre_auth_pressure` alert preset](alerts.md).
`gregale edge-rules summary --app my-api` shows pre-auth blocks alongside
validation failures and rejections by the other edge gates.

Up to 16 exact public method/path overrides can add stricter limits for
sensitive endpoints. For example, a login endpoint can allow fewer requests
from one source than the rest of the app:

```json
{"pre_auth_rate_limit":{"mode":"observe","requests_per_second":20,"burst":40,"routes":[{"method":"POST","path":"/login","requests_per_second":2,"burst":4}]}}
```

Each route has its own source bucket, while every request also consumes the
app-wide source bucket. Paths are matched before edge rewriting, after
normalizing the decoded URL path. Configure the path your public client sends;
route matching does not understand application route parameters. Route limits
return `x-faas-rate-limit-scope: pre-auth-route` when enforced. Shared NAT
addresses still share a bucket, so start in `observe` mode.

An exact route can opt into a fleet-wide request budget with
`"coordination":"central"`:

```json
{"pre_auth_rate_limit":{"mode":"observe","requests_per_second":20,"burst":40,"routes":[{"method":"POST","path":"/login","requests_per_second":2,"burst":4,"coordination":"central"}]}}
```

Central coordination uses one Postgres token consume per matching request.
The same trusted source and route map to the same bounded counter on every
replica. The gateway stores no raw IP address in that counter; it maps sources
to 1,024 deterministic shards per route. Colliding sources share allowance,
so an aggressive threshold can affect unrelated clients. Idle counters are
pruned after two hours. If the central store fails or is not configured, the
route falls back to its local source bucket and increments
`gateway_ratelimit_degraded_total{scope="preauth"}`. Other routes and the
app-wide source bucket remain local. Use the observe endpoint below before
enforcing a shared route budget.

For login or verification routes, a `failed_responses` budget can count only
application failures. Successful responses do not spend this budget:

```json
{"pre_auth_rate_limit":{"mode":"observe","requests_per_second":20,"burst":40,"routes":[{"method":"POST","path":"/login","requests_per_second":20,"burst":40,"failed_responses":{"failures_per_minute":5,"burst":3,"statuses":[401,403]}}]}}
```

The default counted statuses are `401` and `403`. Apps can select up to four
4xx codes except `429` to match their login response contract. Only proxied
application responses count: a gateway authentication denial, cached response,
or wake error does not. After the budget is spent, enforce mode returns a
`429` before authentication or VM wake, with
`x-faas-rate-limit-scope: pre-auth-failures` and a calculated `Retry-After`.
By default the budget is local to each gateway replica. Set
`failed_responses.coordination` to `"central"` for a fleet-wide budget on the
same exact route:

```json
{"pre_auth_rate_limit":{"mode":"observe","requests_per_second":20,"burst":40,"routes":[{"method":"POST","path":"/login","requests_per_second":20,"burst":40,"failed_responses":{"failures_per_minute":5,"burst":3,"coordination":"central"}}]}}
```

Central failure coordination checks one shared counter before compute on
each matching request, then records one token only after a selected proxied
application response. Concurrent failures can create bounded token debt, so
the next request remains blocked until it refills. The same verified source
and route map to one of 1,024 opaque source shards on every replica; no raw IP
is stored in the counter. Colliding sources and users behind a shared NAT
share allowance. Idle failure counters are pruned after seven days. A central
error falls back to the local source bucket, which continues recording
failures and remains active after recovery. This option is independent of the
route's request `coordination` setting. Observe mode records would-block
decisions while continuing to serve the route.

To observe distributed failures against the same application login target,
set `observe_targets: true` on a `POST` route with `failed_responses` and
`coordination: "central"`. On each selected failed response, the application
returns `X-Gregale-Abuse-Target` containing the 64-character lowercase hex
HMAC-SHA256 of the normalized submitted login identifier, using an app-owned
secret. Compute this for both existing and unknown users; conditioning the
header on account existence can create a timing-based account-enumeration
signal. Keep the HMAC key out of the request and response, and use the same
key across application replicas. The gateway removes this reserved response
header before it reaches the client. Gregale receives only an opaque digest;
it does not persist, log, or expose that digest or the raw identifier.
Responses without exactly one valid digest are counted as missing or invalid.
Successes and gateway-generated denials do not enter target counters.

The Node and Python SDKs provide `preAuthTargetDigest` and
`pre_auth_target_digest`, with `PRE_AUTH_TARGET_HEADER` for the header name.
Each helper accepts the app's already-normalized identifier and an app-owned
key of at least 32 bytes, then returns the required lowercase digest. Generate
the key randomly, keep it server-side, and share it across replicas. String
keys use UTF-8 bytes as supplied; a hex-encoded key is not decoded. Normalize
with the same rules used for account lookup, including unknown accounts. If
accounts are scoped by tenant, include the tenant in that normalized lookup
value, with an unambiguous encoding shared by every replica, so separate
tenants do not share a target signal. Attach the header
exactly once on selected failed login responses, regardless of whether the
account exists; never attach it to a successful response. Rotating the key
starts a new target history while old counters expire. See the
[Node](../sdk/node/README.md#login-target-observation) and
[Python](../sdk/python/README.md#login-target-observation) examples.

The gateway maps each valid target to two independent sets of 2,048 bounded
shared counters and spends one token per set on each selected failure. When
both sets exceed the configured `failed_responses` budget, it increments the
aggregate `target_threshold` observation. Collisions can produce false
signals, especially at high login volume; this is an approximate detection
signal, not an account-level quota. It never blocks or locks an account, even
when the pre-auth policy mode is `enforce`. Each valid failure uses two central
counter calls. If central coordination is unavailable, the signal uses
replica-local counters and records `target_fallback`.

Hobby and higher plans can opt into the `login_target_pressure` alert preset
after configuring `observe_targets`. The preset sends a signed webhook when
more than five aggregate `target_threshold` observations occur in 15 minutes;
its default cooldown is one hour. The app owner can adjust the cloned alert
rule's threshold and cooldown. The alert payload contains an
`observations_path` such as
`/v1/apps/my-app/pre-auth-observations?range=15m` for investigation. It never
contains the login identifier or its digest. The signal is approximate, so
review the observations and application login logs before changing policy.
The alert also includes `dashboard_path`, which opens the read-only pre-auth
protection view for the same app and window. The dashboard shows the current
policy, would-block response classes, target signals, and a distinct degraded
telemetry state. Select a window there to compare recent observations; a 2xx
response is only a possible false-positive signal.
This preset and metric support webhook notifications only; neither can run a
deployment action or lock an account. If Prometheus is unavailable, the alert
rule reports a degraded source and does not fire.

The optional `login_target_signal_health` alert watches for a missing or
invalid target digest after application changes. It evaluates the worst
observed route with at least 20 selected failures in 15 minutes and sends a
webhook when the gap exceeds 10%. Fewer failures leave the rule in `unknown`;
unavailable Prometheus data leaves it `degraded`. Neither case sends an alert
or establishes that the signal is healthy. It links to the same pre-auth
dashboard and cannot run a deployment action.

Before switching to `enforce`, read
`GET /v1/apps/{slug}/pre-auth-observations?range=1h` (available on every
plan). It returns one app policy, each configured route policy, each
configured failure budget, and each configured target observation.
`would_block` counts observe-mode requests that
crossed that policy's threshold; `result_2xx`, `result_3xx`, `result_4xx`,
`result_5xx`, and `result_unknown` classify those same requests' final gateway
responses. A `2xx` result is a possible false-positive signal, not proof of a
legitimate user. An app and its route can both register one request as a
would-block, so do not sum policy rows as unique requests. Route policy IDs
use fixed slots (`route_0` through `route_15`, with matching `failures_` and
`targets_` IDs),
so if routes are reordered or replaced during the requested time range, older
counts can refer to a previous configuration. Use a window after the last
policy edit. The response maps slots to the current configured paths; the Prometheus metric
`gateway_pre_auth_policy_shadow_total{app,policy,outcome}` uses only bounded
policy IDs, never client IPs or paths. Prometheus unavailability returns
`source: "degraded: ..."` and zero counts; these are unavailable data, not a
clean result. As with the limiter, measurements come from all gateway
replicas scraped by Prometheus. Enforcement is replica-local except for exact
routes that opt into central request or failed-response coordination.

The source is the client IP verified by the public gateway, which replaces
incoming `X-Forwarded-For` before passing the request to the internal gateway.
An enforce-mode app returns `403` when this trusted address is missing or
malformed. Source buckets are bounded to 1,024 per configured policy (the
app-wide policy, each route override, and each failure budget) and 65,536 per
gateway;
further addresses share that policy's overflow bucket until an inactive bucket can
be safely evicted.
The app-wide guard and default failed-response guard are local to each gateway
replica. Central failed-response coordination shares a bounded route budget
across replicas.
Existing app and account limits continue to cap aggregate request rates.
Shared corporate/NAT IPs also share a source bucket; use `observe` to choose
a suitable threshold.

`gateway_pre_auth_rate_limit_total{app,outcome}` reports `would_block`,
`blocked`, `route_would_block`, `route_blocked`, `failure_recorded`,
`failure_would_block`, `failure_blocked`, `central_fallback`,
`failure_central_fallback`, and `untrusted_source`
decisions without putting IP addresses or paths in metric labels.

## Security headers on your app

Your app controls its own security headers. When a response sets one of
the headers below, directly or through a `kind=headers` edge rule, Gregale
sends your value unchanged. When it sets none, Gregale adds a default:

| Header | Default on `*.gregale.dev` | Default on your custom domain |
|---|---|---|
| `Strict-Transport-Security` | `max-age=31536000; includeSubDomains` | `max-age=31536000` |
| `X-Content-Type-Options` | `nosniff` | `nosniff` |
| `Referrer-Policy` | `strict-origin-when-cross-origin` | `strict-origin-when-cross-origin` |
| `X-Frame-Options` | none | none |
| `Permissions-Policy` | none | none |

On a custom domain, HSTS does not include subdomains by default because
Gregale does not serve your other subdomains. Add `includeSubDomains` (and
`preload`) yourself once every subdomain serves HTTPS. Set `X-Frame-Options`
or a `Content-Security-Policy` `frame-ancestors` directive if your pages must
not be framed, and `Permissions-Policy` to restrict browser features.

```sh
gregale edge-rules create --app my-api --kind headers --match-host my-api.example.com \
  --headers-response-set 'X-Frame-Options:DENY'
```

Before 2026-10-09 these headers were fixed by the platform, including HSTS
with `includeSubDomains` on custom domains. Browsers that cached that HSTS
policy keep it until it expires (up to a year after their last visit).

## Web application firewall

A `kind=waf` edge rule (Pro and above) checks matched requests against the
OWASP Core Rule Set for SQL injection, cross-site scripting, path traversal,
command injection, scanners, and header injection. It works the same on
`*.gregale.dev` and on your custom domains. `observe`, `warn` and `block` at
paranoia level 1 are generally available; paranoia level 2 is in preview and
runs in `observe` only. Warn and block reach accounts one by one as they
launch: until they reach yours, creating a rule in either mode, or switching
one to it, returns `422` with code `edge_feature_unavailable`, and existing
warn and block rules only observe.
The rule's `mode` decides what happens to a detection:

| Mode | Headers and URL | Request body |
|---|---|---|
| `observe` (default) | sampled and reported | sampled and reported |
| `warn` | checked on every request; a detection adds `X-WAF-Warning: <rule id>` to the response | sampled and reported |
| `block` | checked on every request; a detection is answered with `403` before your app wakes | sampled and reported |

Request bodies are never blocked: checking them costs tens to hundreds of
milliseconds, so they are inspected off the request path, and only a sample
of them under load. In `warn` and `block`, a request without a body is
checked in-path only; it is not also sampled. To reject bad bodies, declare their shape with a
`kind=validate` rule. Checks on headers and the URL take about 1 ms of CPU
per matched request, and can add several milliseconds when the node is busy.
A node runs several checks at once, one per processor but one, and a check
that finds them all busy waits up to 1 ms for a free one. When your app's
check budget (roughly 500 requests per second) runs out, or no check frees
within that wait, the rest pass unchecked and are counted as
`inline_skipped`; the WAF never slows your app down to keep up.

Start in `observe`, look for false positives, then switch to `block`:

```sh
gregale edge-rules create --app my-api --kind waf --match-host my-api.example.com --match-path '/*'
gregale edge-rules summary --app my-api --range 24h   # detections, categories, top CRS rule IDs
gregale edge-rules update <rule-id> --kind waf --waf-exclude-rules 942100   # silence a false positive
gregale edge-rules update <rule-id> --kind waf --waf-mode block --waf-exclude-rules 942100
```

`edge-rules update --kind waf` replaces the whole WAF action, so repeat every
setting you want to keep (mode, exclusions, thresholds) in each update.

Each detection is also a security event, so you can see which request was
flagged and why. `gregale edge-rules events --app my-api --outcome blocked`
(or `detected` for sampled requests, `warned` for warn rules) lists the
request ID, method, path, client and user agent, plus the CRS rule IDs that
scored, their attack categories and the anomaly score. The matched payload
and the query string are never stored. Up to 10 detections per rule per
minute are kept, for 7 days; the Edge rules page in the dashboard shows the
same table. A WAF rule's ordinary matches are counted but not listed, since
every inspected request matches.

Some legitimate traffic is known to score as an attack at paranoia level 1.
Exclude these rule IDs on rules that match such routes, or narrow the rule's
`--match-path` so it skips them:

| Traffic | CRS rule IDs | Checked |
|---|---|---|
| GraphQL query in the URL (`GET /graphql?query={...}`) | 942190 | headers and URL, so `block` rejects it |
| GraphQL request bodies | 932235, 942290 | body (reported only) |
| Prose with SQL keywords after a semicolon (`...; select one from the list`) | 942350 | body (reported only) |
| Content that is SQL, HTML or code: a docs search, a CMS editor, a snippet tool | 942100, 941160 | URL and body |

The WAF accepts any `Content-Type`, or none, and the methods GET, HEAD,
POST, OPTIONS, PUT, PATCH and DELETE; other methods are reported (rule
911100).

`warn` and `block` run at paranoia level 1. Level 2 detects more and
produces more false positives; it is available in `observe` only. The
`edge_waf_detections` alert preset notifies you of bursts, and blocked
requests also count toward `edge_rejection_pressure`.

## Browser challenge

A `kind=challenge` edge rule keeps scripts and scrapers off browser pages.
An app can have 3 on Hobby, 10 on Pro and 25 on Scale; Free has none.
Challenge rules reach accounts one by one as they launch: until they reach
yours, creating one or switching one on returns `422` with code
`edge_feature_unavailable`, and existing challenge rules challenge nobody.
A matched request without a valid pass never reaches your app:

- A page load gets a short page that does a small calculation in the
  browser (a proof of work, tens to a few hundred milliseconds by default),
  then reloads the page with a signed pass cookie. No third-party script is
  involved, so it works the same on custom domains.
- Any other request (an API call, an image, a script without the pass) gets
  `403` with code `challenge_required`.
- Requests with a verified API key, consumer key or JWT are never
  challenged, so API clients keep working.

The pass lasts an hour by default and is tied to the browser's address
range and User-Agent, so it cannot be copied to another machine. Start in
log mode to see what the rule would challenge, and add a `verified_bot
missing` condition so search engine crawlers still reach your pages:

```sh
gregale edge-rules create --app my-site --kind challenge --match-host my-site.example.com \
  --match-path '/*' --match '{"field":"verified_bot","op":"missing"}' --mode log
gregale edge-rules update <rule-id> --mode enforce
```

`--challenge-difficulty` (10 to 22, default 16) sets how much work a browser
does; each step doubles it. `--challenge-pass-ttl` (300 to 86400 seconds)
sets how long a pass lasts.

Under attack the difficulty can rise on its own. Set
`--challenge-max-difficulty` above the difficulty, and while the rule serves
far more challenge pages than usual (more than four times its usual rate,
and more than 60 a minute on one gateway) the difficulty rises by 2 bits a
minute, up to that cap, then falls back as the load eases. Visitors who
already hold a pass are not affected. The edge-protection summary shows how
many pages were served at a raised difficulty. An update replaces the
whole challenge action, so pass every setting you want to keep:

```sh
gregale edge-rules update <rule-id> --kind challenge --challenge-difficulty 16 \
  --challenge-max-difficulty 20 --challenge-pass-ttl 3600
```

Visitors need JavaScript. Without it the page asks them to turn it on and
shows a reference (the request ID) to send you. Look it up to see their
address, then exempt it with a condition on the rule, such as
`{"not":{"field":"client_ip","op":"in_list","list":"office-ips"}}`:

```sh
gregale edge-rules events --app my-site --request-id <reference>
```

An enforced challenge rule records what it did as security events, with
the client's address, country, User-Agent and path: `challenged` (the page
was served), `rejected` (a request that was not a page load got `403`),
`solved` (a browser earned a pass) and `failed` (a solution was refused: a
forged token, one carried to another machine, or too little work). Visitors
who already hold a pass or a verified credential are counted, not recorded.
In log mode the rule's matches appear as `logged` events instead.

```sh
gregale edge-rules events --app my-site --outcome rejected
```

## Penalty box

An edge rule judges each request on its own: a scanner refused at
`/wp-login.php` simply tries `/.env` next. A **penalty** makes a rule keep
refusing a client after it has caught it. Once the rule has refused the
same client a number of times, every request from that client to your app
is refused for a while, whatever path it asks for.

Penalties work on rules that refuse clients: `ip` and `geo` rules,
`throttle` rules (a client that keeps hitting the limit) and `waf` rules in
block mode. They are released account by account: until your account is
included, adding a penalty, or switching on a rule that has one, returns
`422` with code `edge_feature_unavailable`, and existing penalties box
nobody while the rules themselves keep refusing.

```sh
# Block anyone who probes a scanner path from the whole app for 30 minutes.
gregale edge-rules recipe block-scanner-paths --app my-site --set penalty_minutes=30 --enforce

# A sign-in throttle that boxes a client for 15 minutes after 5 refusals in a minute.
gregale edge-rules update <rule-id> --penalty-for 15m --penalty-after 5 --penalty-within 1m

# Remove the penalty; clients it holds are let go on their next request.
gregale edge-rules update <rule-id> --clear-penalty
```

A boxed client gets `403` with code `penalty_box`, naming the rule, and a
`Retry-After` header saying when it ends. Penalties last from 1 minute to
24 hours.

Some details to know:

- The client is its IP address. For IPv6 it is the whole /64, which one
  machine can rotate through.
- Many people can share one address (offices, mobile carriers). A threshold
  above 1 and a short penalty keep one of them from locking out the rest.
- A box made on one edge node holds on every node within about a second,
  and survives a restart. Each node counts refusals toward a box on its
  own, so a client spread over several nodes can be refused a few more
  times before its first box.
- Removing the penalty, disabling the rule, or switching it to log mode lets
  its clients go. A log-mode rule never boxes anyone.
- The edge protection summary shows how many clients were boxed and how many
  of their requests were refused.

## Outbound connections and DNS

Workloads reach the internet by name. Outbound TCP is allowed only to addresses
the workload resolved through the platform's DNS resolver in the last 10 to 60
minutes, depending on the record's TTL. To connect to a fixed IP address (for
example a database without a hostname), add it to the app's egress allowlist.
Lookups of known abuse infrastructure, such as mining pools, return NXDOMAIN.

## Abuse tooling

Every image build is scanned for abuse tooling before it runs. Images that
carry a cryptocurrency miner, a mass port scanner or a flood tool fail to deploy
with `image_abuse_detected`, and the error names the rule and file. Mining pool
addresses and proxy or tunnel servers are recorded for review but do not block
the deploy.

## Quarantine recovery

When an enforce-policy app is parked after live security evidence regresses,
`GET /v1/apps/{slug}/security` reports the quarantined deployment and digest.
While it is quarantined, the deployment does not boot from any source: public
traffic, service-to-service calls, crons, warm floors, prewarm, warm pools and
app tasks are all refused with `security_posture_blocked`.
After deploying a newer image, use `POST /v1/apps/{slug}/security/recover` with
that deployment id. Recovery restores traffic only when every live canary has
fresh, complete, digest-matched scan evidence with zero high, critical, or
unknown findings. The operation requires deploy-write scope and MFA and emits
an audit event when the app returns to `active`.

After recovery, enforce-mode apps keep a bounded security-evidence lease. A
lightweight imaged sweep checks the live deployment between full rescans and
parks the app if the evidence expires, the image digest changes, scanner
metadata becomes invalid, or the result contains a blocking finding. The
quarantine audit and notification include the specific evidence reason.

Report a suspected vulnerability through the security contact listed in [security.txt](security.txt). Include a minimal reproduction and affected resource ids, but never include live credentials or customer data. Gregale will acknowledge receipt and coordinate a safe disclosure window.
