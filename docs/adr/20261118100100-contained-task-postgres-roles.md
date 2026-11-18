# ADR-20261118100100: Restricted PostgreSQL roles for contained Task rehearsals

**Status:** Accepted
**Date:** 2026-11-18

## Context

A contained PostgreSQL fixture gives traditional managed workers a login
that inherits the fixture owner's privileges. Task runtimes need limited
write access to queue tables, and observers need read access to a smaller
set. The Task binding contract rejects membership in the migration owner
role, schema creation by runtime roles, and executable security-definer
functions. An owner-member login cannot satisfy that contract.

## Decision

Keep `read_write` for the primary fixture owner and add `task_runtime` and
`task_observer` for sibling workloads. A plan using either Task role must
give only the primary owner access and omit PostgreSQL fault injection.
The existing fixture initializer seeds the required regular Task tables
as the primary. The native adapter verifies table ownership before
granting runtime or observer access.

The adapter grants database connection, schema usage and table privileges
from the canonical Task allowlist. It gives neither login any role
membership. Setup removes PUBLIC grants on the disposable schema, its
relations and routines, and configures the owner's default privileges.
Fixture cleanup finds the logins by their resource-specific name even if
an earlier deletion already dropped the database.

This contract covers seeded fixtures and the initial grants. A runner
that applies migrations must recheck native ACLs and the candidate
before recording execution evidence. The managed-work preview neither
consumes owner-approved hosted rehearsal claims nor admits access to the
approved hosted application's database.

## Consequences

Existing contained scenarios using only `read_write` keep their original
behavior. Task scenarios can exercise separated runtime and observer
permissions on a disposable database without borrowing hosted bindings.
The enclosing scenario still controls provisioning, recorder fencing and
deletion. Changes to the Task table allowlist apply to both the native
binding probe and contained grants.
