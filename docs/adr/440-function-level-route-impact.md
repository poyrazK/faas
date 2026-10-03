# ADR-440: Function references and semantic source changes for route impact

- Status: accepted
- Date: 2026-10-01
- Related: ADR-439 (static FastAPI route impact)

## Context

Module import evidence can flag every endpoint in a file when an unrelated
helper changes. Formatting and comment edits also produce broad warnings.
Developers need a smaller review list with enough evidence to understand
which function changes connect to each endpoint.

## Decision

Extend the local FastAPI adapter with AST fingerprints and a bounded graph
of module-level function references. Preserve isolated standard-library
parsing and the rule that customer source never executes.

Fingerprint ASTs without location attributes. Compare functions separately
from module initialization. For initialization fingerprints, replace the
bodies of module-level functions with placeholders while retaining their
bindings, decorators, arguments, defaults, and annotations. Keep raw changed
files and source fingerprints for audit, including formatting-only edits.

Resolve stable module-level references, imported aliases, relative imports,
simple re-exports, and unconditional local imports. Traverse handler functions
and explicit FastAPI dependency roots from both revisions. References include
direct calls and callbacks, so they express potential impact rather than
proof of runtime execution. Functions called by module initialization also
contribute roots; startup effects must not disappear simply because handlers
do not call the initialization helper.

Keep broader local module evidence for initialization changes and unmodeled
registration references such as class middleware and model/type metadata. Unresolved reachable calls,
shadowed/conditional bindings, dynamic lookups, custom decorators, nested
scopes, and mutations retain module fallback with explicit uncertainties.
Uncertainty for one handler does not turn unrelated resolved handlers into
unknown results. Registration completeness still governs route additions and
removals; function uncertainty independently makes the report incomplete.

Emit schema version 2 with changed symbols, function locations and reference
chains, evidence kinds, per-route precision, and uncertainties. Existing CLI
arguments, local-only behavior, export permissions, and CI gates remain.
Centralize symbol, edge, issue, depth, and report bounds in `pkg/api/limits.go`;
fail without returning a truncated report if any bound is exceeded.

## Consequences

Comments, formatting, and unrelated function-body edits stop flagging routes
through resolved function graphs. Shared helper and dependency changes produce
actionable function chains. Globals, imports, classes, and route setup can
still produce broad warnings because their import-time behavior matters.

This is static module-level analysis. Methods, dynamic dispatch, installed
package behavior, runtime configuration, and application data remain outside
the precise graph. Consumers of JSON must account for schema version 2 and
the narrower meaning of `source_changed`. `no_linked_changes` continues to
mean only that the supported model linked no changes, never that a route is
safe or unaffected.

## Validation

Integration tests exercise formatting and unrelated functions, sibling
handlers, imported aliases and relative re-exports, unconditional local
imports, transitive calls, callback references and cycles, FastAPI dependencies,
module initialization, removed references, ambiguous bindings, dynamic and
method calls, fallback isolation, deterministic output, bounds, CLI review
output, and CI gates. Existing Git capture, source privacy, isolated parsing,
registration, and report-export tests also run.
