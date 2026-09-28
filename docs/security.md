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

The source is the client IP verified by the public gateway, which replaces
incoming `X-Forwarded-For` before passing the request to the internal gateway.
An enforce-mode app returns `403` when this trusted address is missing or
malformed. Source buckets are bounded to 1,024 per configured policy (the
app-wide policy and each route override) and 65,536 per gateway;
further addresses share that policy's overflow bucket until an inactive bucket can
be safely evicted.
The guard is local to each gateway replica, so its per-source threshold is an
early abuse brake rather than a fleet-wide quota. Existing app and account
limits continue to cap aggregate request rates. Shared corporate/NAT IPs
also share a source bucket; use `observe` to choose a suitable threshold.

`gateway_pre_auth_rate_limit_total{app,outcome}` reports `would_block`,
`blocked`, `route_would_block`, `route_blocked`, and `untrusted_source`
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
