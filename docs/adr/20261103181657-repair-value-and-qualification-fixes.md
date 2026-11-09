# ADR-20261103181657: Repair value comparison, index qualification and failover

- Status: accepted for internal operator qualification
- Date: 2026-11-03
- Extends: [selective data repair](20261016-selective-data-repair.md), [replacement](2026101702-repair-replacement.md), and [capture and transport fixes](20261103171817-repair-capture-and-transport-fixes.md)

## Problem

Captured timestamptz JSON depends on the writer's timezone. Comparing that JSON
with another session's output incorrectly excludes an unchanged instant and
rejects validation, replacement, apply or post-apply verification. Rewriting
captured JSON or frozen plans would invalidate their historical digests.

Standalone preparation accepts independently namespaced plan and source IDs,
including equal UUIDs. Replacement committed a child and fenced that parent,
but Go lineage validation rejected the result after commit.

Enrollment's unsupported-expression check inspected constraints and defaults,
but omitted expression indexes and partial-index predicates. Those indexes are
also absent from the historical fingerprint, so their custom functions could
change while readiness and validation continued to accept the table.

Snapshot export passed only pgx's first host to libpq. The executor could use a
healthy fallback while every rehearsal failed against the unavailable first
host. Generic URI parsing also rejected some multi-host IPv6 authorities.

## Decision

The explicit owner upgrade adds `normalize_values(regclass,jsonb)`. It converts
only recorded fields through their enrolled PostgreSQL types using
`jsonb_to_record`, then emits JSON in the observing session. Planning, replacement,
freshness, apply and verification compare this normalized expected value with
the current row. Timestamp arrays and NULL values use the same conversion.
Actual values and versions still determine conflicts. Row triggers, stored
fingerprints, journals, plan documents, digests, exclusions, approvals and
receipts remain unchanged. Previously excluded rows require a new plan; existing
plans do not promote exclusions. Qualified executor and observation roles need
execute permission on the new function; capture-only writers do not.

Replacement lineage permits a parent plan ID equal to the source operation ID.
The child must remain distinct from both, and same-ID replay retains the
committed child's immutable selection and receipt.

One catalog predicate qualifies function dependencies of constraints, defaults,
index expressions and index predicates. Fresh enrollment rejects unsupported
functions. Existing enrollment checks, executor and writer readiness, freshness
and verification block them without invoking application functions or modifying
historical fingerprints. Built-in immutable expressions and ordinary indexes
remain supported. The generated Go, Node and Python probes use this predicate.

Snapshot export passes the ordered host/port list to libpq and preserves
`target_session_attrs`. Adjacent pgx fallbacks for TLS/plaintext alternatives
collapse to one address because libpq already implements `sslmode`. pgx qualifies
the complete URI; generic URI parsing is used only for query options.
Credentials remain in the subprocess environment and never enter its arguments
or the isolated rehearsal server.

## Validation and activation

PostgreSQL regressions exercise capture, owner upgrade, validation, replacement,
apply and verification across UTC, Istanbul, New York and Kathmandu, including
timestamp arrays, NULL, JSON fields and actual timestamp drift.
Replacement tests cover standalone identity reuse, replay, history and apply.
Catalog tests cover custom expression indexes and predicates, legacy enrollment,
readiness and execution, alongside supported built-in and ordinary indexes.
Snapshot tests cover ordered ports, TLS alternatives, query host overrides and
IPv6 authorities; local PG16 qualification also exports through a healthy
fallback after the first endpoint refuses connections.

This change does not enable customer preview or alter native VM lifecycle.
Production activation retains its existing qualification requirements.

## Type-contract amendment

[ADR-20261103185741](20261103185741-repair-lossless-types-and-runtime-guards.md)
supersedes the selected-array coverage above. JSON capture does not retain SQL
array bounds, so arrays and plain-json fields, including nested domain/composite
members, are rejected until a lossless capture format is available. Scalar
timestamp comparison and preservation of unselected native values remain supported.
