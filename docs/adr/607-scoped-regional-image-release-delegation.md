# ADR-607: Scoped regional image release delegation

Date: 2026-10-04
Status: Accepted; regional authorization boundary.

## Context

ADR-606 coordinates immutable releases but requires regional account-admin keys
on each resume request. Unattended recovery needs a credential the directory can
seal without retaining broad regional account access.

## Decision

A directory mints a prefix-disjoint `fp_release_` bearer and a grant UUID, seals
its token and exact identity before contacting the cell, then registers only its
SHA-256 with a regional account-admin key. Regional PUT/GET/DELETE grant routes
bind the immutable UUID/hash/expiry to the cell, regional app, current personal
owner/org and one global app release stream. Repeated identical registration
returns the same grant, including revoked/expired state; it never renews it.
Changed input conflicts. Revocation tombstones survive while the app exists.

The bearer authenticates only for ordered image release PUT/GET, ownership proof
for its app, GET of its own grant metadata and DELETE of its own grant. It cannot use ordinary deployment,
account, app mutation, grant issuance or another app/stream route. Revocation,
expiry, app removal/ownership change and personal-org suspension fail closed.
Each receipt replay must match the delegated stream before it is returned. The
regional deployment transaction rechecks the grant while locking the app, so
revocation or expiry after middleware cannot accept fresh intent. Account-admin
image release behavior remains available for explicitly authorized requests.

There are at most 32 active, unexpired grants per app; expiry is bounded to seven
days. Both are protocol bounds in pkg/api/limits.go. Registration and revocation
lock the app before the grant, matching image release intent lock ordering.
Runtime SQL uses SQLC. Database guards preserve grant identity, closed state,
owner/cell binding and revocation terminality; downgrade refuses retained grants.

The directory credential journal, revocation outbox and bounded background worker
will use this boundary. They must seal a fixed identity before issuance, recover
lost registration responses, recheck the frozen cell/binding before unsealing,
and revoke on detachment. Serving/wake remains independent of the directory.

## Validation

Exercise immutable registration/replay, parallel issuance bounds, revocation,
expiry, owner transfer, cell/app/stream isolation, denial of ordinary APIs,
receipt replay isolation and a revoke/expiry race before deployment acceptance.
Use PostgreSQL guards and real authenticated HTTP requests. Full multi-region
completion still requires the directory worker, global quota/identity,
health/ingress, managed placement policy and native KVM recovery evidence.
