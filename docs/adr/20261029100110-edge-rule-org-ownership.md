# ADR-20261029100110: Edge rules are owned by the app's organization, not its creator account

- **Status:** proposed
- **Date:** 2026-10-29
- **Owner:** gateway (edge rules, traffic policy) and apid maintainers
- **Amends:** ADR-091 (edge rules), ADR-570 (pinned traffic-policy snapshots), ADR-1191 (app-wide rules)
- **Context (Hunt 24 H24-1):**
  - An `edge_rules` row stores `account_id` = the app's **creator** account. A
    company app keeps its creator's `account_id` forever (orgs own apps, but the
    row is never re-stamped on a membership change — the same pattern behind the
    departed-creator class the #5700 guard covers on apid's write path).
  - Every "which rules apply to this host" decision keys off that account:
    - **serving** reads — `ReadPublicHostEdgeRules` (the pinned public-host
      projection, the production path via `pinPublicRouteGraph`),
      `OwnedEdgeRulesForHost` and `OwnerEdgeRules` (the cached-owner path);
    - **write validation / analysis** — `MeasureTrafficPolicyProjection`,
      `readTrafficHostAnalysis`, `beginRuleTrafficPolicyMutation`, and the
      **per-account** traffic-policy mutation locks (`LockTrafficPolicyAccount`,
      `tryAcquireTrafficPolicySession`).
  - `match_host` is free-form (`*`, a wildcard, any hostname), so account keying
    lets a rule written on one of a creator's apps apply to the hosts of **every
    other app that creator made** — in another org, in their personal account,
    and after they have left the org. The apid create path already refuses a
    departed creator writing *on the company app* (#5700); the open vector is
    that creator writing a rule on an app they still own, pointed at the former
    app's host, which the gateway applies because the account matches.
  - The tenant boundary everywhere else is `SameAppResourceOwner`: the app's
    **org** when it has one (its personal org otherwise), not the creator
    account. The service-call path was aligned to this in Hunt 23 (H23-1).
- **Decision:** Edge rules are owned by the app's **resource owner** (its org,
  personal org for a personal app), and every projection and lock keys off a
  single owner identity.
  - Add `edge_rules.owner_id` (the app's resource owner), stamped on create and
    carried forward; backfill from `COALESCE(apps.org_id, apps.account_id)` and
    keep it in step when an app's org changes. This is the load-bearing choice:
    one column that **serving, write-validation and locking all key off**, so the
    serving read and the write-validation projection stay identical (the
    invariant the `traffic_*_analysis` Pg tests enforce) and the mutation lock
    moves from per-account to per-owner coherently.
  - Re-key, in one expand/contract migration sequence (data → control plane →
    edge/node, per release-compatibility), the reads and locks listed in Context
    from `account_id` to `owner_id`.
  - A soft-deleted app's rules stop applying to its owner's other hosts
    (already true for the owner-set reads, ADR-091 amendment 2026-10-28); carry
    that into the owner_id reads.
- **Rejected alternative — scope the reads by `COALESCE(apps.org_id,
  account_id)` with an apps join, no new column (the Hunt 24 first attempt,
  PR #5823, closed):** it fixes the three serving projections (verified:
  production serving honours the org boundary, and the public-host projection
  carries `org_id`), but it does **not** compose with the write-validation and
  analysis path, which stays account-keyed. That breaks the serving-vs-validation
  consistency invariant (caught by the `traffic_global/host/environment_analysis`
  Pg tests) and leaves the per-account mutation locks keyed differently from the
  reads. Re-keying only half the subsystem is worse than not starting.
- **Consequences:**
  - A migration and a coordinated re-key of a recently-GA subsystem; needs its
    own PR series, not a bug-fix PR.
  - Per-owner (not per-account) traffic-policy mutation locking: a change to the
    concurrency-fencing granularity that must be reviewed on its own.
  - Two members of one org can then see each other's rules on the org's hosts —
    a correctness gain the account key suppressed.
- **Interim posture:** the exploit surface is narrow — it requires being the
  **former creator** of the victim app and writing a rule on an app they still
  own, pointed at the victim host. apid's create-path org check (#5700) blocks
  the direct vector (writing on the company app). Tracked as a known gap until
  this ADR ships.
- **Evidence from the closed attempt (#5823, branch
  `fix/edge-rule-org-scope`, kept for reference):** `TestPgEdgeRulesScopeByResourceOwner`
  (serving reads, incl. `PublicHostAppEdgeRules`) and the public-host projection
  carrying `org_id` both pass; the `traffic_*_analysis` Pg tests fail because the
  write-validation path was left account-keyed — the signal that motivated the
  single-`owner_id` decision here.
