# ADR-20261103193020: Native repair comparisons and precise capture provenance

- Status: accepted for internal operator qualification
- Date: 2026-11-03
- Extends: [selective repair](20261016-selective-data-repair.md), [value comparison](20261103181657-repair-value-and-qualification-fixes.md), and [runtime guards](20261103185741-repair-lossless-types-and-runtime-guards.md)

## Problem

Writer `extra_float_digits=0` rounded actual old float values before journaling.
A repair could restore that rounded value while rehearsal and verification
passed. The old JSONB comparison of undeclared fields also ignored changes to
unselected array bounds and raw-json duplicate keys. Converting whole rows to
JSONB broke ordinary updates on valid unselected `json` containing `\u0000`.
Unqualified whole-row aliases collided with application columns named `t` or `r`.

## Decision

The guard and capture triggers now compare only the relevant native fields as
record text, under function-local `extra_float_digits=3`. Primary-key comparison
does not inspect unrelated values. Capture completeness retains array bounds and
raw-json spelling instead of normalizing them to JSONB. An undeclared native
change aborts the transaction, including when a caller swallows the callback
error. Selected fields and keys alone are converted to journal JSONB, at full
float precision. Selected arrays/plain-json fields remain unsupported as decided
previously; unchanged unselected native values remain permitted.

Planning and apply project only the selected expected values and managed row
version. Every field reference is qualified. Normalization uses an explicit
whole-row record reference. Planning, normalization, replacement, validation,
apply and verification also use function-local full float precision, preserving
the calling session's settings on return. The current native row remains the
base for inverse assignment, preserving unrelated domains and raw values.

The owner upgrade installs the new trigger definitions and compatibility
fingerprint. That fingerprint preserves the original contribution only for
the exact reviewed trigger bodies and complete catalog attributes. Modified
bodies or settings do not gain compatibility. Stored enrollment fingerprints,
source images, plan documents, digests, approvals and receipts are not rewritten.
Capture additionally requires the current trigger implementations, so a downgrade
to the recognized old implementation cannot certify a new capture. Unknown
installed trigger/fingerprint definitions abort the upgrade atomically with
`repair_row_contract_upgrade_requires_review`; the owner must inspect them.

A nullable `operations.capture_contract_version` distinguishes prior producers
from the new contract. Existing rows retain NULL. New source and repair operations
record version 1 only after current-contract qualification. Provenance stays
immutable with finalized operation metadata and survives payload retention.

Old captures on tables containing floats, arrays or plain `json`, including
domain/composite members and unselected columns, cannot establish full precision
or completeness. New planning, replacement and apply reject that unproven
history. Validation and verification return `schema_changed` without claiming
an exact observation. Safe legacy scalar captures retain their approvals and
can still be repaired. Historical committed receipts remain readable/replayable;
no upgrade fabricates lost values or new proof for old captures.

Doctor and the generated Go, Node and Python writer probes verify the required
function-local settings and the new metadata column. Apply the explicit owner
upgrade before deploying matching executor/SDK probes. Existing role grants
remain sufficient; this decision adds no new runtime function names or grants.

## Validation and activation

Regressions exercise ordinary writes and full repair with valid unselected
Unicode-escape JSON, array bounds, alias columns, quoted dollar identifiers and
writer/executor precision settings 0, -15 and 3. Rehearsal, validation,
replacement, inverse apply and verification preserve exact float values.
Undeclared array, JSON and float changes roll back without a retained capture.

Upgrade tests retain safe legacy approval and digest bytes, reject unproven
rounded float history, preserve historical receipt recovery, detect downgraded
triggers and refuse unknown bodies/settings without partial replacement.
Real isolated Docker rehearsal, all three SDK capture integrations and the
core/API race suites remain qualification evidence. This does not activate
customer preview or change the native VM qualification gates.
