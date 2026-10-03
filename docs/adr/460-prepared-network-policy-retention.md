# ADR-460 · Retain valid prepared networks across eligible policy changes

- **Status:** proposed
- **Date:** 2026-10-03
- **Amends:** ADR-149's eviction on an eligible policy change; spec §6.3 and §7.
- **Decision:** Keep fresh, unused prepared networks for other eligible exact
  policies within the existing global cache capacity. The latest observed
  policy remains the replenishment target. When the ready pool is full and
  has no entry for that target, retire only its oldest spare to make room for
  one target-policy entry. A completed preparation remains useful even if
  another policy became the target while setup was in progress.
- **Why:** ADR-149 currently retires every previous-policy spare on a policy
  change. A local fake-runner reproduction with three slots and alternating
  100/250 Mbit policies replaced all three spares at each completed fill and
  left the previous policy unable to claim one. Different eligible tenant
  plans and egress rates can produce this shape. Saved production observations
  recorded 52–103 ms of network setup, but do not establish that policy churn
  caused those costs or that the prepared cache was enabled on every node.
- **Consequences:**
  - The configured 0–16 global capacity, allocator ownership, expiry/refresh
    ages, one worker and teardown-failure slot retention remain unchanged.
    No separate per-policy allowance or unbounded policy-history map is added.
  - An initially single-policy pool uses all configured slots. After mixed
    traffic, policies share those slots until older spares are consumed or
    reach refresh age. A burst for one policy may therefore have fewer ready
    spares than the global capacity. Capacity one still replaces its spare on
    a policy change; there is no burst-sized reserve promised for every policy.
  - Expired or refresh-aged entries are retired as before. Their replacements
    use the latest policy, so quiet older policies can eventually lose their
    spares. This change avoids immediate whole-pool eviction, not every miss.
  - Claim still requires exact policy equality. The complete validated network
    config is checked before a VMM starts, with the existing permitted DNAT
    retargeting and ordinary-setup fallback. Unsupported policies remain
    ineligible; used tenant namespaces never return to this pool.
  - These are network-only reservations. They contain no VMM, guest memory or
    tenant cgroup and do not change app admission or CPU/RAM ceilings.
  - The change makes no achieved latency or sub-350 ms claim. Network and
    staging currently overlap asynchronous prefetch; whole-wake comparisons
    must account for any exposed page-fault work.
- **Validation:** Pure-Go regressions cover alternating exact-policy claims,
  one-slot behavior, minimal oldest-spare replacement, policy changes during
  preparation and failed teardown at capacity. Existing race tests retain
  concurrent unique ownership, full-config matching, fallback and shutdown
  coverage. Before rollout, run the required network/restore and leak gates
  on dedicated native x86_64 Linux KVM hosts, then compare same-policy and
  mixed-policy wakes through first upstream byte, including a same-policy
  burst immediately after mixed traffic. Include expiry, failure and fallback
  observations and keep total host capacity fixed.
- **Rejected alternatives:** Increasing the cache capacity hides churn and
  spends more host network resources; matching only egress rate relaxes the
  policy contract; preserving used tenant namespaces breaks ADR-149's fresh
  namespace ownership contract. A policy demand-history scheduler adds state
  and weighting rules before a production hit-rate measurement justifies it.
