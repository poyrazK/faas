# ADR-20261103231827: Repair interval fidelity, range callbacks and catalog rendering

- Status: accepted for internal operator qualification
- Date: 2026-11-04
- Extends: [catalog contracts](20261103224500-repair-catalog-contracts.md) and [scalar fidelity](20261103205518-repair-scalar-fidelity-and-checks.md)

## Problem

An interval recorded under SQL-standard output could be reconstructed with
different signs under PostgreSQL output, despite passing capture's local round
trip. A range canonicalization function could change after approval without
changing the named-type signature, then reinterpret restored values or leave
session effects during validation. Foreign keys referencing the managed row
version could cascade outside the reviewed selection. DateStyle-dependent
catalog rendering could reject unchanged schema while readiness reported ready.

## Decision

Capture contract version 3 pins IntervalStyle to postgres for native comparisons
and journal construction. Inverse selection, replacement, apply and native
observation use the same interpretation. These settings are function-local and
do not alter the application's callback or pooled connection settings. The
preceding version-2 row definitions remain archived for reviewed upgrades and
their canonical table-fingerprint contribution is preserved.

Older selected interval values or keys, including nested domains, composites,
ranges and multiranges, lack their original rendering context. They cannot
authorize planning, replacement or apply; validation and verification report
schema_changed. An owner upgrade never relabels their producer version, rewrites
images or changes approved documents. Historical receipts remain recoverable.

Qualify range input independently of index use. Follow all columns through
range/multirange subtypes, arrays, domains and composites. Reject application
canonicalizers, subtype-difference functions and subtype operator classes,
including aliases of builtin implementations. Inspect subtype constraints using
the existing expression policy. Named-type signatures include range callback
definitions and local tuple identities. An explicit owner upgrade may establish
a future baseline; it cannot certify earlier captures against a changed range
contract. Builtin ranges and qualified custom ranges remain supported.

Incoming update cascades are checked against selected fields and the managed
gregale_row_version column. That column changes on every repair and must not
grant authority to update child rows outside the plan.

New enrollments retain fingerprint_datestyle and fingerprint_intervalstyle
alongside fingerprint_timezone. Rendering uses those saved settings in both
runtime functions and trusted readiness SQL. New entries use ISO, YMD and
postgres. An upgrade fills missing settings only when the known catalog query
reproduces the saved fingerprint under the upgrade connection's rendering
settings. Otherwise the entry remains blocked for owner review. Rerun the owner
upgrade with the original rendering settings; never replace the fingerprint to
accept a mismatch. Saved hashes, documents and approvals remain unchanged.

## Validation

Regressions cover all four interval styles, nested interval types, old interval
history and receipt replay, matching and mismatching rendering upgrades, runtime
and writer readiness, real schema drift, managed-version foreign keys and range
callbacks across repair entry points. Docker acceptance asserts the exact
negative duration and range bounds after real dump/restore, rehearsal and apply.
Go, Node and Python consume regenerated readiness probes and the existing grants.
