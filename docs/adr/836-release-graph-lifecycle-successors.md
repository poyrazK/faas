# ADR-836: Release graph lifecycle successor bindings

Status: Accepted

## Context

ADR-835 rejected project-owned successor applications because their ordinary
production route can select an active release graph rather than the deployment
with the highest traffic weight. Mutable application settings also need not be
those frozen into the selected deployment.

## Decision

Resolve a project successor through exactly one active production release set,
using the same project/account, environment and member identity constraints as
ordinary gateway release resolution. The pinned destination capture must belong
to that app's single member, whose deployment must remain live in production.
The graph can retain that member at zero weighted traffic; an unrelated newer
weighted deployment cannot replace its default destination. Without an active
production graph, project successor approval fails closed.

Require the deployment's captured production workload spec. Verify its app,
project, account and environment identity, revision and settings hash, then apply
its frozen settings before checking visibility, authentication, maintenance and
operation compatibility. Preserve original settings JSON when checking its hash;
JSONB normalization must not change embedded numeric spellings. Mutable desired
heads cannot substitute for a captured spec. Source-app project successors still
refer to the candidate and use its frozen settings.

Extend the durable successor snapshot with the complete active graph generation
and membership, frozen workload spec and environment identity, and destination
liveness. Graph IDs serve as generation identifiers. Approval takes the project
lock used by publication, and receipt insertion and traffic writes recheck the
snapshot. Publication, rollback, member changes, workload-pin/spec mutation and
environment changes permanently invalidate affected receipts. Reinstating an
old member or graph cannot revive an invalidated receipt. Memory store publishes
under its transaction lock and performs the corresponding invalidation.

This concerns ordinary public ingress without an explicit retained-release
request header. Explicit release/deployment pins are separate routing requests.
Header-dependent routing chains, tenant surfaces, wildcard/environment domains
and declared-route ingress policies retain ADR-835's fail-closed behavior.

## Consequences

Existing destination app/deployment/capture pins work for project applications
without a new request shape. Graph settings determine the destination, while
legacy applications retain their single production deployment at 100 percent
requirement. Receipts predating the extended binding require fresh approval.
Graph and workload mutation triggers compare current receipt snapshots and may
scan uninvalidated approval records; indexes and target-specific invalidation can be
added if approval volume warrants them.
