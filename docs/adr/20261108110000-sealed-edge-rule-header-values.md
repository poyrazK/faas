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
  `secret` yet; their values stay as written. (Superseded by the second
  amendment of 2026-11-09.)
- Trace output already redacted credential-like values; secret ops have no
  value to show.

## Amendment 2026-11-09: sealing follows the fleet, and stored values are sealed

**Problem.** Units release independently and apid upgrades before the
gateways (docs/release-compatibility.md). A gatewayd-internal older than this
ADR ignores `secret_id` and applies the op's empty value, so a request reached
the origin with `Authorization:` set to nothing. That happened for every rule
apid sealed while an older gateway still served it, and for every sealed rule
after gateways were rolled back. Values stored before this ADR also stayed
as written in the database and in up to 100 rule-set versions per app,
masked in responses but readable at rest.

**Decision.**

1. **Gateways report that they open sealed values.** gatewayd-internal adds
   `edge_header_secrets` to its traffic runtime report (ADR-570), true only
   when it holds the edge identities. The column stores the reporting boot,
   `edge_header_secrets_boot`, and counts only while it equals the row's
   current `boot_id`: an older gateway's registration resets the columns it
   knows and replaces the boot, so a rolled-back gateway stops counting.
2. **apid seals only while every gateway reports it.** Every active compute
   node with a gateway target needs a fresh report (within
   `TrafficRuntimeObservationFreshness`), including a node whose listener
   heartbeat is stale and which edge-rule convergence skips: it may still
   serve. A deployment with no registered serving gateway (single-box,
   development) counts as ready; its daemons upgrade together. Until then a
   value is stored as written, as before this ADR, and still left out of
   responses (point 5).
3. **apid seals stored values in the background.** Every
   `EdgeRuleHeaderSecretBackfillInterval` (15 min, first pass after 2 min),
   once the fleet is ready, apid seals each credential value a live rule
   still stores as written. The change goes through edge-rule convergence and
   only if the rule did not change since it was read; it adds a rule-set
   version and the audit event `edge_rule.header_values_sealed`. It then
   rewrites stored rule-set versions in place: only the affected rules'
   actions change, the digest is recomputed as the version trigger computes
   it, and a version changed since it was read is left for the next pass. One
   row is stored per app and value in a pass, however many versions repeat
   it. Versions of deleted apps are skipped.

**Consequences.**

- During a rollout, credentials written before every gateway is upgraded are
  stored as written for up to one backfill interval after the last gateway
  reports.
- Rolling gateways back to a release older than this ADR still breaks rules
  sealed earlier; that release cannot read `secret_id`. Roll apid and the
  gateways back together, or not past this ADR's release.
- A version restored by a rollback holds sealed values once the backfill has
  run; before that it restores the value as written and the next pass seals
  it.

## Amendment 2026-11-09 (2): environment edge policies

**Problem.** Environment-owned `headers` rules refused `secret`, kept
credential values as written, and the environment policy responses (GET, the
environment state and its diffs, and trace input) returned them.

**Decision.**

1. **Policy values are sealed as the app's.** A policy belongs to one app and
   environment, and gatewayd-internal turns it into that app's rules before it
   opens sealed values, so a policy value is an `edge_rule_header_secrets` row
   of the app. The write path, the fleet gate and the `secret_id` and
   `redacted` round-trips are the application rule's. A redacted op takes the
   stored value of the same header from the rule at the same position first,
   then from the other stored rules.
2. **Policy responses leave credential values out**, as rule responses do.
3. **The background pass seals stored policies** written through the API:
   through edge-rule convergence on the environment URL, replacing the policy
   only if its `updated_at` is unchanged. It skips an environment with an
   active Git source for the app. Those values come from the repository: the
   GitOps observer compares stored policies field by field with the
   definition, so a sealed value would read as drift and be rewritten on every
   apply. A manual write the store refuses as Git-managed is left as well.

**Consequences.** Credentials in Git-managed environment policies stay as
written at rest, as they are in the repository; responses still leave them
out. Referencing a secret from the environment definition instead of a
literal is a separate change.
