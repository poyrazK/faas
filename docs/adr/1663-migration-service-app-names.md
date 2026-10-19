# ADR-1663: Separate migration service names from app names

## Status

Accepted for local implementation.

## Context

Repository names such as Compose `api` and `web` are local service identities.
Gregale app slugs are globally unique and some names, including `api`, are
reserved. Requiring repository edits to satisfy those allocation rules makes
ordinary migrations fail before customers can configure their services.

## Decision

Migration-mode scans retain repository names, including generic Procfile and
root services. Existing legacy generic members and explicit saved selectors
retain their earlier project-name identity so upgrades do not replace them.
APID suggests stable project-prefixed app names and accepts an
`app_slugs` map keyed by exact repository names. Normalization or truncation
adds a digest to preserve distinctions. Scan checks format, reservations,
duplicate targets, creator ownership and global holders including deleted apps.
An availability lookup failure stops the review. The store remains authoritative
when another customer claims a name after review.

The plan returns each service's `name` and `app_slug`. Its token binds the
resolved mapping and migration mode along with source and project identity.
Cached apply repeats that mapping through normal admission and reconcile.
`WorkloadName` remains the repository identity; `Slug` is the allocation.
Configuration, dependency ordering and readiness conditions keep their original
service keys. Later scans and reconciliation preserve existing allocations.
Import cannot rename an existing member or adopt an unrelated app holding a name.

The console shows suggested names, allows edits for new allocations and requires
another server review after edits. Configuration values stay in the form while
names are reviewed. Only the newest reviewed token can be submitted. This review
re-fetches the selected source ref; apply consumes the resulting exact archive.

The migration journal saves each target name before app creation. Resume checks
unfinished allocations against saved names, even if the original apply stopped
before creating apps. The console re-scans with the saved mapping. Older receipts
without a saved target before preparation retain their established name fallback.

Production service resolution prefers a repository workload alias within the
caller's creator account and project. Existing global slug lookup remains the
fallback for standalone and explicit app references. Preview and scenario routing
retain their environment-aware owners. Service access policies and reliability
settings accept project-local repository names while preserving existing slug
references. Same-named callers in another project gain no alias permission.
Explicit binding verification targets use the same production resolver.
PostgreSQL looks up at most two matching IDs to detect ambiguous aliases; it
does not load the project inventory on each internal request.
Grouped release preparation, activation and staging resolve those aliases to
the reviewed target deployments and require their original dependency pins.
Preview production fallback resolves the mapped project member while retaining
the existing explicit project and target access policies.

Because workload names now affect routing and policies, service evidence revisions
include them in both stores. An append-only migration updates the PostgreSQL
invalidation trigger and invalidates existing service evidence on upgrade and
rollback. No deployment, scheduler, source archive or customer configuration
values are changed by naming review.

## Consequences

Customers can choose available app names without repository edits. Availability
is checked again on apply; a scan reserves no names. Saved targets remain fixed
for retries, and moving an unfinished service to another name requires a new
migration. Upgrading or rolling back the routing change invalidates previous
service-binding evidence, requiring fresh checks before an enforced release.

## Validation

Tests cover normalization, long names, reserved and taken targets, duplicate
allocations, unchanged dependency/configuration keys, repeat reconciliation,
custom name persistence, saved-name retry fences, project-local resolution and
authorization, and workload-name evidence invalidation in both stores. HTTP,
SDK and console tests cover the wire contract and review-before-deploy flow.
