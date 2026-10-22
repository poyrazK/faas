# ADR 1036: Declarative bucket triggers for the owning compute app

Status: Accepted

## Context

Bucket notification rules already capture destinations at mutation acceptance
and use durable dispatch, retry and idempotent invocation admission (ADR 552).
Deployment and developer workspace declarations need to configure those rules
without deleting manually configured destinations or routing workspace uploads
to production apps.

## Decision

Add optional `triggers` declarations to deployment bucket dependencies and
`dev.buckets`. Each declaration has a stable `id`, optional ObjectCreated events
(default wildcard), prefix and suffix. The destination is derived by the server
from the bucket's owning app and region. Arbitrary destinations are unavailable
through this contract; use ordinary notification rules for cross-app routing.

Persist complete normalized managed rule snapshots in
`object_bucket_notifications.managed_rules`. A CHECK requires every managed
snapshot to equal a current rule, with unique nonempty IDs. The public response
exposes only `managed_rule_ids`. Derived IDs include bucket identity and the
user's declaration ID, so independent workspaces never share rule identities.

`PUT /v1/apps/{slug}/buckets/{bucket}/compute-triggers` requires MFA,
`storage:manage`, bucket write access and an explicit expected revision. Account
and bucket locks make compare-and-swap atomic, including unchanged requests.
Reconciliation replaces the previously managed subset and preserves manual
rules. Manual ID collisions return 409; overlapping notification filters return
400 without changing intent. Deployment reads the current revision and fails
on conflicts instead of automatically overwriting a concurrent edit. Multiple
trigger declarations resolving to one bucket are rejected before binding writes.

Omitted declarations preserve existing intent. An explicit empty array removes
only managed rules. Full manual notification replacement (including signed S3
configuration) clears management even if the rule content is identical. A later
deploy must not silently adopt manually owned IDs. New managed destinations are
validated for ownership and plan entitlement; unrelated retained manual
configuration can still be cleared from management after its target retires.

Developer declarations target the isolated developer app. Restart, omission and
refresh retain durable identities. Existing app/bucket cleanup handles workspace
stop and expiry. Accepted events keep the original destination snapshot across
configuration changes and use the existing dispatcher; clearing configuration
does not cancel already accepted work. Delivery remains at least once. Stable
invocation admission prevents duplicate rows after a lost recipient checkpoint,
but functions must make external effects idempotent.

## Rollout and rollback

Apply the additive migration and roll out compatible writers before using managed
reconciliation. A legacy writer changing a managed rule fails the ownership
CHECK instead of silently invalidating ownership. Downgrade refuses active
managed metadata. Explicitly clear management or take manual ownership before
rolling back. No new provider permissions, VM lifecycle operation or external
provider service is required.

## Verification

Memory and actual PostgreSQL contracts cover manual preservation, stable IDs,
concurrent revision conflicts, restart, takeover, migration replay, invalid
ownership, legacy writes and downgrade guards. API and CLI tests cover explicit
intent, strict input and disabled-service clearing. Signed AWS SDK gateway tests
exercise upload filters, captured self-target routing and retry after checkpoint
loss. These local fixtures qualify invocation admission, not native VM execution
or a live cloud provider.
