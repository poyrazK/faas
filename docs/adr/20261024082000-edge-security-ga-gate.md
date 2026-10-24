# ADR-20261024082000: Release gate for edge security features

- **Status:** accepted
- **Date:** 2026-10-24
- **Related:** ADR-831 (WAF and challenge), ADR-20261022105846 (penalty box),
  ADR-20261023113721 (edge feature platform flags), ADR-1700 (platform
  flags)

## Context

WAF warn and block, challenge rules and rule penalties are behind platform
flags that are off for every account (ADR-20261023113721). Nothing says
when a flag may be turned on for everyone or when the docs may drop
"preview". ADR-831 set one target for WAF (2 ms in-path p95), and the
other two features have none. The product catalog, which generates the
customer capability matrix, did not list any of the three, so the release
evidence contract (`make api-hosting-scorecard-check`) did not cover them.

## Decision

1. **The three features are catalog capabilities**, at maturity `preview`:
   `edge-waf-blocking` (Pro and Scale), `edge-browser-challenge` (Hobby and
   above) and `edge-penalty-box` (every plan: penalties work on geo and
   throttle rules, which Free has). Each platform flag names its
   capability, so the capabilities API reports the feature unavailable to
   accounts the flag leaves out.

2. **Each has a scorecard gate** (`edge-waf-blocking-preview`,
   `edge-browser-challenge-preview`, `edge-penalty-box-preview`). The gate
   lists numeric targets and the tests and docs that produce the evidence;
   CI fails if a locator goes stale.

3. **A feature moves from `preview` to `ga`** (catalog maturity, docs label,
   flag default on, then the flag retired) only when every criterion below
   is met. Moving it is the product owner's call, recorded in
   `docs/STATUS.md`. The flag stays as the kill switch until it is retired.

4. **Release stages**, each held for at least 14 days with its targets
   met before the next:
   1. internal accounts;
   2. design partners (at least 5 accounts that asked for the feature);
   3. default on for every entitled account.

   Withdrawing the flag is the rollback at every stage.

### Criteria

Common to all three:

| # | Criterion | Evidence | Status |
|---|---|---|---|
| C1 | Turning the flag off makes the feature inert at the edge and refuses configuring it | `TestEdgeChallengeFlag`, `TestEdgeWAFBlockingFlag`, `TestEdgePenaltyBoxFlag`, `TestEdgeFeatureReleaseGate` | met (#5236) |
| C2 | A rule the edge does not enforce says so in the API, dashboard, CLI and trace | `unreleased_features` (ADR-20261023113721 amendment 1) | built, not merged |
| C3 | Alerts and runbooks cover the feature's failure modes | runbooks under `docs/runbooks/` | open |
| C4 | Each release stage held its targets for 14 days | `docs/STATUS.md` entries | open |

WAF warn and block (`edge-waf-blocking`):

| # | Criterion | Target | Evidence | Status |
|---|---|---|---|---|
| W1 | In-path check latency on the reference compute node (n2-standard-4) at 100-500 req/s | p95 at most 2 ms | `TestEdgeWAFInlineLoad_E2E` with `FAAS_E2E_WAF_LOAD_GATE=1` | met before W2's fix (1.49-2.00 ms, ADR-831 amendment 10); re-measure after it |
| W2 | Checks skipped under load at 100-500 req/s | at most 0.1% | same run; `gateway_waf_inline_skips_total{reason="busy"}` | open: 2.5-8% measured; ADR-831 amendment 11 (not merged) models 0.003-0.013% |
| W3 | Detection on the CRS regression corpus | no regression against the corpus baseline | `TestCRSRegressionCorpus` | met |
| W4 | False positives | no benign corpus request flagged beyond the documented ones; no unexplained in-path detection in 14 days of design-partner traffic | `TestBenignCorpus`; stage-2 review | corpus met; review open |
| W5 | Fails open, with a node kill switch | a skipped or failed check passes the request; `FAAS_EDGE_WAF_INLINE_DISABLED` | `TestCheckInlineFailsOpen` | met |

Browser challenge (`edge-browser-challenge`):

| # | Criterion | Target | Evidence | Status |
|---|---|---|---|---|
| H1 | A browser solves and passes, a script is refused, verified credentials and crawlers pass | all pass | `TestEdgeRulesChallenge_E2E`, `TestApplyEdgeRuleChallenge`, `TestChallengeSolveRoundTrip` | met |
| H2 | The page's solver agrees with the gateway | all pass | `TestChallengeSolverJS` | met |
| H3 | Solve time at the default difficulty on a low-end phone (2020 mid-range Android, current Chrome) | p95 at most 2 s | manual measurement, recorded in `docs/STATUS.md` | open: estimate only (tens to hundreds of ms) |
| H4 | Raised difficulty is capped and falls back | all pass | `TestChallengeLoadRaisesAndLowers` | met |
| H5 | Browser navigations that get the page go on to solve it, on real traffic | at least 90% over 14 days of stage 2 | `gateway_edge_challenge_total` solved / issued | open |

Penalty box (`edge-penalty-box`):

| # | Criterion | Target | Evidence | Status |
|---|---|---|---|---|
| P1 | Boxes after the threshold, refuses on every path with Retry-After, releases when the rule loses its penalty | all pass | `TestPenaltyBoxRefusesBoxedClientAcrossPaths`, `TestPenaltyBoxThresholdWindowAndExpiry` | met |
| P2 | A box holds on every gateway and survives a restart | within 1 s, p95 | ADR-20261022105846 amendment 1 (not merged): `TestPenaltyBoxSharedAcrossGateways` | built, not merged |
| P3 | Memory stays bounded | per-app and per-gateway caps hold | `TestPenaltyBoxPerAppCap` | met |
| P4 | No client boxed in error | no unexplained box reported in 14 days of stage 2 | stage-2 review | open |

Evidence from branches that are not merged yet joins the scorecard gates
when they merge.

## Consequences

- The capability matrix lists the three features as `preview`, and the
  capabilities API reports them unavailable until the flag includes the
  account.
- None of the three can reach GA today. The open criteria are W2 (built,
  needs merge and a prod run), H3 (a device measurement), C2 and P2 (built,
  need merge), C3 (runbooks), and the stage reviews (W4, H5, P4, C4), which
  need the flags turned on for internal accounts and design partners.
- WAF `observe` stays generally available (ADR-831 amendment 9) and is not
  part of this gate.
