# ADR-792: Deployment-specific route lifecycle metadata

Status: Accepted

## Context

ADR-828 attached app-import metadata before routing and omitted pinned URLs.
Production splits and canaries can serve different lifecycle contracts.

## Decision

Resolve lifecycle metadata after final target selection/capacity admission,
using the target's deployment ID and original public method/path. Never use
an app-wide import as a fallback for a missing deployment capture. Match both
account and app ownership in capture metadata and reject truncated/invalid
captures. Missing metadata omits advisory headers without changing admission.

Cache captures by account/app/deployment, with the existing five-minute TTL.
A database trigger emits app OpenAPI invalidation notifications on capture
insert, update and deletion. Wholesale per-app invalidation includes negative
entries. Generation fences prevent in-flight reads from reintroducing stale
cache entries. Transactional notifications become visible after commit.

Platform-authored date headers take precedence over guest date headers in
both streaming and legacy HTTP forwarding. Response-cache entries currently
lack final serving deployment identity: omit lifecycle dates and successor
links on capture/replay, including stale and preexisting entries. Retain other
headers/links. Cache-hit lifecycle publication is deferred until the response
cache records the deployment associated with the body.

Sunset reports default to deployment captures, record source/hash/time, and
retain explicit `--source manual_import` for import review. Saved diff reports
accept deployment capture provenance and flag changed source/hash evidence.

## Consequences

Pinned, production and canary live responses share a deployment-based metadata
authority. Gateway-generated early responses and cached bodies omit headers.
This supersedes ADR-828's publication timing and app-hostname-only behavior and
ADR-829's default import source. Removal approval remains independent.
