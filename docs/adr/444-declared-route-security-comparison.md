# ADR-444: Captured route authentication comparison

- Status: accepted
- Date: 2026-10-01
- Related: ADR-435 (preview reports), ADR-442 (request compatibility)

## Context

Preview request comparison classified any security-list change as unknown and
did not inspect referenced security scheme definitions. Customers needed manual
review for accidental anonymous access, credential/scope removal, and added
authentication requirements. Reordering equivalent alternatives also produced
noise. Captured OpenAPI declarations cannot establish runtime enforcement or
historical gateway configuration.

## Decision

Add a separate bounded CompareSecurity adapter in pkg/openapidiff and preview
report version 4. Remove security from request-shape comparison. The report adds
independent security availability, per-route security results, controlled
baseline/candidate summaries, human findings, and review priorities. Preserve
captured deployment IDs and document hashes as provenance. Keep current gateway
policy and optional requirements separate.

Resolve effective root/operation requirements in OpenAPI 3.0/3.1. Model each
requirement object as an AND of credentials and scope/role sets; the list is OR.
An empty declaration or absent effective requirement has no declared credential
requirement; an empty requirement object explicitly allows anonymous access.
Compare positive formulas through bounded conjunction implication. A strict
widening is a declared protection regression; strict narrowing restricts clients.
Equivalent order/duplicates/redundant alternatives do not count as changes.
Incomparable replacements remain unknown instead of ranking credential strength.

Validate referenced scheme definitions for API keys, HTTP basic/bearer, OAuth2,
OpenID Connect, and 3.1 mutual TLS. Normalize header and HTTP mechanism case.
Ignore descriptions, bearer-format hints, and extension semantics. Validate
required OAuth scopes against the supplied flows; compare 3.1 role requirements
as sets, without interpreting their application meaning. Track credential
identity by component name and supported definition. Local securitySchemes
references resolve within the supplied capture; never fetch external resources
or discovery metadata. Definition changes, including credential transport and
provider endpoints, require review even if security requirements are unchanged.
Relative flow/provider URLs remain unknown without the original document base URI.
Malformed/unsupported declarations cannot collapse into anonymous alternatives.
Known anonymous additions can coexist with unresolved candidate alternatives.

Independent limits live in pkg/api/limits.go: 2,000 routes, reference depth 64,
50,000 nodes including implication pairs/atom visits, 5,000 findings, 4,096 bytes
per semantic string, 16 MiB aggregate semantic metadata. Aggregate exhaustion
returns no partial comparison. Referenced path items or unparsed HTTP operations
make the comparison unavailable. Added/removed routes are not security-compared.

Keep names, scope/role values, URLs, actual header names, references, and extension
values private. Output uses controlled codes and authentication/kind summaries.
Known security findings become blockers; passing samples cannot erase them.
Add --fail-on-security-regression for declared reductions and include known
authentication client restrictions in --fail-on-request-breaking. Unknowns fail
only the existing --fail-on-incomplete gate when selected. Emit output before
gate exit. Preserve response/request-break, test-failure, and policy-violation
outcome precedence; independent flags inspect security fields directly.

No API/database/SDK, runtime enforcement, source analyzer, test runner, or server
promotion changes. The CLI uses the same existing account-scoped reads.

## Consequences

Customers can reject known declared authentication reductions before release
and review client migrations with route/traffic context. Findings remain bounded
declaration evidence; they do not assert actual exposure, correct authorization,
valid tokens, or universal authentication compatibility. Gateway enforcement
drift requires future deployment-bound policy snapshots.

## Validation

Table-driven tests cover inheritance/overrides, anonymous declarations, AND/OR
changes, scope/role sets, equivalent alternatives, credential definitions,
local/external/cyclic references, malformed metadata, independent bounds,
privacy, and immutability. An independent exhaustive truth-table oracle checks
4,096 implication comparisons. CLI tests verify version 4, six GET reads,
output-before-exit, independent gates, passing-sample blockers, traffic ordering,
outcome precedence, missing evidence, and redacted text/Markdown/JSON.
