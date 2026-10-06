# ADR-439: Static FastAPI route impact between Git source revisions

- Status: accepted
- Date: 2026-10-01
- Related: ADR-435 (preview reports), ADR-436 (route requirements),
  ADR-438 (transactional policy application)

## Context

Captured OpenAPI contracts and observed traffic identify endpoints but do not
connect a change in application source to handlers and shared local modules.
A contract can remain identical while a pricing helper or middleware changes
the behavior of several endpoints. Developers need an explainable route list
for review and targeted testing before deployment.

## Decision

Add a local, read-only `gregale routes impact [slug] --base REF` command,
initially for statically registered FastAPI HTTP routes. Resolve the baseline
and optional candidate to immutable Git commits; otherwise capture the
working tree, including unignored untracked Python sources. Select one
source/import root and one FastAPI entrypoint.

Use an embedded Python standard-library AST analyzer under `python3 -I -S`.
Only the analyzer executes; customer modules are never imported, evaluated,
or used for Python startup. Pass bounded source bytes through stdin, preserving
Python encoding declarations. No new server endpoints, schema, SDK surface,
or customer-intent writes are needed.

Resolve static application/router bindings, decorators, imported handlers,
router inclusion, and literal prefixes. Attach local import chains to changed
files from both revisions, so removed dependencies still contribute evidence.
Treat module imports as potential impact rather than function-call proof.
Keep sibling router imports out of unrelated application context traversal;
explicit middleware and dependency references provide shared roots.
Only registrations in statically imported modules enter the index;
conditionally imported registration modules remain explicit unknowns.

Emit versioned text/Markdown/JSON reports with source locations, deterministic
source fingerprints, issues, and CI gates. The app slug labels the report; it
does not assert a relationship to any deployment.

Dynamic or ambiguous registration is incomplete. Never infer a confirmed
route removal or addition from an incomplete static index. Never label
unlinked routes safe or unaffected. Non-Python changes are visible unknowns.
Exceeding source, graph, subprocess output, evidence, or time bounds fails
analysis without publishing a truncated success. Centralize bounds in
`pkg/api/limits.go`.

## Consequences

Developers get an evidence-backed route review list without starting the app,
installing dependencies, or contacting a platform API. This can become an
input for a separately owned behavioral test runner.

Python 3 and Git are required on the CLI machine. Static module analysis can
overestimate impact, especially when several handlers share one module.
Factories, dynamic routing, mounted apps, runtime state, installed dependency
behavior, and other frameworks need future adapters or runtime evidence.
Committed candidates provide stable snapshots; working-tree capture is
useful locally but is not atomic across files.

## Validation

Tests cover nested/aliased routers, imported handlers, relative imports,
re-exports, prefixes, shared middleware, transitive dependencies, sibling
isolation, additions/removals, incomplete indexes, source privacy, isolated
parsing, real Git commits and working-tree edits, ignored/untracked files,
symlinks, bounds, report determinism, CLI exports, Markdown escaping, help,
and CI gates.
