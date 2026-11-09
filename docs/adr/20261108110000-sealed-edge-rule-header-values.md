# ADR-20261108110000: Edge-rule header values can be sealed

Date: 2026-11-08
Status: Accepted
Amends: [ADR-091](091-edge-rules.md) (edge rules),
[ADR 20261022111812](20261022111812-seal-domains-per-reader.md) (seal domains: the edge
domain gains a column)

## Context

A `kind=headers` rule's request header ops are how customers attach an
upstream credential at the edge: `Authorization: Bearer …` to an origin
behind a `route` rule, an `X-Api-Key` for a backend. The value was stored as
written in `edge_rules.action`, copied into every rule-set version, returned
by every rule, version, export and dashboard read, and therefore visible to
every organization member with `org.read_app`, to read-only agent keys and to
anyone holding a database backup. The 2026-11-07 edge-rules review recorded
this as E11.

The platform already seals the credentials gatewayd-internal needs to read at
request time (app basic-auth credentials and log drain headers) to the edge
seal domain; gatewayd-internal is that domain's only reader.

## Decision

1. **A request header op can be secret.** `{"name": "Authorization",
   "action": "set", "value": "Bearer …", "secret": true}`. apid seals the
   value to the edge domain's recipients (the same write mode as basic-auth
   credentials, namespace `EDGE_RULE_HEADER_VALUE`, at most 4096 bytes) and
   stores it in a new table, `edge_rule_header_secrets` (one immutable row
   per sealed value, owned by the app). The op keeps only `secret: true` and
   the row's `secret_id`; its `value` is never stored or returned.
2. **Credential-named request headers are secret without asking.**
   `Authorization`, `Proxy-Authorization`, `Cookie`, and any name containing
   `token`, `secret`, `password`, `api-key`, `apikey`, `signature` or
   `credential` are sealed on every create and update. A template value
   (ADR-968) is not sealed: it reads request values, and `secret` with
   `template` is refused.
3. **Only request headers.** Response headers go to the client, so `secret`
   on a response header op is refused, as is `secret` on a `remove` op.
4. **Keeping a secret.** An update or rollback that sends an op with its
   `secret_id` and no `value` keeps that sealed value; the id must belong to
   the rule's app. Rule-set versions store the `secret_id`, so rollback
   restores the sealed value without apid ever reading it back. A rollback
   to a version saved before this ADR restores its values as stored; they
   stay masked (point 5) until the rule is next written.
5. **Values stored before this ADR are masked.** A credential-named literal
   saved earlier is returned with `value` empty and `redacted: true` until the
   rule is next written. Sending that op back unchanged (`redacted: true`, no
   `value`) seals the stored value; it is never echoed.
6. **The gateway opens values when it compiles a host's rules.**
   gatewayd-internal reads the referenced rows (only rows of the rule's own
   app), opens them with its edge-domain identities, and caches each opened
   value by id; the rows never change. A value it cannot read fails that
   rule's compilation, so the rule is not applied and the failure is logged,
   rather than forwarding the request without its credential silently
   succeeding as something else.
7. **The edge domain re-seal covers the table.** `edge_rule_header_secrets.ciphertext`
   is registered to the edge domain; the edge migration lists and re-seals it
   like a log drain header.

## Consequences

- Header credentials no longer appear in API responses, rule-set versions,
  account exports, the dashboard or MCP tool output, and are encrypted at
  rest.
- A client that read a rule and writes it back keeps its credentials: secret
  ops round-trip by `secret_id`, legacy ones by `redacted`.
- Rows are deleted with their app. A secret no rule or version references
  any more is kept until then; values are small and bounded by the rule
  count.
- Environment edge policies (environment-owned `headers` rules) do not accept
  `secret` yet; their values stay as written.
- Trace output already redacted credential-like values; secret ops have no
  value to show.
