# ADR-20261023113721: Platform flags for edge rule features

- **Status:** accepted
- **Date:** 2026-10-23
- **Related:** ADR-1700 (platform flags), ADR-831 (WAF; amendments 7–8,
  challenge rules and escalation), ADR-20261022105846 (penalty box),
  ADR-960 (log mode), ADR-1750 (strict budgets, the release-gate precedent)

## Context

Three edge rule features merged recently: WAF `warn` and `block` modes
(ADR-831, preview), `kind=challenge` with difficulty escalation (ADR-831
amendments 7 and 8) and rule penalties (ADR-20261022105846). Each is gated
only by plan limits. "Preview" is a docs label, and the one runtime switch,
`FAAS_EDGE_WAF_INLINE_DISABLED`, turns the in-path WAF check off for every
account on a node. There is no way to launch these features to chosen
accounts first, or to withdraw one from an account without a deploy.

## Decision

1. **Three platform flags** in the ADR-1700 catalog, each off by default
   for every account until launch:
   - `edge_waf_blocking`: warn and block modes of `kind=waf` rules. Observe
     mode is generally available and not gated.
   - `edge_challenge`: `kind=challenge` rules, including escalation.
   - `edge_penalty_box`: rule penalties.

   apid and gatewayd-internal both decide them, by account and plan. Each
   should retire into general availability by 2027-03-31.

2. **apid refuses switching a feature on.** While a flag is off for the
   account, apid answers 422 `edge_feature_unavailable` to:
   - creating a rule that uses the feature, enabled or not: any challenge
     rule, a WAF rule in warn or block mode, a rule with a penalty;
   - an update after which the rule enforces a gated feature it did not
     enforce before. A rule enforces when it is enabled and not in log mode
     (ADR-960), so enabling it, leaving log mode, moving WAF to warn or
     block, or adding a penalty is refused.

   Every other edit, disabling, log mode and deleting always work, so
   withdrawing a flag never strands a rule. Recipes and route-insight fixes
   create through the same path. The other edge-rule writers (environment
   policies, manifest routes, async routes, OpenAPI suggestions) cannot
   produce these features.

3. **gatewayd-internal stops enforcing.** Unlike strict budgets (ADR-1750),
   these flags are kill switches at the edge, decided per request from the
   set the runtime-config watcher keeps current:
   - a challenge rule serves no page, refuses nothing, and leaves
     `ChallengeSolvePath` to the app;
   - a warn or block WAF rule is inspected as observe: no in-path check, no
     403 and no warning header, the off-path sample still taken;
   - a penalty neither boxes nor refuses clients; the rule itself keeps
     refusing.

   Existing rules in production go inert when this ships, until an operator
   releases the flag. Turning a flag on makes such rules enforce at once,
   without an edge-rule cache reset.

4. **No flag set, no gate.** A gateway handler built without flags (unit
   tests, tools) enforces every feature. gatewayd-internal always wires its
   set; before the first reconcile it holds catalog defaults, which are off.

## Consequences

- Operators launch each feature with `flags enable` plus `flags allow` per
  account, then `default` for everyone, and can withdraw it from one account
  or the platform without a deploy.
- Customers see 422 `edge_feature_unavailable` naming the feature; the
  OpenAPI spec already lists 422 for creating and updating an edge rule.
- Dashboard recipe cards and the challenge alternative in recipe
  suggestions still show for accounts outside a flag; applying one is
  refused with the same 422. The edge-rule trace simulator
  (`pkg/edgeruletrace`) reports what a rule set does with every feature
  released.
- Attack mode and AI crawler controls, not yet merged, get their own flags
  on their branches under this decision.
