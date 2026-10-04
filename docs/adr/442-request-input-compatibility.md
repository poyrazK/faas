# ADR-442: Directional request compatibility in preview reports

- Status: accepted
- Date: 2026-10-01
- Related: ADR-435 (preview reports), ADR-441 (review priorities)
- Validation/union coverage extended by [ADR-443](443-request-validation-compatibility.md).

## Context

Response comparison cannot determine request compatibility. Adding a required
input or narrowing accepted values can break existing callers without changing
a response. Customers need actionable review findings and an independent CI
gate, while incomplete OpenAPI evidence must remain visible. Runtime test
samples and source references cannot prove compatibility for all inputs.

## Decision

Add `openapidiff.CompareRequests` as a separate, bounded comparator over captured
OpenAPI 3.0 and 3.1 documents. Preserve the existing response comparator and its
server promotion gates. Resolve only supplied local component references; retain
the raw loaded document internally for those references and inherited security.
Never fetch an external reference or return original schema values.

Compare required bodies/fields/query/header/cookie parameters, core types,
scalar enums, nullability, object properties with boolean additionalProperties,
array items, and media coverage with most-specific wildcard selection. Normalize
parameter inheritance, operation overrides, header identity, annotations,
declaration order, numeric enum equality, and default serialization settings.
Use directional input acceptance rules rather than response property rules.
Required read-only fields in 3.0 are response-only. A newly documented path
parameter cannot prove a new requirement for an existing path segment.

Unsupported validation keywords, composed schemas, schema-valued additional
properties, complex enums, read-only input semantics, custom dialects, cyclic or
external references, body encoding, parameter content, serialization changes,
removed input declarations, and effective security changes remain unknown in
this increment. [ADR-444](444-declared-route-security-comparison.md) subsequently
moves authentication declarations into a separate bounded comparator.
Unresolved parameter identities prevent claiming a new required parameter.
Known independent restrictions can coexist with unknown schema evidence.
Referenced path items and unparsed operations make comparison unavailable.

Centralize all work and output bounds in `pkg/api/limits.go`. Crossing aggregate work or finding bounds
returns no partial request comparison. Invalid or oversized individual metadata
produces controlled unknown findings. Preview reports retain existing response
facts and show unavailable request evidence; missing captures cannot be replaced
by current policy or traffic-derived routes.

Preview reports now always use envelope version 3. The optional source-impact
input remains version 2. Add separate request evidence and per-route status,
completeness, change, and controlled findings. Preserve existing contract,
source, policy, traffic, and candidate-receipt provenance. Findings include only
codes and metadata locations, never enum values, examples, defaults, credentials,
reference URLs, schema excerpts, or receipt errors. Escape metadata for Markdown.

Request restrictions are blockers even with passing candidate samples. Unknown
requests need evidence. Supported widening alone does not require review.
Generalize the existing review queue so request findings work without a source
artifact; preserve independent source review and baseline traffic ordering.
Report request_breaking_changes without overriding response breaks, candidate
test failures, or policy violations.

Add --fail-on-request-breaking for known supported request restrictions. Keep
--fail-on-breaking scoped to existing response breaks and route removals.
--fail-on-incomplete includes unresolved requests and all other review needs.
Emit the report before applying gates. The command uses existing account-scoped
GETs, executes no application requests or tests, and writes no platform state.

## Consequences

Customers can catch declared client-input breaks during preview review, identify
the affected parameter/field, and separate known restrictions from missing
analysis. CLI consumers must accept envelope version 3. Supported checks are
conservative and do not attest to runtime validation, business behavior, or
universal compatibility. Common composed or formatted schemas require manual
review until the comparator supports their semantics.

## Validation

Tests cover widening and narrowing, required inputs, request-specific object
rules, arrays, media wildcards and overrides, local references and escapes,
parameter inheritance and serialization, scalar enum equality and privacy,
3.0/3.1 nullability and read-only requirements, inherited security, malformed or
unresolved evidence, cycles and dialects, immutable/deterministic normalization,
and aggregate comparison bounds with no partial results. CLI tests exercise output
before gate exit, independent response/request gates, passing candidate samples,
source and policy priority preservation, missing captures, bounded failures,
version 3 output, redaction, and Markdown escaping.
