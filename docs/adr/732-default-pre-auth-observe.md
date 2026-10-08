# ADR-732: New apps observe the pre-auth source limit by default

- **Status:** accepted
- **Date:** 2026-10-08

## Context

The pre-auth source limit (`pre_auth_rate_limit`) bounds each trusted client
address before consumer-key lookup, JWT verification, body admission, or a VM
wake. It is the platform's only per-source guard that runs before credentials
are checked, and it is the cheapest place to stop credential stuffing and
scraping against a parked app.

It is opt-in: an app without the field has no per-source guard, only the
plan-wide app and account limits. In practice new apps ship without it, and a
customer learns that they needed it during an incident, with no history showing
which sources would have been limited.

The static hardening headers (HSTS, `X-Frame-Options`, `X-Content-Type-Options`,
`Referrer-Policy`, `Permissions-Policy`) are already emitted on every public
response by `httpsec.Static`, so headers are not part of this decision.

## Decision

`POST /v1/apps` gives a new app `pre_auth_rate_limit` in `observe` mode when the
request does not set the field:

- 10 requests/s and burst 20 per source (`PreAuthDefaultRequestsPerSecond`,
  `PreAuthDefaultBurst` in `pkg/api/limits.go`), clamped to the plan's
  `RateLimitRPS` and `RateLimitBurst` (Free becomes 5 requests/s, burst 20).
- No route overrides, so every bucket is local to the gateway replica and no
  central counters are written.
- An explicit value, including `{"mode":"off"}`, is stored exactly as sent.
- Existing apps are not migrated; their configuration is unchanged.

The default is applied where new apps are built: `buildApp` in
`cmd/apid/handlers.go` (the API, CLI, SDKs, dashboard, and `gregale dev`) and
`workloadToDraftApp` in `pkg/reconcile/apply.go` (project and GitHub repository
apply). PR preview apps copy their parent's manifest, so they inherit the
parent's setting rather than the default.

## Why

`observe` never rejects a request: over-limit sources are recorded as
`would_block` and the request continues. The default therefore cannot break an
app, while every new app accumulates the evidence needed to switch to
`enforce` safely. Turning on `enforce` by default was rejected for that reason
(see below).

## Consequences

- New apps show a configured guard in `gregale` and the dashboard's pre-auth
  page, with would-block observations from their first request.
- The gateway does per-source bucket work for new apps. The source table is
  already bounded per app (`preAuthSourcesPerApp`), so memory stays bounded.
- An app behind a single shared egress address (an office NAT, a server-side
  caller) may show would-block traffic that is legitimate. That is the purpose
  of observe mode: the customer raises the rate or adds route overrides before
  enforcing.
- Copying an app's manifest (environment clones, GitOps) carries the stored
  value, as for any other manifest field.

## Rejected alternatives

- **Enforce by default.** Breaks apps whose legitimate traffic comes from few
  addresses, with no prior signal to the customer.
- **Backfill existing apps.** Silently changes stored configuration for every
  account; existing customers can opt in with one `PATCH`.
- **Plan-wide defaults only.** They bound an app's total traffic, not a single
  abusive source, so they do not address credential stuffing.
