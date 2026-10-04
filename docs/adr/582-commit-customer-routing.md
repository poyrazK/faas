# ADR-582: Versioned business-key and customer routing for Commit

- **Status:** Accepted, operator qualification only
- **Date:** 2026-10-03
- **Extends:** ADR-430

## Context

Commit currently serializes every event from a source on one account-scoped
lane. Customer-facing applications need unrelated orders to proceed independently,
while work concerning the same order must coordinate within the customer's scope.
Customer identity must be authorized separately from ordinary application data.

## Decision

A source fixes its application, operation policy, `contract_version` and
`allow_tenant_selection` when created. Reusing a source name with different
bindings returns a conflict. Existing sources default to version 1, retain their
source-wide lane and reject routing metadata. Sources cannot be upgraded in place;
create and bind a new source through an explicit owner-controlled migration.

Version 2 requires a `routing` object with `version: 2` and a typed scalar `key`.
The existing work-policy scalar canonicalization and 256-byte canonical key bound
apply. Numbers with equivalent values share a lane; strings, numbers and booleans
remain distinct. Policy identity and scope are part of the lane identity.

An account-scoped source forbids a customer selector. Setting
`allow_tenant_selection: true` grants the trusted producer database account-owner
routing authority. This requires a platform-tenant-scoped queue policy and version
2. Each event must select an active tenant in that account with an active surface
linked to the fixed target application. Tenant-required request applications are
supported by these sources. Jobs and environment-scoped policies remain unsupported.
This grant trusts all writers to the source outbox to select authorized customers;
it is unsuitable for direct writes by untrusted customer clients. Event `data`
never grants identity, overrides a destination, or selects a release.

`apid` and the managed relay preserve the same routing metadata. Admission locks
the account, source, active tenant and app link in the existing PostgreSQL
transaction. The operation receives the selected platform tenant and the existing
resolved deployment pins. Receipt insertion and operation admission remain atomic.
Version 2 uses `(source UUID, event UUID)` for owner idempotency so separate sources
can safely share a business lane. Version 1's owner identity is unchanged.

Receipts retain normalized routing independently of event type and payload. A
repeated identity must match all three. Replay recovers the original operation
before pause, tenant suspension, policy lifecycle, release resolution or quota
checks. It does not reopen completed work. Enabled sources prevent incompatible
policy scope changes. Paused sources retain their immutable authority.

Version 2 sources may be enabled only after every serving `apid` and `schedd`
uses this contract. Pause them before a binary rollback: older binaries do not
validate version 2 metadata. The retained migration is not a binary compatibility
mechanism.

The customer schema adds nullable JSONB `routing`, with a shape CHECK. Owners
explicitly run `pkg/commit/schema_routing_upgrade.sql` for older outboxes. The
relay never performs DDL. Version 2 schema qualification requires the column;
version 1 relay and insert paths continue to support outboxes without it.

## Consequences and validation

Applications can delete per-customer serialization and post-commit publishing
code while retaining ownership of their business transaction. This slice does
not implement effect delivery adapters, a new timeline UI, automatic coordinated
releases, or exactly-once external side effects. The existing Commit promotion
and native KVM qualification gates remain in force.

PostgreSQL acceptance tests cover concurrent identity recovery, typed key lanes,
cross-customer isolation, source identity separation, immutable authority,
foreign/unlinked/suspended customers, routing conflicts, migration replay and
legacy schema compatibility. API/relay and Go/Node/Python transaction helpers
carry routing in the actual customer business transaction.
