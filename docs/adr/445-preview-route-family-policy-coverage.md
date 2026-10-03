# ADR-445: Preview route-family policy coverage

- Status: accepted
- Date: 2026-10-01
- Related: ADR-435 (preview reports), ADR-436 (route requirements), ADR-437 (policy plans)

## Context

Concrete requirements establish policy for a single request path. They cannot
prove protection for every parameter value or automatically assign newly added
operations. Customers need a release check that finds unassigned routes and
policy coverage gaps without sending traffic or changing app configuration.

## Decision

Add a preview-only requirements version 2. Literal path prefixes and explicit
methods select groups with existing authentication, throttle and budget checks.
Groups overlap conjunctively. Optional concrete version 1 entries remain exact
assignments. Explicit public exceptions override groups for their exact captured
method/path and require a local rationale; the rationale is never serialized.
Exceptions declare customer intent and do not change or establish runtime access.

Every selected captured candidate operation needs an assignment. Use only the
candidate capture, independent of baseline availability, with deployment and
document-hash provenance. Exclude removed baseline and current policy-observed
routes. Missing/empty/truncated captures, referenced path items or operations,
unparsed methods, and unsupported versions make inventory unavailable. No
partial inventory can establish full coverage. Empty groups violate the check;
stale exact assignments are unknown. Preserve the existing requirements gate
and output-before-exit behavior. Keep the outer preview report at version 4;
the opt-in requirements section is independently versioned.

Respect effective root/path/operation server overrides. Non-root or variable
server paths remain unknown without a declared mapping to gateway paths; never
drop a server prefix to claim protection on a different path. Root server URLs
are checked only within the declared platform-host scope and never serialized.
Equivalent template hierarchies with different parameter names invalidate the
inventory. This follows [OpenAPI path construction and identity rules](https://spec.openapis.org/oas/v3.1.0.html#paths-object).

Prove policy selection across canonical decoded path families containing only
nonempty whole-segment parameters. Support literal gateway selectors,
whole-segment stars, and recursive trailing /* using actual gateway semantics.
Select the lowest-priority full-family winner only if no higher/equal candidate
can select a subset, vary with headers or tie. Lower-precedence partial rules do
not affect an unconditional full winner. Validate request-context rules with
the same proof before applying existing action, throttle and budget checks to
a representative. A representative is never itself evidence of coverage.
Unsupported families/globs and ambiguous selection stay unknown.

This establishes current platform-host configuration, not historical deployment
policy, application authorization, runtime enforcement, custom hosts, implicit
methods, encoded path aliases or framework normalization. Preserve bounded
overflow throttle behavior and configured budget baseline scope. Never copy
raw actions, header selectors, JWT metadata, credentials or exception rationales
into shareable reports.

All new limits live in pkg/api/limits.go: 100 groups, 2,000 captured operations,
1,000 rules, 10,000 findings, one million work nodes, 64 path segments, 2,048 path
bytes, 128 name bytes, 1,024 rationale bytes, 4,096 metadata bytes, and 16 MiB
aggregate work bytes. Count selector visits/comparisons and repeated selected
action processing. Discard partial rows on aggregate exhaustion.

Keep API, SDK, server policy planning and apply requirements version 1. Group
remediation requires a separate design; the preview evaluator is read-only and
uses the same two optional authenticated app/rule reads as concrete checks.

## Consequences

New captured routes automatically receive group checks or a coverage violation.
Customers can distinguish missing policy, incomplete family evidence and
deliberately public operations. Unsupported selector syntax may require manual
review even if some paths are protected. Static captured inventory is limited
to declared operations and cannot discover undeclared runtime endpoints.

Tests exercise strict formats, exact exceptions, overlapping requirements,
candidate-only provenance, precedence/mutations/privacy, bounded failure and
CLI gates. A finite equality-class oracle compares family proofs with the
gateway matcher. Existing version 1 and planner rejection checks preserve
compatibility.

ADR-446 subsequently extends this coverage model into server planning and transactional apply.
