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

Apps can opt into a gateway rate limit that runs after hostname routing and
before consumer-key lookup, JWT verification, body admission, or VM wake. Set
`pre_auth_rate_limit` when creating an app or through
`PATCH /v1/apps/{slug}`:

```json
{"pre_auth_rate_limit":{"mode":"observe","requests_per_second":2,"burst":4}}
```

`observe` records requests that would exceed the source limit without
rejecting them. Change `mode` to `enforce` to return `429` with
`Retry-After: 1` and `x-faas-rate-limit-scope: pre-auth`. Set `mode` to `off`
to disable the guard. The setting is absent and disabled on existing apps.
The rate and burst must be positive and no greater than the app plan's
request rate and burst.
If the app moves to a lower plan, the gateway clamps an existing setting to
the new plan ceiling.

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

The gateway maps each valid target to two independent sets of 2,048 bounded
shared counters and spends one token per set on each selected failure. When
both sets exceed the configured `failed_responses` budget, it increments the
aggregate `target_threshold` observation. Collisions can produce false
signals, especially at high login volume; this is an approximate detection
signal, not an account-level quota. It never blocks or locks an account, even
when the pre-auth policy mode is `enforce`. Each valid failure uses two central
counter calls. If central coordination is unavailable, the signal uses
replica-local counters and records `target_fallback`.

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

## Quarantine recovery

When an enforce-policy app is parked after live security evidence regresses,
`GET /v1/apps/{slug}/security` reports the quarantined deployment and digest.
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
