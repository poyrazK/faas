# ADR-443: Validation bounds and nullable request unions

- Status: accepted
- Date: 2026-10-01
- Related: ADR-442 (request compatibility)

## Context

Generated application contracts use numeric bounds, length/item limits, and
nullable unions. Treating those schemas as unknown limits the request CI gate's
usefulness. Comparing keyword values mechanically also creates false breaks:
integer > 0 and integer >= 1 accept the same values, and a tighter limit may
exclude none of a baseline's finite enum values.

## Decision

Extend the existing request comparator with minimum/maximum, exclusive numeric
bounds, minLength/maxLength, and minItems/maxItems. Preserve separate request
and response gates, captured revision provenance, and report version 3.

Use private rational numeric values and arbitrary-size integers for accepted
integer endpoints and count bounds. OpenAPI 3.0 exclusive bounds are boolean
modifiers; 3.1 exclusive bounds are numeric assertions that intersect inclusive
bounds. Select the stronger limit. Convert integer-only domains to inclusive
integer endpoints and compare continuous domains independently. Numeric values
are interpreted after capture/loader decoding; this does not recover precision
already lost upstream. String lengths use Unicode code points. Count minimum
zero is the default, and unrelated type-specific bounds are ignored after
validation.

Filter scalar enum values against supported baseline and candidate constraints.
Report tightening only when previously accepted enum values are excluded.
An enum covering all values in a bounded integer range, a numeric singleton,
or an empty-string-only schema is equivalent. Count candidate coverage without
expanding arbitrarily large ranges, charging enumeration to existing work bounds.
Remove empty numeric, string, and array domains from accepted types. An
integral numeric singleton has only integer values. A rejecting item schema
accepts only the empty array when zero items are allowed; do not compare item
restrictions when the baseline allows no items. Required rejecting properties
can empty a supported object domain. Nullable alternatives remain separate.

Fold only OpenAPI 3.1 anyOf with exactly two branches, one of which accepts only
null and the other of which has supported semantics. Support local component
references, either branch order, nested nullable fields/items, annotations, and
an empty non-null branch. This is recognition of one semantic shape rather than
a branch-count quota. Charge branch collections and recursion to the existing
centralized work/depth bounds. Generic unions, allOf, oneOf, validation siblings
on nullable union wrappers, formats, patterns, multipleOf, uniqueness, and other
unsupported keywords remain unknown. Malformed bounds do not become unrestricted
schemas. Exhausted aggregate bounds still discard the whole request comparison.

Add controlled numeric-minimum/maximum, string-length, and array-size findings
at normalized schema locations. Keep all actual limits and enum values private,
and use the existing review blockers and --fail-on-request-breaking gate.
Passing candidate samples cannot erase a declared restriction; supported
widening or equivalent rules do not require additional request review. The CLI
performs the same existing reads and executes no application requests/tests.
No server promotion, API, database, SDK, or source analyzer behavior changes.

## Consequences

Customers can catch more declared input restrictions in generated schemas while
keeping explicit uncertainty for complex validation. The comparer still evaluates
captured declarations, not runtime coercion, custom validators, business logic,
or universal compatibility. Nullable union normalization improves common model
coverage without claiming arbitrary JSON Schema subsumption.

## Validation

Regression tests cover numeric/size tightening and widening, inclusive/exclusive
bounds across OpenAPI versions, negative/fractional integer boundaries, large
integers, finite enums, Unicode and combining sequences, default/equivalent
rules, impossible domains, empty-array-only schemas, nested nullable models,
local references, branch order, unsupported composition, malformed bounds,
input immutability, privacy, and aggregate bounds. An independent acceptance
oracle checks all pairs of interval variants over integer and number domains.
CLI tests verify generated-model-shaped inputs, version 3, six read-only API
reads, output-before-gate exit, response/request gate separation, passing-sample
blockers, widening/equivalence acceptance, and redacted text/Markdown findings.
