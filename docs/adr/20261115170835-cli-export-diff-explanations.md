# ADR-20261115170835: Field explanations for offline export diffs

Status: Accepted
Date: 2026-11-15

## Context

Offline export comparison identifies changed record IDs. Developers need field
locations to diagnose that drift without putting row values into CI reports.

## Decision

Add opt-in `gregale export diff BEFORE AFTER --explain`. Retain the existing
schema version, comparison semantics, ID arrays, summary, exit codes and
--fail-on-change behavior. Omission and explicit false preserve existing human
and JSON bytes. Standard interspersed parsing, literal --, Boolean false and
last-value-wins handling remain. Authentication and API access remain absent.

Enabled JSON reports add `explanations: [{id, changes: [{path, kind}]}]`, including
an explicit empty array if no records changed. Only records present in both
files with unequal digests have explanations. IDs retain the existing typed
canonical lexical order and exact numeric representation. Added/removed record
IDs remain in their existing arrays. Kinds are added, removed and changed.

Paths are RFC 6901 JSON Pointers over decoded field names: escape ~ as ~0 and /
as ~1, including / for an empty field name. Sort changes by escaped path.
Recurse when both values are objects. Missing fields produce one added/removed
entry at that field, even when their value is an object. Replacements across
object/scalar/array types produce one changed entry. Arrays are atomic fields;
order differences change the array field, without exposing element indices or
values. Empty objects, null and absent fields remain distinct.

Reuse the existing typed SHA-256 comparison, duplicate field/key detection,
strict Unicode and exact coefficient/exponent numeric normalization. Equivalent
numbers and object-field ordering do not create explanations. Validate both
inputs before output. Emit no partial report on invalid input or read failure.
Writes retain short-write detection and existing Problem/native signal behavior.
No new classifier, SDK, server, receipt lookup, file mutation or replay is added.

Retain a digest and object field-name tree only in explain mode; strip scalar
values and array contents after parsing each record. Default indexes retain
only their existing record key/digest. Explain memory grows with all indexed
object fields plus the existing keys and largest record, and with the complete
result; do not claim constant memory. Each file is read once. Human paths use
JSON string escaping. IDs and field names are intentional result data; field
values, row bodies and input paths never enter explanations.

## Verification

Core tests cover nested objects, all three kinds, missing/null/empty objects,
whole-array/type replacement behavior, escaped/empty/control/Unicode field names,
exact large numbers and fractions, semantic equality, deterministic row/field
ordering, numeric versus string IDs, explicit empty explanations, unchanged
default JSON/human bytes, invalid/duplicate inputs, real read-only files and
short/failing writers. Public main.Run tests and an isolated built-CLI journey
cover flags/false/last-wins, JSON/environment/human modes, help, offline profile
selection, CI exit behavior, custom keys, literal separators and zero API reads.

A standalone core race pass does not qualify CLI dispatch, manifest visibility,
Problem routing or process journeys. Require full hosted exact-head CI and
reference freshness. This feature is stacked on the offline export diff; only
after that parent is verified merged, integrate its actual squash through a
normal actual-main merge and repeat final qualification.
