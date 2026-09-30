# Request traffic policy snapshots

ADR-375 pins a public request's compiled host rules before route substitution.
The resolved app flags and plan table join that snapshot after owner lookup.
External CORS presets and named-environment headers/CORS are included as
resolved actions. Upload, wake and retries retain those inputs even when the
shared caches refresh. App slices and maps are copied before guest work.

## Runtime evidence

Ordinary responses expose `X-Gregale-Traffic-Policy: traffic-v1:<sha256>`.
The gateway owns this response header at commitment; application responses and
edge header actions cannot overwrite it. The request span carries
`gregale.traffic.policy_revision`, and request logs carry
`traffic_policy_revision`. Raw Upgrade response bytes do not expose this HTTP
response header; the request span/log still records the snapshot.

This is a versioned fingerprint of the effective inputs used by that request,
not a database transaction number or fleet convergence acknowledgement.
Different hosts or tenants can have different fingerprints. Compiler/plan-table
changes can change the fingerprint. Credentials and raw policy are never
included in response or log evidence. Live counter, picker, cache, and breaker
state are decisions made against the snapshot, not frozen counter values.

## Failure and update behavior

A warm verified cache can supply a snapshot during a store outage. A cold or
expired cache cannot: it returns `traffic_policy_unavailable`/503 with
`Retry-After: 1` before authentication, wake or guest execution. A compiled
policy with reported rule errors also refuses an unverified owner snapshot.
Failure checks run after owner resolution: another account's broken free-form
host rule or unavailable preset cannot take this tenant offline. A
verified empty policy is valid. Invalidation between selector-host and
base-host reads refuses the inconsistent combination. Normal convergence
fences new requests; previously admitted requests retain their inputs.

Account/app/deployment emergency cancellation now uses the separate durable
generation fence described in [HTTP security revocation](traffic-security-revocation.md).
Its local gateway and Postgres checks do not establish full path or deployed
acceptance. Imported declared-route document pinning, simulator/runtime
agreement and managed-service policy fingerprints remain required work.
Native lifecycle and deployment acceptance remain pending.

## Covered paths

| Path | Snapshot and evidence |
| --- | --- |
| Ordinary public HTTP, including cache and edge responses | Compiled host rules, resolved app flags and plan; response/span/log fingerprint |
| Named environment, verified domain, listener selector | Effective host rules; selector and base host use one cache generation |
| Public streaming and raw Upgrade | Pinned rules/app inputs; span/log evidence; ordinary HTTP header only where normal response commitment is used |
| Managed service proxy | Binding authorization and reliability lookup remain separate; full snapshot/fingerprint pending |
| Synthetic HTTP through the public routing handler | Same handler snapshot; direct bridge dispatch has no host-policy snapshot |
| TCP service tunnels, detached jobs, unmediated guest sockets | Outside this public HTTP snapshot |

## Local evidence

Full gateway and internal-gateway command suites passed on Darwin. Tests cover
an actual handler wake while app settings and rules change, route/retry/deadline
matching after refresh/store failure, resolved preset changes, deep copying,
selector-host generation changes, compile refusal and protected response
evidence. A representative snapshot freeze benchmark measured 22.4 microseconds
and 12.1 KB per request on the local M3 Pro; this is not deployed load evidence.
These checks do not establish native or deployed acceptance.
