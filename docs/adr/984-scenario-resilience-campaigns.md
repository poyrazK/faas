# ADR-984 · Scenario resilience campaigns and recovery regression gates

- **Status:** accepted
- **Date:** 2026-10-12
- **Amends:** ADR-423 (scenario-scoped chaos), ADR-981 (private TCP faults)
- **Decision:** Extend registered scenario tests with leased private-service DNS
  faults and connection-attempt evidence, plus bounded staged recovery assertions,
  repeated fault campaigns, deterministic seed selection, regression gates,
  isolated replay, and failure minimization. Execute every campaign seed as its
  own scenario workload and evaluate aggregate policies at the campaign root.
- **Why:** A passing load SLO alone does not prove that a dependency fault was
  exercised or that recovery stayed within a previous application version's
  behavior. Saved execution history must support reproducible CI decisions.
- **Scope:** DNS faults act only on verified callers and targets in the same
  registered scenario run, under the existing fault lease and cleanup rules.
  TCP attempt counts include failed connections and distinguish attempts from
  matched faults. These mechanisms do not modify host networking or production
  workloads. Existing service authorization and normal tenant limits apply.
- **Evidence:** Coverage counts come from uniquely identified, complete fault
  windows. Recovery gates use confirmed recovery and each target's own phase
  metrics. Saved baselines must match workload, profile, seeds, repetitions and
  fault choices; report fingerprints and full policies are pinned in aggregate
  replay. Missing evidence fails selected gates. Existing SLO, cleanup and
  cancellation failures remain independent.
- **Bounds:** CLI campaign, replay and minimization run/time budgets limit test
  execution; they do not create or change account plan quotas. Trend reports read
  bounded saved reports offline and never provision workloads. Descriptive
  worst-observed metrics do not claim statistical confidence.
- **Verification:** Portable socket, DNS, authorization, store conformance,
  CLI/report and replay tests cover the feature. Real VM forwarding acceptance
  remains a separate native KVM gate; portable tests do not substitute for it.

See [scenario test configuration](../scenario-tests.md) for all limits, commands,
report contracts, and CI gate semantics.
