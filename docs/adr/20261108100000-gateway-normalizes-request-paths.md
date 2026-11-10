# ADR-20261108100000: The gateway forwards normalized request paths

Date: 2026-11-08
Status: Accepted
Amends: [ADR-091](091-edge-rules.md) (edge rules),
[ADR-20261107190000](20261107190000-edge-rule-request-shape-bypasses.md)
(edge rules read requests the way apps do)

## Context

Only one rule of each gate kind (`jwt`, `ip`, `geo`, `limit`, `throttle`,
`validate`, `maintenance`, `waf`, `challenge`) applies to a request: the
highest-priority rule whose selectors match. Since ADR-091 (amendments
2026-10-28 and 2026-11-02) and ADR-20261107190000, a gate's `match_path` and
`match` condition hold when they hold for the raw path or any normalized
form, so a strict rule on `/upload` also covers `/public/../upload`.

That widening only adds protection when the selected rule is the stricter
one. An exception, that is a higher-priority rule of the same kind that
relaxes the gate (an `ip` rule open to everyone on `/public/**`, a `limit`
rule with a large body cap on `/public/**`, a `waf` rule in observe mode),
matched `/public/../upload` by its raw form and was selected instead. The
app, like most frameworks, served `/upload`, so the request reached it under
the exception and skipped the strict rule. The 2026-11-07 edge-rules review
recorded this as a residual risk.

## Decision

1. **gatewayd-internal normalizes the request path before anything reads
   it.** At the top of the request handler, before edge rules, routing,
   declared-route policy, the response cache and forwarding, `.` and `..`
   segments are resolved (also when percent-encoded) and repeated slashes
   collapsed, keeping a trailing slash (`api.NormalizeEdgeRequestPath`). The
   app receives the normalized path. This is what nginx (`merge_slashes`)
   and Envoy (`normalize_path`) do in front of an app.
2. **Paths already in that form are untouched**, escapes included, so
   `%2F` inside a segment still reaches the app encoded. A rewritten path is
   forwarded re-escaped from its decoded form: an encoded slash inside such
   a request becomes the `/` that rules already read it as.
3. **Backslashes, `;` segment parameters and case are not rewritten.** Apps
   disagree on them (a `\` or `;` is a literal character to most Node and
   Python apps), so the gateway keeps them and gates keep matching those
   forms protectively. An exception selected through one of them covers a
   path that a framework reading it that way serves as the same route.
4. **The WAF still inspects the request target as received**, so traversal
   signatures see `../`. Logs and analytics record the normalized path.
5. **The trace simulator normalizes its input path the same way**, so a
   trace reports the rule the gateway selects.

## Consequences

- An exception rule can no longer be selected for a dot-segment or
  repeated-slash path that the app serves as a path the exception does not
  cover.
- Every kind, not only gates, now sees the normalized path: a `redirect`,
  `rewrite`, `route`, `headers` or `cache` rule on `/old/**` also matches
  `//old/x` and `/x/../old/x`.
- An app that gave meaning to `//` or to unresolved dot segments receives
  the normalized path instead. Browsers and HTTP clients normalize dot
  segments before sending, so only hand-built requests change.
- No API, schema or wire change; a gatewayd-internal from before this ADR
  keeps forwarding the raw path, so the change takes effect node by node as
  gateways update.
