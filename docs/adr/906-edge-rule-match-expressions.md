# ADR-906: Edge-rule match expressions

- **Status:** accepted
- **Date:** 2026-10-09
- **Related:** ADR-091 (edge rules), ADR-905 (rule-set versions), ADR-104
  (throttle dimensions)

## Context

An edge rule selects requests with four fixed selectors: `match_host`,
`match_path`, `match_methods` and exact-value `match_headers` (all ANDed).
Everything else is either impossible or bolted onto one kind's action:
client IP and country exist only inside `kind=ip` / `kind=geo`, there is no
cookie or query selector, no negation, no OR, and no pattern match on a
header value. Each new need so far became a new kind, which costs a
migration (the closed `kind` CHECK), a pointer field on the action struct,
and switches in apid, the CLI, gateway compile and the trace simulator.
Customers cannot express "redirect beta testers (cookie `beta=1`) to the
canary app", "require a JWT except from the office network", or "add a
header for EU visitors" without a new kind.

## Decision

1. Every rule gains an optional **`match`** condition, stored in a nullable
   `edge_rules.match_expr jsonb` column. A rule applies only when its existing
   selectors match **and** `match` evaluates true. Rules without `match`
   behave exactly as before.

2. The condition is **structured JSON, not a text language**, so it needs no
   parser and validation errors point at a field:

   ```json
   {"all": [
     {"field": "cookie:beta", "op": "eq", "value": "1"},
     {"not": {"field": "client_ip", "op": "cidr", "values": ["10.0.0.0/8"]}}
   ]}
   ```

   A node is exactly one of `all` (every child), `any` (at least one child),
   `not` (negation), or a leaf `{field, op, value|values}`.

   - Fields: `method`, `path`, `host`, `client_ip`, `country`,
     `header:<name>`, `cookie:<name>`, `query:<name>`.
   - Ops: `eq`, `ne`, `in`, `not_in`, `prefix`, `suffix`, `contains`,
     `exists`, `missing`, `regex` (RE2), `cidr` (client_ip only).
   - String comparisons are exact, except `method`, `host`, `country` and
     header names, which are case-insensitive as on the wire.

3. **Bounds:** depth ≤ 4, at most 32 nodes, at most 64 values per leaf,
   values ≤ 256 bytes, regex ≤ 256 bytes. Regexes and CIDRs are compiled
   once, when the gateway loads a host's rules, never per request; RE2
   guarantees linear-time matching.

4. **One evaluator.** Validation, compilation and evaluation live in
   `pkg/api`. Evaluation is a pure function over a request snapshot
   (method, path, host, headers, cookies, query, client IP, country). The
   gateway and the trace simulator both call it, so they cannot disagree,
   which is the same reason ADR-905's trace work moved host and path
   matching to `pkg/api`.

5. **Unavailable values.** `client_ip` is the single trusted forwarded hop;
   `country` comes from the gateway's GeoIP database. When a value is
   unavailable (forged or missing forwarded header, no GeoIP database, no
   record) it is treated as absent: `exists` is false, `missing` is true,
   and every other op is false. A condition therefore cannot be satisfied
   by a value the gateway does not trust. Hard network allow/deny policy
   stays with `kind=ip` / `kind=geo`, which fail closed.

6. The path the condition sees is the same path the rule's `match_path`
   sees (after any rewrite that runs before the rule's phase).

7. Versioning: `match_expr` is part of the ADR-905 rule-set snapshot and is
   restored by rollback.

## Consequences

- New selection needs (cookie, query, IP/country on any kind, negation,
  OR, patterns) no longer require new kinds or migrations.
- Kinds stay closed and keep their actions; this ADR does not replace the
  four fixed selectors, which remain the cheap, indexable pre-filter.
- A rule's cost on a matching request grows by at most one bounded
  expression evaluation; non-matching hosts and paths are filtered first.
- The CLI takes `--match '<json>' | @file` and `--clear-match`; the API takes
  `match` on create and update and `clear_match` on update.
