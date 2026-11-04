# ADR-20261103205518: Repair scalar fidelity and strict business checks

- Status: accepted for internal operator qualification
- Date: 2026-11-03
- Extends: [native capture images](20261103193020-repair-native-capture-images.md) and [domain qualification](20261103201136-repair-timezones-and-domain-qualification.md)

## Problem

A composite result column named `q` could replace the check wrapper's whole-row
reference. A SELECT returning false and that extra column could pass rehearsal
and live named checks. Custom operator implementations also escaped expression
qualification because PostgreSQL records operator dependencies separately from
direct function calls.

JSONB cannot retain floating-point negative zero. Full float output precision
therefore did not prove a lossless capture. JSONB equality additionally ignores
numeric scale: a journal retained `1.00` and `1.0`, but inverse selection omitted
that field and freshness/verification could miss the change.

## Decision

Both the named evaluator and the legacy Docker wrapper inspect the explicit
whole-row reference `q.*`. Extra columns remain errors, including composite
columns named `q`. Query bytes, manifest hashes and saved evidence stay intact;
live checks enforce the corrected shape even for an older approved plan.

Shared catalog qualification follows operator dependencies to their underlying
functions. Custom operators and custom/nonimmutable implementations are blocked
in table/domain constraints, defaults and index expressions/predicates. Builtin
immutable operators remain supported. Qualification never executes them.

Capture proves a native round trip for selected fields and keys before retaining
a journal row. It reconstructs the JSONB images using the original native row as
the base and compares native record text under full float output precision.
Lossy values, including negative zero in a selected domain/composite or key,
abort the transaction with `repair_capture_value_not_lossless`. Unselected
negative zero, raw JSON and array values retain their native representation.
The preview does not support journaling selected negative zero values.
The same rule rejects a selected JSONB literal `null`, which its journal cannot
distinguish from SQL NULL. SQL NULL and JSONB objects containing nested JSON
nulls remain supported.

New captures record `capture_contract_version=2`; NULL and version-1 evidence is
never relabeled. Catalog-only inspection follows recorded values together with
their domain/composite member types. Older float zeros and JSONB literal nulls
cannot establish their original native value. Integer zeros and nulls inside a
JSONB object remain unambiguous. New planning, replacement and apply reject ambiguous
history; validation/verification report `schema_changed`. Earlier NULL-producer
restrictions remain in force. Historical committed receipts remain recoverable.
NULL-producer captures on tables containing numeric or JSONB columns also lack
native completeness proof: their old JSONB comparison could miss undeclared
scale-only changes. That history cannot acquire new repair authority.

Inverse selection compares JSONB text to retain numeric scale. Live comparisons
use `values_match(anyelement,jsonb)`, reconstructing only the recorded fields on
the current native row and comparing native output. Planning, replacement,
validation, apply and verification therefore detect scale and zero-sign drift
without converting unrelated columns to JSONB.

Old candidate plans that omitted a scale-changing inverse field cannot authorize
apply/replacement or certify a complete restoration. A fresh plan identity can
recover those fields from the original lossless numeric journal, subject to the
current row/version guards and new rehearsal/approval. The upgrade never expands
an existing selection or rewrites approvals, plan digests or capture images.

New plan documents record `value_contract_version=2` so later compaction retains
proof of complete inverse selection. Older scale-sensitive plans and version-1
float captures cannot regain verification authority after their original
payload is purged; unavailable proof stays unavailable.

The exact reviewed version-1 row SQL is retained as an upgrade template. Reviewed
old/new producers contribute the same legacy fingerprint; unknown function
bodies/settings still stop the owner upgrade atomically.

The trusted function manifest is assembled once from the binary's installation
SQL and copied for each inspection. Repeated metadata construction must not
consume the readiness inspection deadline. It contains no customer state.

## Validation and activation

Regressions cover false/composite check results, live rollback, custom operators
through domains and indexes, negative-zero rejection/rollback, positive-zero
support, nested types, scale-only repair and native freshness/verification.
Upgrade tests preserve safe approvals and receipts, reject ambiguous zeros and
incomplete inverses, and recover numeric scale through a fresh plan. Real Docker
checks exercise malformed-check rejection and a scale-restoration invariant.

Apply the explicit owner upgrade before the matching executor and SDK probes.
Separate executor/freshness/verification roles need EXECUTE on
`gregale_repair.values_match(anyelement,jsonb)` in addition to existing grants.
Capture-only writer grants are unchanged. Customer preview activation and
native VM qualification gates are unchanged.
