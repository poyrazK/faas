# Request traffic policy snapshots

ADR-375 pins a public request's compiled host rules before route substitution.
The resolved app flags and plan table join that snapshot after owner lookup.
External CORS presets and named-environment headers/CORS are included as
resolved actions. Imported OpenAPI method/path declarations and environment
route-contract overlays are pinned before the app snapshot is sealed. Route
enforcement and observed route labels use that same contract. Upload, wake and
retries retain those inputs even when shared caches refresh. App slices and
maps are copied before guest work.

## Runtime evidence

Ordinary responses expose `X-Gregale-Traffic-Policy: traffic-v1:<sha256>`.
The gateway owns this response header at commitment; application responses,
edge header actions, and declared or late trailers cannot overwrite it. The request span carries
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
acceptance. Imported document cache entries are owner scoped. An invalidation
during a pending document load refuses its unpublished snapshot, while an
already pinned request retains its contract. The configured total deadline is
armed before loading that document or scoped contract; expiry returns 504.
Managed service calls now pin the policy inputs described below. Simulator/runtime
agreement and complete service-path acceptance still require verification.
Native lifecycle and deployment acceptance remain pending.

## Managed service calls

Both internal service-proxy listeners read discovery, alias access and
authorization in one read-only repeatable-read Postgres transaction. This
includes preview/scenario-test namespace, caller binding and transport policy,
target caller/method/path grants, dependency reliability, and target protocol
and WebSocket posture. Release membership/expiry, exact override eligibility and
positive deployment weights use that same committed view. The complete lookup
has a 250 ms bound and releases the
transaction before queueing, wake or dispatch. Missing verification returns
503 with `Retry-After: 1`; it does not use a warm authorization fallback.

The gateway copies the result and retains it across wake and retry. The
response header and dependency span carry a fingerprint that also includes the
effective default retry configuration and named service/alias. Declared call
timeouts count from service-handler entry, so policy lookup consumes that
budget. Application headers and trailers cannot replace the proof. Raw Upgrade
bytes have span evidence but no protected HTTP proof header.

Emergency account/app/deployment generations remain fresh independent checks.
Endpoint health remains live. The snapshot chooses one deployment before wake:
release membership takes precedence over an exact override, followed by version
affinity or weighted selection. Ordinary calls without an affinity key use local
random selection against the pinned weights. Instance rotation/local-node
preference and retries stay inside that deployment, including after a cold wake.
A newly published graph or changed weight cannot redirect an admitted call.
No eligible deployment returns 503 before wake. The roster is bounded to 100
positive deployments; a larger roster refuses verification.

The policy proof includes verified release/override verdicts and weights.
Selection entropy and the individual random choice are excluded, so equivalent
policy inputs keep the same fingerprint. The dependency span separately records
`gregale.traffic.selected_deployment_id`. Expired release pins return 410,
ambiguous membership/conflicting pins 409, invalid header shapes 400, an explicit
release without verified source deployment 403, and an ineligible exact override
422. These refusals precede wake/guest dispatch. Normal updates retain admitted
policy; emergency generation fences still apply.

## Covered paths

| Path | Snapshot and evidence |
| --- | --- |
| Ordinary public HTTP, including cache and edge responses | Compiled host rules, imported/scoped route contract, resolved app flags and plan; response/span/log fingerprint |
| Named environment, verified domain, listener selector | Effective host rules; selector and base host use one cache generation |
| Public streaming and raw Upgrade | Pinned rules/app inputs; span/log evidence; ordinary HTTP header only where normal response commitment is used |
| Managed service proxy | Atomic access/namespace/reliability/transport/release/override/weight snapshot; one deployment across wake/retry; response/span fingerprint |
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
