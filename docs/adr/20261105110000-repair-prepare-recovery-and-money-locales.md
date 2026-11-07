# ADR-20261105110000: Repair preparation recovery and monetary locales

- Status: accepted for internal operator qualification
- Date: 2026-11-05
- Extends: [repair value rendering](20261103231827-repair-intervals-range-callbacks-and-rendering.md)

## Problem

Preparing a plan while another plan is open leaves the previous view active on
failure. Retrying can generate a new operation UUID, and Refresh from retained
history can read the previous historical plan instead of the pending operation.
A committed draft whose response was lost can therefore lose its recovery path.

PostgreSQL money output and input depend on lc_monetary, including currency
symbols, separators and fractional digits. A capture's local round trip does not
prove that another connection or an isolated rehearsal can interpret its journal.
Catalog defaults and checks containing money constants also render by locale.

## Decision

Beginning hosted preparation clears the previous plan and historical refresh
path. Retain the requested UUID and source across failed requests. Reuse that
UUID for a retry, and refresh the pending hosted operation. A different source
or a new preparation after successful recovery reserves a fresh identity.

Capture contract version 4 uses function-local lc_monetary=C for journal
construction, native comparison, inverse selection, replacement, apply,
validation and verification. This covers selected money fields and keys through
domains, composites, ranges and multiranges. Preserve native money bytes,
including with locales whose fractional digit count differs. The application's
callback and pooled connection keep their original settings.

Version-3 row definitions remain archived for exact owner upgrades and canonical
fingerprint compatibility. New enrollments record fingerprint_monetary=C.
Fingerprint rendering uses the saved monetary locale alongside the existing
DateStyle, IntervalStyle and timezone. An owner upgrade fills a missing monetary
locale only if that connection reproduces the existing fingerprint. Otherwise
keep the entry blocked until the owner repeats the upgrade under the original
locale. Never rewrite an enrollment fingerprint to accept a mismatch.

Earlier captures containing selected money fields or keys do not retain their
rendering context. They cannot authorize planning, replacement or apply;
validation and verification report schema_changed. Keep their journal bytes,
plan digests, approvals and receipts unchanged. Existing committed receipts
remain recoverable. Non-monetary historical captures retain their existing
eligibility. Regenerate all three leaf SDK readiness probes from the new source
contract, and apply the explicit owner schema upgrade before new clients.

## Validation

Browser regressions simulate a lost prepare response after a committed draft,
retry, duplicate submission, source changes and recovery from retained history.
PostgreSQL regressions exercise monetary fields, nested types, keys, catalog
rendering and connection-setting restoration across C, German and Japanese
locales when installed. Upgrade coverage checks historical evidence, replayed
receipts, safe non-monetary approvals and repeatable locale qualification.
