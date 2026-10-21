# ADR-202610161954: Private immutable hosting previews

Date: 2026-10-16
Status: Accepted for the internal, default-off pilot

## Context

Staging verifies release bytes without exposing them through production routing.
Operators need to inspect those exact bytes before promotion, including on a new
site without a live release. Infrastructure qualification remains unavailable.

## Decision

Create account-owned, bounded preview references to verified ready releases.
Each uses a fresh `p-UUID` label under the existing isolated hosting wildcard and
its existing internal Access audience/allowlist. No per-preview Cloudflare
resource, process, build, or copy of release bytes is created. Release and expiry
are immutable; revocation is terminal. Independent admission, edge and dashboard
flags default off. Previews require durable routing.

R2 publishes conditional immutable bindings, generations 1 (active) and 2
(revoked). Retain tombstones beyond SQL cleanup so delayed writers cannot revive
URLs. On every request, Access verification precedes preview/cache reads; binding
identity, origin, hard expiry and canonical site permission precede file serving,
HEAD and 304. The canonical head explicitly carries `preview_allowed`, independent
of whether a release is live. Missing permission fails closed. Preview routing
contains only the bound release; production routing remains promotion-only.
Preview traffic does not enter production analytics.

Site-row locking serializes creation, retirement, quota enforcement and deletion.
Any preview row, including expired/revoked rows awaiting cleanup, prevents release
retirement and physical artifact collection. Site deletion atomically revokes all
references. Reconciliation continues when admission is off; after a tombstone
receipt and the edge/storage grace, it removes the SQL reference. Hard expiry
still applies while apid is unavailable. Never delete preview tombstones as part
of release garbage collection.

## Consequences

Limits reside in `pkg/api/limits.go`: eight references/site, default 24-hour TTL,
5-minute minimum, 72-hour maximum. Metadata repair is bounded to 100 rows/pass,
two workers, a 30-second dispatch deadline and six-hour active repair; failed
publications retry after 30 seconds. Cleanup grace is 150 seconds after the first
revocation receipt. Revocation/suspension has up to 60 seconds of edge staleness
after publication; a receipt does not establish worldwide convergence.

All writers and reconcilers must be drained for the migration/upgrade. The
migration queues new canonical generations for explicit preview permission;
old code must not publish those generations or collect preview-referenced files.
Downgrade requires revoking previews and draining references. See the
[operator guide](../internal-frontend-hosting-previews.md) for rollout and evidence.
