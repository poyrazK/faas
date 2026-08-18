# Sub-issue #03 — Schema validation modes (observe / warn / block)

Parent: [README.md](README.md)

## Problem

Today the `kind=validate` edge rule is **block-only**: any body that fails
the JSON Schema returns 422. Code:

- `pkg/gateway/handler.go:2281-2289` buffers the body and matches the rule
  by host/path/method.
- `pkg/gateway/handler.go:2428-2463` returns 422 on schema mismatch.
- `pkg/edgevalidate/validator.go:14-37` exposes streaming + unknown-field
  controls but no mode.

Operators asking "which endpoints receive invalid payloads?" can't answer
that without enabling blocking — which breaks their customers. They need
an **observe** mode (count mismatches without rejecting) and a **warn**
mode (count + add `X-Validation-Warning` response header, still proxy).

## Proposal

Add a `mode` field to the validate rule projection:

```sql
ALTER TABLE edge_rules
  ADD COLUMN validate_mode TEXT NOT NULL DEFAULT 'block'
    CHECK (validate_mode IN ('observe','warn','block'));
```

Migration renames the existing default; this is forward-compatible for
existing rules because `block` is the strictest mode.

### Semantics

| Mode | 422 returned? | Counter incremented? | Response header? |
|---|---|---|---|
| observe | no | yes | no |
| warn | no | yes | `X-Validation-Warning: <rule_id>` |
| block | yes (existing behavior) | yes | n/a (already 422) |

Counter = a new Prometheus metric
`gateway_validate_failures_total{app_id, rule_id, mode, reason}` where
`reason` is a bounded enum (`required_missing`, `type_mismatch`,
`additional_properties_not_allowed`, `enum_violation`,
`format_violation`, `other`) — mirrors the reason taxonomy already used
in `pkg/edgevalidate/validator.go`.

### Code touch points

- `pkg/gateway/handler.go:2281-2289` — read `mode` from the matched rule.
- `pkg/gateway/handler.go:2428-2463` — branch on mode:
  - observe / warn → emit metric, optionally header, then continue proxy.
  - block → existing 422 path.
- `pkg/edgevalidate/validator.go:14-37` — add `Mode` to the `Validator`
  config; gate the `WriteHeader` call in handler.
- `pkg/api/edge_rules.go` — DTO gets `validate_mode` field; default
  `block` to preserve behavior.
- `migrations/00XXX_edge_rules_validate_mode.sql` — new migration.

### Limits

None new — the metric cardinality is bounded by `(app × rule × mode × reason)`
which is small. The 50-route cap from ADR-093 does not apply because
validate rules are not path cardinality.

## Acceptance

1. A rule with `validate_mode='observe'` lets invalid bodies through and
   shows up in `gateway_validate_failures_total`.
2. `validate_mode='warn'` adds the response header on every invalid
   response and still proxies.
3. `validate_mode='block'` is unchanged from today's behavior (default).
4. Property test: 1k fuzzed payloads, all three modes, asserts the
   counter increments monotonically and the response status is correct
   per mode.
5. Migration is numbered in the next available slot; cross-PR fence check
   runs before commit.

## Dependencies

None. Foundation sub-issue.

## Audit provenance

- `pkg/gateway/handler.go:2281-2289` — buffer + match.
- `pkg/gateway/handler.go:2428-2463` — block-only 422.
- `pkg/edgevalidate/validator.go:14-37` — no mode enum.
- `migrations/00214_edge_rules_kind_validate.sql` — original kind=validate.
