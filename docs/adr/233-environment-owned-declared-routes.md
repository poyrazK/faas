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
  `routes` under shared resources. App hostnames do not select a named project
  environment today; environment-owned contracts apply only to deployment-
  specific URLs. Domains and pre-routing edge rules remain application-owned.
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
