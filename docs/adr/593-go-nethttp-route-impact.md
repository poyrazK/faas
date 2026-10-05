# ADR-593: Static Go net/http route impact

- **Status:** Accepted
- **Date:** 2026-10-04
- **Relates to:** ADR-439 (static FastAPI route impact), ADR-440 (function-level source impact), ADR-441 (preview source joins)

## Context

The local route impact command connects source changes to FastAPI endpoints,
but Go services currently receive only an unmapped-file warning. Developers
using Go need the same route-level review before a release, with no customer
source execution or new server-side state.

## Decision

Extend `gregale routes impact` with a standard-library Go AST adapter for
`net/http` `ServeMux` registrations. Preserve FastAPI report version 2 and emit
version 3 for Go reports because Go snapshots and routes include new provenance
fields. Preview reports accept both versions.

Support literal method/path and path-only registrations on the default mux and
on mux values created by `http.NewServeMux`, including registration helpers
whose mux parameter is typed as `*http.ServeMux`. Resolve local handler functions,
anonymous handlers, `http.HandlerFunc` wrappers, and direct calls through
same-module imports. Expand Go's `GET` pattern to `GET` and `HEAD`; expand
methodless path patterns to the supported standard methods. Fingerprint each
registration independently so one changed route does not mark sibling
registrations in the same function as changed.

Dynamic patterns, unresolved handlers, syntax errors, unavailable same-module
packages, missing module identity for cross-package resolution, and changes to
non-Go files remain explicit incomplete evidence. The analyzer does not
type-check, build, import, execute, or contact dependencies from customer
source. It does not claim that a discovered mux is attached to a running
server. Existing Git, source-size, route, graph, evidence, output, and deadline
bounds apply; Go file and function-reference counts use bounded limits.

## Consequences

Go teams can use the local source impact report and join it to the existing
preview route review without another service or entitlement. Dynamic routing
and routers outside `net/http` continue to require supplied tests and captured
deployment evidence; they cannot be inferred as safe from an empty static
result.
