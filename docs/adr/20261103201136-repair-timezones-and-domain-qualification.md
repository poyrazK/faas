# ADR-20261103201136: Repair fingerprint timezones and domain qualification

- Status: accepted for internal operator qualification
- Date: 2026-11-03
- Extends: [native capture images](20261103193020-repair-native-capture-images.md) and [expression qualification](20261103181657-repair-value-and-qualification-fixes.md)

## Problem

Catalog deparsing renders timestamp constants using the connection timezone.
The same table could therefore pass doctor and capture on one connection and
fail on another. Retention also hashed timezone-dependent record JSON, making
an unchanged preview fail purge on a connection using another timezone.

Expression qualification inspected table constraints but missed constraints
on column domains. Reconstructing a selected domain value during validation
could execute a custom constraint function, including one that leaves session
advisory locks held after the read-only observation returns.

## Decision

New enrollments pin fingerprint rendering to UTC. The owner-maintained registry
stores `fingerprint_timezone`; the fingerprint function renders in that zone
and restores the caller's setting on return. Doctor and the generated writer
probes inline the same trusted catalog query in their rolled-back snapshots.
They never invoke the installed fingerprint function to qualify it.

The owner upgrade records a legacy enrollment's timezone only when rendering
the current catalog under the upgrade connection's timezone reproduces its
existing fingerprint. It leaves unmatched entries NULL and blocked. The owner
can rerun the upgrade in the original enrollment timezone after reviewing the
catalog; matching enrollments are filled without rewriting fingerprint,
capture, plan or approval bytes. Actual schema drift is never adopted as a new
baseline. Only exact known fingerprint/trigger implementations are upgraded.

New retention previews freeze their rendering timezone inside the saved,
digest-bound document. Purge uses that timezone for the complete selection
recheck and restores the caller's setting. Existing previews remain unchanged:
use their original connection timezone or prepare and review a new preview.
No digest is silently reinterpreted, and all previous concurrency/state guards
remain enforced.

Custom/nonimmutable constraint functions on domains are rejected using catalog
dependencies, recursively through domain bases, composite members and arrays
of every column. The shared qualification applies before enrollment, capture,
planning, replacement, apply, validation and verification. Builtin immutable
domain checks remain supported. Previously admitted unsupported domains report
blocked/schema_changed without evaluating their functions. Saved committed
receipts remain recoverable.

## Validation and activation

Regressions cover timestamp defaults and constraints across connection zones,
legacy non-UTC approval preservation, unmatched upgrade rejection, real default
drift, domain nesting and observation before any custom-function evaluation.
Retention covers plan/journal digests, replacement timestamps, replay and
unchanged legacy previews. Core/API race tests and all SDK capture suites check
the shared generated probes. Apply the explicit owner upgrade before deploying
matching executor/SDK probes; existing whole-table registry SELECT grants and
runtime function grants remain sufficient. Customer preview activation and
native VM acceptance gates are unchanged.
