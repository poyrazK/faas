# ADR-233 · Environment-owned declared route contracts

- **Status:** accepted
- **Date:** 2026-09-24
- **Decision:** A project environment may own an explicit declared-route
  contract for each workload. The additive PUT surface writes it, environment
  clone copies it in the same transaction as configuration and scoped values,
  and state/diff expose both the contract and its ownership. The gateway uses
  the scoped contract for exact deployment URLs, whose resolved app carries a
  pinned deployment scope. Ordinary application hostnames retain their legacy
  application-wide contract.
- **Why:** Application-wide declarations cannot explain or isolate a route
  difference between staging and production. Copying their values without
  durable ownership would make `environment create --from` misleading.
- **Consequences:** The policy row is keyed by app and registered environment,
  removed with either owner, and requires an explicit route list when
  enforcement is enabled. A missing row falls back to the application-wide
  contract and is reported as application-owned. Clone snapshots the effective
  application declarations when they are explicit, so later application edits
  cannot silently change the clone. State/diff compare sorted route declarations
  and report ownership changes. Scoped requests read the policy before the
  pre-wake route gate and fail closed if that read errors.
- **Limitation:** An application-wide OpenAPI document without explicit
  declarations remains shared. Clone does not claim to copy it, and reports
  `routes` under shared resources. Ordinary app hostnames do not select a named
  project environment; environment-owned contracts applied only to deployment-
  specific URLs until the stable environment URL follow-up below. Domains and
  pre-routing edge rules remain application-owned.
  Their ownership and routing transitions require separate decisions.
- **Rejected alternatives:** Treating app-wide OpenAPI documents as copied
  without a versioned document row; adding a hostname-derived environment
  selector to the edge-rule matcher; silently applying a staging route policy
  to the production app hostname.

## Operational notes

The route-policy read is deliberately uncached for pinned deployment URLs in
this first slice. It keeps a write from briefly opening an undeclared path on
an edge with a stale cache and preserves fail-closed behavior during database
errors. Regular app hostnames pay no additional read. A future cache must have
an invalidation/convergence contract as strict as edge-rule mutations before
it can replace the direct read.

The API and CLI require a full replacement body with both
`only_allow_declared_routes` and `declared_routes`. The CLI asks for confirmation;
the API requires MFA and deploy-write scope. The database rejects an enabled
gate with an empty explicit list, so direct store writes cannot accidentally
restore a shared OpenAPI fallback.

## Follow-up: stable platform URL for a named environment

Each registered project environment now has a stable platform URL per public
workload. `gregale projects environments releases` and the effective-state API
show it even before that workload's first release; requests return 404 until a
live deployment exists in that environment. The hostname is one 57-byte DNS
label: `env-` followed by the unpadded lowercase base32 environment UUID,
`-`, and the unpadded lowercase base32 app UUID, under the existing
`*.gregale.dev` wildcard. It is intentionally opaque rather than an ambiguous
concatenation of two hyphenated slugs. Deleting and recreating an environment
changes its UUID and invalidates its old URL without any hostname reassignment.

The gateway validates that the environment and app still exist, belong to the
same account and project, and have a live deployment for the environment
scope. It pins that deployment and scope, so the existing scoped declared-route
contract applies. These mutable hostnames bypass the route and stale caches:
promotion, deletion, and control-plane errors cannot leave an old release
serving from a cached route. Ordinary app hostnames, exact deployment URLs,
custom domains, and application-owned edge policies were unchanged. The
headers/CORS follow-up below addresses part of that policy limitation;
custom domains and other edge-rule kinds still need separate decisions.

## Follow-up: environment-owned headers and CORS policies

A workload in a registered environment can now own an explicit replacement
set of `headers` and inline `cors` edge rules for its stable environment URL.
`PUT .../workloads/{workload}/policies` replaces the full list (including an
empty list that disables application-level headers/CORS inheritance). The CLI
exposes the same operation through `gregale projects environments policies
set`. Rules use the existing edge action validation and a per-environment
limit no larger than the account plan's app rule cap. CORS preset references
are rejected because presets remain application/account-owned.

The effective-state and diff APIs show these rules and their ownership. A
clone copies an explicitly owned policy inside its transaction and reports a
non-secret copy count. An environment without such a row retains the existing
application-rule fallback and is reported as application-owned, not falsely
equal to another environment. The edge-rule convergence fence invalidates the
host cache on each replacement; the stable URL's route lookup also fails
closed if its policy store is unavailable. Application-wide `route` rules may
not substitute the stable URL's encoded workload before identity resolution.
Other edge-rule kinds, ordinary app hostnames, and custom domains remain
application-owned and are still listed as shared resources.

## Follow-up: environment-owned redirect and rewrite policies

Redirect and rewrite rules form a second, independently replaceable policy
group on the stable environment URL. `PUT .../workloads/{workload}/routing-policies`
accepts only those two kinds. An explicit empty list suppresses inherited
redirects and rewrites there without changing headers/CORS or ordinary app
hostnames. The CLI exposes `gregale projects environments policies routing set`.
The separate record matters: an existing explicit headers/CORS policy must not
silently disable application-owned redirects after this feature is deployed.

The clone transaction copies an explicit routing policy and counts it with
other copied policies. State and diff report its ownership and rules separately
as `routing_policies`. The gateway filters wildcard rules to the workload
encoded in the stable hostname, replaces only the redirect/rewrite kinds, and
uses the existing convergence fence for cache invalidation. Route substitution,
custom domains, and remaining edge-rule kinds are not made environment-owned
by this decision.
