# ADR-582: Static Go Chi route impact

- **Status:** Accepted
- **Date:** 2026-10-05
- **Relates to:** ADR-581 (static Go `net/http` route impact), ADR-440 (function-level source impact)

## Context

Go applications using Chi currently receive no route mapping from the Go source
impact analyzer. Chi supports nested router groups, so endpoint paths depend on
literal prefixes assembled across `Route` callbacks.

## Decision

Extend the existing Go route analyzer to recognize Chi v4 and v5 router values,
typed `chi.Router` parameters, supported HTTP method registrations, and inline
`Route` and `Group` callbacks with literal prefixes. Concatenate nested literal
prefixes with endpoint patterns without executing customer code.

Dynamic prefixes and patterns remain explicit incomplete evidence. `Mount`
expands routes when its prefix is literal and its target is a local router
variable with statically indexed registrations. Nested groups and mounts are
expanded within the existing graph and route bounds; dynamic paths, unresolved
targets, and mount cycles remain incomplete. Root routes inside `Route` groups
or under mounts without a trailing slash are reported for both Chi's exact
prefix and its slash form. Middleware functions that resolve
to local Go symbols are attached to routes according to `Use`, `With`, and
nested group scope, preserving middleware order so attachment, removal, and
ordering changes identify the affected endpoints. Changes to shared middleware
functions also identify the endpoints that use them. Middleware expressions
that do not resolve to local functions remain explicit incomplete evidence.
The analyzer does not type-check Chi, resolve external dependencies, or claim
that a discovered router is attached to a running server. Existing source,
route, evidence, and deadline bounds apply.

## Consequences

Chi users can receive route-level source impact for common inline router
composition, local mounts, and local middleware without a dependency install
or code execution. Unresolved mounts, middleware, and dynamic route construction
remain visible as uncertainty rather than being presented as complete route
evidence.
