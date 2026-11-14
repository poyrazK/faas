# ADR-20261104105500: Pipeline setup ownership, capability revocation and retries

- **Status:** implemented for local/staging qualification; production disabled
- **Date:** 2026-11-04
- **Amends:** ADR-1591, ADR-20261027123814, ADR-20261103225803

## Context

An admitted account could save a setup naming another account's database. The
global setup lifecycle guard then blocked that database's owner from deleting or
resizing it. Existing-database ownership was checked only after the reservation.
A saved Data API plan also outlived its capability release: queued preparation
and deployment could create new resources after the operator withdrew access.
Finally, retrying a failed setup reused a fresh negative connection check and
immediately blocked again, without reconsidering repaired source settings.

## Decision

Read the existing mirror through its owner service before saving a new setup.
Keep provisioning mirrors eligible, but reject retiring or unavailable mirrors.
A database trigger additionally validates destination ownership and visibility,
matches an explicit target to its saved request, and holds a shared row lock until
the reservation commits. The lock serializes admission with deletion and resize.
Fresh server-generated mirror IDs may be reserved before a database row exists.
The trigger covers direct catalog writes and workers from the previous release.

Scope the destination lifecycle guard to reservations belonging to its account.
The repair migration cancels existing foreign/private-clone reservations in
restartable keyset batches, retains their metadata and resource IDs, advances
revisions and releases leases. It does not delete customer resources or alter
legitimate active setups. Rollback removes
the new guard without reactivating invalid reservations.

Recheck the Data API capability before app/binding preparation and before a new
build is admitted. Preserve recovery of already accepted deployment/build IDs
and read-only deployment status while access is withdrawn. Restoring access
allows the same saved intent to proceed, without replacement resources.

An explicit retry of a failed source or mirror check invalidates only the
matching completed negative evidence, atomically with the owned revision change.
Capacity failures also invalidate completed negative allocation evidence.
Preserve newer passed checks and pending/running checks. Keep check creation
timestamps and the existing request cooldown: setup stays working while waiting for the
cooldown or an active observation, then requests a fresh check automatically.

## Validation and rollout

PostgreSQL tests cover foreign/missing/retiring targets, new-mirror reservations,
both destination-lock race orders, legitimate owner lifecycle protection,
migration repair/replay/rollback and stale-worker fences. Capability tests cover
revocation before preparation/deployment, restoration, accepted-work recovery and
status reads. Retry tests cover negative evidence, exact destinations, cooldown,
newer passed/running checks and foreign/stale revision rejection.

Apply both catalog migrations before the control-plane update. Full pipeline
acceptance, generated-schema/SQL checks, migration policy checks and affected
package tests remain required. Production/provider qualification is unchanged.
