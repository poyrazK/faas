# ADR-20261115163305: Offline comparison of CLI export inventories

Status: Accepted
Date: 2026-11-15

## Context

Saved JSON Lines exports and integrity receipts support archived inventories.
Developers also need to identify additions, removals and changes between two
files, and use that comparison in CI without re-reading a live API.

## Decision

Add `gregale export diff BEFORE AFTER --key FIELD --fail-on-change`. The key
option defaults to `id` and names one literal top-level field, not a JSONPath
expression. Its value must be a nonempty string or JSON number in every row.
String and numeric keys are distinct; numeric spellings with the same decimal
value identify the same key. Duplicate keys within either file are rejected.

Both inputs are local regular files, opened read-only through the existing
verification input helper. Raw `-` is refused; `./-` can name a file. Existing
interspersed parsing, literal `--`, last-value-wins and Boolean `=false` behavior
remain. Dispatch shares the offline export group before connection validation.
No authentication, API request, SDK change or file modification is involved.

Require one UTF-8 JSON object per line. Empty files, CRLF and a final line without
newline are accepted. Reject blank/multiline/malformed rows, missing/invalid keys,
duplicate object fields at any depth and unpaired UTF-16 escapes before emitting
a report. Errors identify before/after and line number without echoing rejected
keys or row contents. Valid surrogate pairs and equivalent Unicode escapes use
the same decoded strings.

Compare logical JSON values. Ignore row order and object-field order, preserve
array order, and distinguish strings from numbers. Normalize decimal numbers
using coefficient/exponent strings and an arbitrary-precision exponent, without
floating point or exponent-sized expansion. Thus 1, 1.0 and 1e0 are equal; integers
beyond 2^53 and tiny/huge exponent values retain their exact distinctions. Signed
zeroes are equal. Hash length-framed typed values and sorted object fields with
SHA-256. Retain one key and digest per row, plus temporary per-record parsing
state; memory grows with unique keys and the largest record. Do not claim
constant-memory traversal, add a record cap or create a disk index implicitly.

Reports contain schema_version 1, key, has_changes, before/after and category
counts, and added/removed/changed ID arrays. Empty arrays and false/zero values
are explicit. Ordering is deterministic by typed canonical identity, not a
locale or numeric sort. Numeric IDs use exact canonical coefficient/exponent
JSON numbers. IDs are intentional result data; row bodies and input paths are
not output. Human IDs use their JSON-escaped representation, including controls.

Successful comparisons exit 0 even when changes exist. Opt-in --fail-on-change
returns 1 when changes exist, after writing the complete report. Invalid input
uses the existing local Problem/human diagnostic path and emits no report.
Output failures retain shared classification/native signals; detect short writes
and permit the existing possibility of torn stdout. No automatic receipt lookup,
repair, replay or follow-up action is performed. A diff compares supplied bytes'
JSON meaning, without an authenticity, snapshot-consistency or file-lock claim.
Use explicit export verification separately when integrity receipts are available.

## Verification

Core tests cover exact decimal equality/distinctions, huge exponents without
expansion, Unicode/field/row normalization, array ordering, duplicate fields and
keys, typed/custom keys, empty/large/CRLF/final records, invalid input/read errors,
deterministic IDs, human escaping, file immutability and broken/short outputs.
Public CLI tests cover default/explicit/environment JSON, human summaries,
fail-on-change including last-wins/false, custom fields, missing profiles,
literal separators, help, errors and zero API requests. A built-CLI isolated
journey checks both normal and CI behavior plus malformed/ambiguous inventories.

A standalone core pass is not full CLI, capability/discovery or journey evidence.
Require full hosted exact-final-integration qualification and generated-reference
freshness. This is stacked on the export-verification feature; preserve its
actual main squash merge before final qualification.
