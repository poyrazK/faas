# ADR-20261103185741: Reject lossy repair types and enforce runtime guards

- Status: accepted for internal operator qualification
- Date: 2026-11-03
- Extends: [selective repair](20261016-selective-data-repair.md), [capture fixes](20261103171817-repair-capture-and-transport-fixes.md), and [value comparison](20261103181657-repair-value-and-qualification-fixes.md)

## Problem

JSONB capture loses SQL array lower bounds and duplicate keys in PostgreSQL
`json`. Inverse updates can therefore restore different SQL values while
rehearsal passes and verification reports unchanged. These missing details
cannot be reconstructed from existing journals. This also affects arrays with
otherwise correct timestamp/timezone conversion.

Base capture and apply did not enforce origin replication mode. A replica-mode
connection could commit business writes with unchanged row versions and no
journal, despite a blocked readiness report. Inverse construction also used a
NULL whole-table record, causing an unrelated NOT NULL domain to fail before
the selected field could be assigned.

Check wrappers stripped a terminal semicolon only at the end of the query.
A valid `SELECT true; -- comment` failed inside the generated subquery. The
legacy Docker wrapper acquired this regression when scalar-result checking
was tightened; the named evaluator had the same limitation.

## Decision

Selected fields and primary keys containing arrays or plain `json` are outside
this preview's supported contract. One catalog predicate follows domains and
composite members recursively. Fresh enrollment rejects these types. Existing
enrollments are blocked by both readiness profiles, capture, planning,
replacement and apply. Validation and verification report `schema_changed`
without claiming the recorded values are lossless. JSONB and supported scalar
fields remain available. Unselected arrays and plain-json columns are permitted
and retain their original native values during repair.

This narrows the timestamp-array coverage described in the value-comparison
decision: scalar timestamptz comparison remains supported, while an array
capture requires a future versioned format that preserves bounds. No upgrade
rewrites fingerprints, journals, plan digests, approvals or historical receipts.
Existing lossy evidence cannot be upgraded into proof of the original values.

Enrollment qualification at execution time requires
`session_replication_role=origin`, before capture callbacks or inverse writes.
Finalization checks it again, so a callback that disables row triggers aborts.
An already committed apply receipt remains recoverable without write readiness.
The fingerprinted business-row trigger implementations remain unchanged.

Inverse assignments populate the locked, current native row instead of a NULL
whole-table record. Only selected fields are assigned, preserving unrelated
domains and native array/json representations. PostgreSQL constraints remain
authoritative.

The owner upgrade adds `check_query(text)`. It identifies statement terminators
outside quoted strings, identifiers, dollar quotes and nested comments, and
allows only comments, whitespace and empty statements after the SELECT.
Both SQL check wrappers use it; PostgreSQL still parses and executes the SELECT.
Checks retain their exact configured bytes and digests and must still return
one row, one column, PostgreSQL boolean type and true value. Qualified executor
roles need execute permission on this helper; capture-only and verification-only
roles do not.

## Validation and activation

Regressions cover plain-json fields, array bounds, timestamp arrays, array keys,
domains, nested composite types, legacy approved plans and historical receipts.
They verify blocked execution without evidence rewriting or business callbacks.
Origin-mode tests cover rejection, mid-callback mode changes, rollback and
committed receipt replay. Domain tests preserve an unselected NOT NULL domain,
array bounds and duplicate JSON keys. Check tests include nested comments,
quoted semicolons, escaped strings, Unicode dollar tags, malformed SQL, extra
statements and scalar-result shape failures.

Real isolated Docker journeys exercise legacy and named terminated checks and
repair with unrelated domain/array/json values. The core/API race suites and
all three SDK capture integrations remain required. This does not enable
customer preview or change native VM lifecycle or its activation gates.

## Capture-image amendment

[ADR-20261103193020](20261103193020-repair-native-capture-images.md) strengthens
the row triggers with native undeclared-field comparison and precise float
capture, while preserving their canonical fingerprint contribution. It also
adds producer provenance instead of treating older potentially rounded or
incomplete journals as newly proven. The selected-type restrictions above remain.
