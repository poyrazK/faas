# ADR-20261103200000: Pipeline replica identity and unfinished generation cleanup

- **Status:** implemented for local/staging qualification; production disabled
- **Date:** 2026-11-03
- **Amends:** ADR-1591

## Context

PostgreSQL cannot use a deferrable primary key as default replica identity.
Publishing such a table can reject subsequent source updates and deletes.
Primary-key indexes can also have nonkey INCLUDE columns; treating those as key
columns changes mirror nullability and can prevent initial copy or replication.

A failed resync can leave an earlier generation active while later resync
requests advance customer intent. Cleaning only the immediately preceding
generation can then report deletion with older native resources still present.
Native DROP SUBSCRIPTION also contacts its publisher using saved connection
information, which may contain a password revoked before cleanup starts.

## Decision

Require a valid, immediate primary-key index and discover only its key attributes,
using indnkeyatts to exclude INCLUDE attributes. Recheck source table compatibility
under the existing bounded source table lock before creating a publication, so
DDL after discovery cannot introduce an unusable replica identity at that boundary.

Before reconciling a later generation, recover all outstanding earlier generations
from the private mirror receipt, exact publication ownership markers and validated
subscription identities. Preserve the recorded table selection when available.
Retire recovered generations in order under the existing destination session lock
and catalog revision/lease fence. Ownership conflicts stop cleanup. No generation
or capacity reservation is retired merely because a later intent was accepted.

Share subscription connection validation between normal replication and cleanup.
Before native teardown, refresh the disabled subscription using the current sealed
source URL and the existing verified, address-pinned network policy. Never enable
a retiring subscription to refresh its credentials. Source and destination cleanup
must complete before deletion is acknowledged and capacity can be released.

## Validation and scope

Portable PostgreSQL tests cover key discovery, nullable INCLUDE columns and a
primary-key change after passing discovery. Native TLS/password-authenticated
tests cover initial copy and updates/deletes, credential rotation immediately
before resync or deletion, repeated resync after an outage, retained rows, native
resource cleanup and ownership rejection.

This changes no production admission, supported provider, capacity entitlement or
VM lifecycle. Live-provider and deployed operational qualification remain open.
