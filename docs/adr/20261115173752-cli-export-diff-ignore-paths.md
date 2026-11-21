# ADR-20261115173752: Explicit ignored paths in offline export diffs

Status: Accepted
Date: 2026-11-15

## Context

Volatile timestamps and counters can make saved inventory comparisons noisy.
Developers need explicit, reviewable field exclusions that also apply to drift
explanations, while retaining record identity and strict input validation.

## Decision

Add repeatable `--ignore-path POINTER` to offline `export diff`. Each value is a
non-root UTF-8 RFC 6901 pointer selecting an object field. Decode ~0 and ~1;
refuse malformed escapes, URI-fragment form and the empty root pointer. `/`
selects an empty field name. Other characters, including dots, percent signs
and *, are literal. No glob, JSONPath or URI decoding is introduced.

Validate the policy after parsing (including effective last-wins --key), before
opening either file. Reject the selected top-level key or any path beneath it,
using decoded tokens so escaped names cannot bypass the protection. Errors use
static text and do not echo rejected policy values. Deduplicate/sort paths for
an optional version-1 `ignored_paths` report field and an escaped human policy
line. Omission preserves all existing human/JSON bytes and explanation behavior.
The policy is visible even for empty inventories or unmatched paths. Report IDs
and field/exclusion names are intentional primary data; field values and input
paths remain absent.

Parse and validate the complete record before filtering. Duplicate object fields
at any depth, duplicate/invalid selected keys, malformed JSON/Unicode and read
failures remain errors even in excluded subtrees. Preserve the original selected
key identity separately, then remove selected object members before recomputing
record digests using the existing typed hash and exact decimal rules. Apply the
same projection before constructing explanation field-name/digest trees. Each
input is read once and remains untouched; no API/authentication/SDK/server or
receipt-lookup behavior changes.

Selecting a field removes the entire value and its presence, including objects
and arrays. Missing paths and scalar prefixes are no-ops. Preserve surviving
empty object containers, null distinctions and array order. Arrays are atomic:
a nonterminal selector encountering an array fails before output; numeric tokens
still name literal object fields. A terminal parent selection dominates nested
selections. This avoids array-index deletion and position-shift semantics.

Added/removed record IDs and counts are unaffected. Changed/unchanged records,
explanations, has_changes and --fail-on-change reflect the remaining projection.
Existing default exits, Boolean false/last-wins, interspersed flags, literal --,
short-write handling, Problem routing and native signals remain. No partial
report is produced on policy/input/read failure. No new row cap or disk index.

Store one immutable policy trie per comparison. Filtering without explanations
retains object trees only for one record; indexes still hold keys/digests. With
explanations, retain remaining field-name/digest trees as before. Memory includes
policy tokens, unique keys, the largest record and complete result; do not claim
constant memory. A projected match is not raw-file integrity verification.

## Verification

Core tests cover nested/whole-field projections, add/remove presence, surviving
containers, null/scalar/missing paths, exact/escaped/empty/control/Unicode names,
literal wildcard/dot names, arrays and numeric object fields, overlapping paths,
exact numbers and semantic equality, key protection, policy ownership/order,
record identity/additions/removals, strict excluded-input validation, errors,
input immutability, omitted-output byte preservation and short/failing writes.
Public main.Run tests and an isolated built-CLI journey cover parsing, help,
JSON/environment/human modes, offline profile selection, effective keys,
visible exclusions, protected-ID refusal and CI behavior without API reads.

Core race results qualify the comparison/input/format core only. Full hosted
exact-final-head CLI/build/lint/reference/journey qualification is mandatory;
small compile/reference adapters do not replace it. This feature is stacked on
export diff explanations. After that parent's verified squash merge, integrate
actual main normally and restart complete final-head qualification.
