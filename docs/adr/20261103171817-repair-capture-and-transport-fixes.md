# ADR-20261103171817: Close selective repair capture and transport gaps

- Status: accepted for internal operator qualification
- Date: 2026-11-03
- Extends: [selective data repair](20261016-selective-data-repair.md), [executor readiness](2026101707-repair-readiness.md), and [capture readiness](2026101708-repair-capture-readiness.md)

## Problem

Row triggers do not observe TRUNCATE. An enrolled capture could commit deletion
without a journal, including a truncate after capture finalization. Covering
primary-key indexes incorrectly classified INCLUDE columns as immutable keys.
Generated columns enrolled and appeared ready but their recomputed values made
capture reject a declared field update. Identity primary keys were incorrectly
rejected by both readiness profiles.

The standalone review page omitted its doctor script route. HTML escaping and
duplicated presentation values could inflate a valid four-MiB capture past the
existing sixteen-MiB transport bound. The legacy Docker check accepted extra
columns and text-to-boolean conversion that live apply rejects.

## Decision

The explicit owner upgrade adds nullable `operations.capture_relation_filenode`;
retained legacy operations keep NULL. New source and repair operations record
the enrolled relation's current storage ID. Finalization and the deferred
operation constraint verify that storage is unchanged. TRUNCATE and physical
rewrites abort the enclosing capture transaction. The existing relation lock
excludes concurrent truncation. Capture requires the pg_class row to predate
its enclosing transaction, including younger savepoint subtransactions, using
PostgreSQL's wraparound-aware xid age: PostgreSQL can truncate newly created or already
rewritten storage in place, so installation and enrollment must commit before
capture starts. Savepoint rollback restores storage and remains supported.

The upgrade does not replace fingerprinted business-row triggers or rewrite
stored fingerprints, captured values, plan documents, digests, approvals or
receipts. It corrects exactly the known legacy INCLUDE metadata after taking
the enrollment lock and verifying its live fingerprint. New enrollment extracts
only `indnkeyatts` primary-key columns. Historical captured keys remain unchanged
presentation evidence; apply still guards the original row UUID and version.

Generated-column tables are explicitly unsupported. Fresh enrollment rejects
them, existing enrollments report blocked in both readiness profiles, and capture
refuses them before invoking application code. Identity attributes are permitted
on primary keys, while selected repair fields remain writable ordinary columns.
The generated Go, Node and Python capture probes share this contract.

All plan-bearing HTTP responses use JSON encoding without HTML escaping. They
retain application/json, no-store, nosniff and existing text-only browser
rendering. Rehearsal headers additionally omit presentation duplicates and use
the same encoding. Logical capture, plan, request and transport bounds do not
increase; immutable values and digests do not change.

The standalone server serves every script referenced by its review shell. The
legacy Docker check validates one row, one column, PostgreSQL boolean type and
true value before publishing successful evidence. Live checks explicitly inspect
column count and type as well. Legacy query bytes and plan/check digests stay
unchanged, and invalid results roll back every inverse write.

## Validation and activation

PostgreSQL regressions cover truncation before, during and after capture, empty
tables, savepoint rollback, committed enrollment, covering-index upgrade and
approval replay, generated-column rejection, and both identity-key modes.
Executor tests fetch every review-shell dependency and round-trip a capture
near its byte limit through standalone, managed and rehearsal transports.
The same scalar-result matrix exercises live apply and real isolated Docker
rehearsal. Existing capture, plan, approval, retention, replacement and receipt
tests remain required.

This change does not enable customer preview or alter native VM lifecycle.
Dedicated-host KVM and crash-recovery qualification remain separate activation
requirements.

## Capture-trigger amendment

[ADR-20261103193020](20261103193020-repair-native-capture-images.md) replaces the
known trigger implementations for native comparisons and precise float images.
The explicit compatibility fingerprint retains their original contribution
only for exact reviewed implementations. Stored evidence and fingerprints remain
unchanged; new captures carry producer provenance, and unproven risky legacy
captures cannot acquire write authority.
