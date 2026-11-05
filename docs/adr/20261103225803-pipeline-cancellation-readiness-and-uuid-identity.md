# ADR-20261103225803: Pipeline cancellation, serving API readiness and UUID identity

- **Status:** implemented for local/staging qualification; production disabled
- **Date:** 2026-11-03
- **Amends:** ADR-1591, ADR-20261103230000

## Context

Saved Data API readiness compared its deployment with the latest attempt across
all environments. A failed candidate or unrelated staging deployment therefore
blocked completion while the intended default deployment still served traffic.
Verification could be started during billing grace but could not be cancelled.
Accepted cancellation also left destination triggers and dirty-range tracking
active when the worker could not decrypt or connect to the source. UUID inputs
accepted by validation were compared or persisted as raw strings, breaking
connection checks, pipeline idempotency and recovery of equivalent saved intent.

## Decision

Complete saved API setup only when its exact deployment serves the default
environment. Keep ownership, live status, Auth issuer and binding checks. A newer
attempt in that environment may fail or remain pending without hiding the serving
deployment. A different serving deployment remains a conflict.

Cancellation requires existing-pipeline eligibility, including the operator gate,
without new-deployment eligibility. Keep its dashboard action available during
billing grace. Preserve exact account, revision and run checks and worker fencing.

Remove cancelled verification tracking using private destination access under the
existing advisory lock and current lease/revision fence before source connection
or capacity admission. During pause/disconnect, suspend owned subscriptions first.
Match the physical run or nonempty logical intent, validate helper ownership and
retain tracking belonging to active table retirement. Cleanup remains transactional
and idempotent; it preserves native replication, mirror data and catalog cancellation.
Approved reader revocations retain priority even if verification cleanup conflicts.

Canonicalize accepted UUID spellings before text persistence and identity comparison.
Apply this to connection-check targets, pipeline creation and saved setup request
IDs. Normalize legacy saved request JSON when reading it, preserving the reserved
resource identities and the meaning of an empty target. No catalog migration,
quota, provider operation or native receipt format change is required.

## Validation and scope

Native TLS/password tests cover cancellation during billing grace, unavailable
source keys, failed source authentication, continuing CDC, helper removal, replay,
stale leases, exact runs, logical intent, foreign ownership and active retirement.
Deployment tests distinguish failed/pending candidates, other environments and a
real serving replacement. PostgreSQL tests cover uppercase, compact, braced and URN
UUIDs, idempotent retries, target-specific evidence, different destinations and
legacy saved request recovery. Dashboard tests cover cancellation eligibility.
Full pipeline acceptance and repository checks remain required. This does not
change the staging-only release or production qualification gates.
