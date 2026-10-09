# ADR-833: Reusable edge-rule lists

- **Status:** accepted
- **Date:** 2026-10-09
- **Related:** ADR-832 (match expressions), ADR-831 (versions), ADR-830
  (log mode), ADR-091 (edge rules)

## Context

ADR-832 conditions put values inline: an office allowlist, a set of
blocked countries or a partner host list is copied into every rule that
needs it, and an edit means updating each copy. Cloudflare solves this with
account-level lists referenced from rule expressions.

## Decision

1. **Lists** are account-scoped, named (`[a-z0-9_-]{1,64}`, unique per
   account) and typed: `ip` (addresses and CIDRs), `country` (ISO 3166-1
   alpha-2), `host` (exact hosts and `*.suffix` patterns) or `string` (exact
   values). Items are validated and canonicalized on write. Counts are plan
   limits in `pkg/api/limits.go`: `EdgeRuleListsPerAccount` /
   `EdgeRuleListMaxItems` are Free 0/0 · Hobby 5/100 · Pro 20/1,000 ·
   Scale 100/10,000.

2. **Reference:** a condition leaf uses `{"op": "in_list", "list": "<name>"}`
   on a field the list type fits: `client_ip` for `ip`, `country` for
   `country`, `host` for `host`, and `header:`/`cookie:`/`query:`/`path`/
   `method` for `string`. `not` composes as usual. A missing value never
   matches (ADR-832 §5).

3. **Validation:** apid rejects a rule that references an unknown list or a
   list whose type does not fit the field. A list referenced by any rule
   cannot be deleted (409).

4. **Gateway:** lists are resolved when a host's rules are compiled, never
   per request. A list that is missing at compile time (deleted by a direct
   database edit) makes the referencing rule never match, as for any
   condition that fails to compile.

5. **Propagation:** a list write touches `updated_at` on every rule that
   references it, in the same transaction. The existing `edge_rules` change
   log then drives scoped cache invalidation on every gateway through the
   repair poller (2 s), while the ADR-831 snapshot, which excludes
   `updated_at`, records no new rule-set version. List edits are not fenced
   (ADR-091 convergence): a list may touch many apps at once, and an
   allowlist/blocklist edit converging within seconds is the accepted
   trade. Rule writes that add a list reference keep their fenced path.

6. **Trace** loads the account's lists so dry runs evaluate `in_list`.

## Consequences

- One edit updates every rule that uses a list; conditions stay short.
- Rule-set versions do not capture list contents: rolling back a rule set
  restores the reference, not the list's past items.
- Lookups are hash-based for addresses, countries, strings and exact hosts
  (`*.suffix` walks the host's labels); CIDR items are scanned linearly, at
  most the plan's item limit, and only for rules whose other selectors
  already matched.
