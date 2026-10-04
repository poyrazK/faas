# ADR-488: Durable regional runtime quota receipt journal

Date: 2026-10-04
Status: Implementation in progress; operational admission enforcement pending.

## Context

ADR-487 authenticates directory quota authority. Regional delivery can be lost,
repeated or reordered across process restarts. Discarding an unknown retirement
would allow its delayed grant to revive an allowance the directory has retired.
A journal must preserve original identities beyond app and tenant collection.

## Decision

The regional receiver verifies the directory role, issuer key and complete
expected binding before writing. Only the state package implements its mutation
interface. The memory and PostgreSQL stores retain the complete original grant,
first accepted signed envelope, and any immutable retirement and its first
accepted envelope. An exact claim replay signed by an overlapping trusted key
returns the original receipt without rewriting authority. Retire-before-grant
creates a tombstone containing the original grant; a later matching grant
returns that tombstone and never changes it to live authority.

A regional app has one immutable directory/global owner/app/cell/tenant stream
and a monotonically increasing allocation-generation head. Registry generations
belong to allocations and may advance after registry re-verification. Unknown
older grants are rejected. Older unknown retirements are retained without
moving the head backward. Equal generations cannot identify two allocations.
New grants verify current local app/tenant/slug identity under the app-row lock.
Retirement remains safe after local identity changes or collection and does not
require a live app. Receipts and heads have no cascading app/tenant foreign key.
PostgreSQL serializes journal mutations per app, derives heads transactionally,
and protects immutable authority, tombstones, monotonic heads and retention from
SQL update, deletion, truncation and populated downgrade.

Every head has `admission_enabled = false`, enforced by a database CHECK. This
is an activation block on this new authority, not proof that existing instances
have drained or that all existing scheduler paths are fenced. The receiver has
no activation or drain-signing method. No public route or daemon configuration
is enabled by this change. Explicit regional consent, scheduler admission across
every path, accounting for in-flight operations and live instances, signed
transport/reconciliation, operator key loading and native failure/recovery
qualification must be integrated before activation becomes available.

## Validation

Run identical memory and PostgreSQL contracts for concurrent delivery, lost
responses, retire-before-grant, stale/equal generations, immutable stream and
retirement identity, key rotation, historical retirement policy and copied
snapshots. Verify PostgreSQL rollback, reconstruction from a new store instance,
SQL guards, app collection retention and empty/populated downgrade behavior.
