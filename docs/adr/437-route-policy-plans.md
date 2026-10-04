# ADR-437: Read-only route policy patch planning

- **Status:** initial design; local composition and version-1 artifacts superseded
  by [ADR-438](438-transactional-route-policy-apply.md). Proposal semantics remain.
- **Date:** 2026-10-01
- **Decision:** Add `gregale routes plan <slug> --requirements PATH` to propose
  throttle and configured execution-budget patches from customer-owned route
  requirements. Compose existing app, edge-rule, and account reads. Reuse the
  requirements evaluator before and after proposed in-memory changes. No new
  control-plane writes, persistence, notifications, or runtime requests.
- **Scope:** Concrete method/path requests on the app's platform hostname.
  Action-only updates require an exact, unconditional host/method/path selector.
  Otherwise create an exact override with an earlier priority and retain the
  broader rule. A priority-0 broader winner, ambiguous selectors, routing
  mutations, invalid evidence, or exhausted quotas remains unresolved.
- **Choices:** Preserve known bursts and key capacities where applicable.
  A missing throttle needs a declared maximum rate and an explicit CLI burst.
  A new budget copies and optionally tightens the existing effective baseline.
  Custom JWT claims, custom budget override headers, and unknown action fields
  require separate review. Authentication findings are retained without patches.
- **Entitlements:** Use the account's current named plan and bundled limits
  for advisory rate/burst/key-capacity and rule-count checks. Disabled rules
  count. Missing plan data blocks new proposals. Any subsequent API write must
  revalidate current configuration, ownership, entitlements, and quotas.
- **Artifact:** Version-1 JSON includes generated typed create/update bodies,
  request-shape impact, displaced IDs, allowlisted before/after summaries,
  unresolved findings/actions, and deterministic requirements/configuration/
  plan fingerprints. Proposed rule IDs are explicitly simulated references.
  Do not export raw selector values or unrelated actions. Save complete local
  files with mode 0600 without replacing an existing or racing destination.
- **Limits:** Reuse the bounded requirements format from ADR-436 and the existing
  API priority ceiling. This introduces no hosting-plan quota.
- **CI:** Optional `--fail-on-unresolved` fails for remaining requirements after
  output/export. Success is a planning result, not an installation receipt.
- **Limitations:** Reads are not an atomic snapshot and fingerprints are not
  signed approval tokens. Exact overrides change configured selection, while a
  new throttle can also change sharing and admission for the former bucket's
  remaining traffic. No live admission, authorization, template-wide coverage,
  custom-domain coverage, or response-time guarantee is claimed.
- **Acceptance:** Pure evaluator/planner tests cover exact updates, broad
  overrides, independent partial plans, customer choices, baseline preservation,
  quotas, redaction, and deterministic fingerprints. CLI tests verify GET-only
  composition, identity checks, local help, artifact integrity and permissions,
  no-overwrite publication, and gate/output ordering.

See [route policy plans](../route-policy-plans.md) for the customer workflow.
