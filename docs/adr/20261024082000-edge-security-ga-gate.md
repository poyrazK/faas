# ADR-20261024082000: Release gate for edge security features

- **Status:** accepted
- **Date:** 2026-10-24
- **Related:** ADR-831 (WAF and challenge; amendment 11, GA; amendment 12,
  check slots), ADR-20261022105846 (penalty box), ADR-20261021220456 (attack
  mode), ADR-20261022083601 (AI crawler controls), ADR-20261023113721 (edge
  feature platform flags), ADR-1700 (platform flags)

## Context

Five edge features are behind platform flags that are off for every account
(ADR-20261023113721): WAF warn and block, challenge rules, rule penalties,
attack mode and AI crawler conditions. The product owner declared the first
two generally available on the production measurement of ADR-831 amendment
10, with known limits (ADR-831 amendment 11); they go live by turning their
flags on. Nothing said when the other three may, and the product catalog,
which generates the customer capability matrix, listed none of the five, so
the release evidence contract (`make api-hosting-scorecard-check`) did not
cover them.

## Decision

1. **The five features are catalog capabilities.** `edge-waf-blocking`
   (Pro and Scale) and `edge-browser-challenge` (Hobby and above) at `ga`;
   `edge-penalty-box`, `edge-attack-mode` and `edge-ai-crawler-controls`
   (every plan) at `preview`. Each platform flag names its capability, so
   the capabilities API reports the feature unavailable to accounts the flag
   leaves out.

2. **Each has a scorecard gate** listing numeric targets and the tests and
   docs that produce the evidence; CI fails if a locator goes stale.

3. **A preview feature moves to `ga`** (catalog maturity, docs label, flag
   default on, then the flag retired) when every criterion below is met.
   Moving it is the product owner's call, recorded in `docs/STATUS.md`. The
   flag stays the kill switch until it retires.

4. **Release stages** for each flag, each held for at least 14 days with its
   targets met before the next: internal accounts; design partners (at
   least 5 accounts that asked for the feature); default on for every
   entitled account. Withdrawing the flag is the rollback at every stage.
   The GA features skip to the last stage once the release carrying ADR-831
   amendment 10's engine is deployed (amendment 11).

### Criteria

Common to all five:

| # | Criterion | Evidence | Status |
|---|---|---|---|
| C1 | Turning the flag off makes the feature inert at the edge and refuses configuring it | the `*Flag` tests per feature, `TestEdgeFeatureReleaseGate` | met |
| C2 | A rule the edge does not enforce says so in the API, dashboard, CLI and trace | `unreleased_features` (ADR-20261023113721 amendment 1) | met |
| C3 | Alerts and runbooks cover the feature's failure modes | the `faas_edge_security` alerts and their runbooks under `docs/runbooks/` (amendment 1) | met |
| C4 | Each release stage held its targets for 14 days | `docs/STATUS.md` entries | open |

WAF warn and block (GA; these retire amendment 11's known limits):

| # | Criterion | Target | Evidence | Status |
|---|---|---|---|---|
| W1 | In-path check latency on the reference compute node (n2-standard-4), 100-500 req/s | p95 at most 2 ms | `TestEdgeWAFInlineLoad_E2E` with `FAAS_E2E_WAF_LOAD_GATE=1` | met: 1.48-1.58 ms with amendment 12's slots (ADR-831 amendment 14); 1.49-2.00 ms before (amendment 10) |
| W2 | Checks skipped under load, 100-500 req/s | at most 0.1% | same run; `gateway_waf_inline_skips_total{reason="busy"}`; `TestInlineSlotModel` | met: under 0.05% at every rate in three passes on `gregale-compute-2` (ADR-831 amendment 14) |
| W3 | Detection on the CRS regression corpus | no regression against the baseline | `TestCRSRegressionCorpus` | met |
| W4 | False positives | none beyond the documented ones on the benign corpus; none unexplained in 14 days of production traffic | `TestBenignCorpus`; detection events (amendment 5); the WAF review (ADR-831 amendment 13) | corpus met; review tooling built; traffic review open |
| W5 | Fails open, with a node kill switch | a skipped or failed check passes the request | `TestCheckInlineFailsOpen` | met |

Browser challenge (GA):

| # | Criterion | Target | Evidence | Status |
|---|---|---|---|---|
| H1 | A browser solves and passes, a script is refused, verified credentials and crawlers pass | all pass | `TestEdgeRulesChallenge_E2E`, `TestApplyEdgeRuleChallenge`, `TestChallengeSolveRoundTrip` | met |
| H2 | The page's solver agrees with the gateway | all pass | `TestChallengeSolverJS` | met |
| H3 | Solve time at the default difficulty on a low-end phone (2020 mid-range Android, current Chrome) | p95 at most 2 s | manual measurement, recorded in `docs/STATUS.md` | open: estimated at p95 0.88-1.12 s from throttled Chrome after ADR-831 amendment 15 (3.8-4.9 s before); the device measurement is pending |
| H4 | Raised difficulty is capped and falls back | all pass | `TestChallengeLoadRaisesAndLowers` | met |
| H5 | Browser navigations that get the page go on to solve it, on real traffic | at least 90% over 14 days | `gateway_edge_challenge_total` solved / issued, with bots removed using `challenged` events (`docs/runbooks/edge-challenge-solve-rate-review.md`, amendment 4) | open |

Penalty box (preview):

| # | Criterion | Target | Evidence | Status |
|---|---|---|---|---|
| P1 | Boxes after the threshold, refuses on every path with Retry-After, releases when the rule loses its penalty | all pass | `TestPenaltyBoxRefusesBoxedClientAcrossPaths`, `TestPenaltyBoxThresholdWindowAndExpiry` | met |
| P2 | A box holds on every gateway and survives a restart | within 1 s, p95 | `TestPenaltyBoxSharedAcrossGateways`, `TestPgStore_EdgePenaltyBoxes` (ADR-20261022105846 amendment 1) | met in tests; production timing open |
| P3 | Memory stays bounded | per-app and per-gateway caps hold | `TestPenaltyBoxPerAppCap` | met |
| P4 | No client boxed in error | none unexplained in 14 days of design-partner traffic | stage-2 review of `boxed` events (ADR-20261022105846 amendment 2; `docs/runbooks/edge-penalty-box-review.md`) | open; review tooling built |

Attack mode (preview):

| # | Criterion | Target | Evidence | Status |
|---|---|---|---|---|
| A1 | Switches on for a bounded time, extends, ends on its own, stays outside quotas and rollbacks, refuses PATCH | all pass | `TestAttackModeOnExtendOff`, `TestDashboardEdgeAttackMode` | met |
| A2 | Needs both `edge_attack_mode` and `edge_challenge`; withdrawing its flag leaves it inert without touching other challenge rules | all pass | `TestAttackModeNeedsItsFlags`, `TestEdgeAttackModeFlag` | met |
| A3 | Used during a real flood without locking out the app's legitimate clients | a stage-2 incident review | `docs/STATUS.md` | open |

AI crawler controls (preview):

| # | Criterion | Target | Evidence | Status |
|---|---|---|---|---|
| I1 | Conditions match the claim and purpose, verify against published ranges, and read absent while unreleased | all pass | `TestEdgeRuleMatchAICrawlerFields`, `TestEdgeRuleMatchAICrawlerFieldsUnreleased`, `TestAICrawlerConditionsNeedTheirFlag` | met |
| I2 | Published ranges refresh, keep the last good copy on a bad feed, and expire | all pass | `TestRefresherKeepsPreviousRangesOnBadFeeds` | met |
| I3 | The catalog matches the crawlers that actually visit | no unknown AI User-Agent above 1% of crawler traffic in 14 days of stage 2 | `gateway_ai_crawler_requests_total`, and a log-mode rule sampling the bots the catalog does not name (`docs/runbooks/edge-ai-crawler-catalog-review.md`, amendment 4) | open |

## Consequences

- The capability matrix lists WAF warn and block and the browser challenge
  as `ga`, and penalties, attack mode and AI crawler controls as `preview`.
  The capabilities API reports each unavailable until its flag includes the
  account.
- The GA features' open criteria are follow-ups, not launch blockers:
  W2's production run, the W4 traffic review, the H3 device measurement and
  H5. The preview features' open criteria are runbooks (C3) and the stage
  reviews (P4, A3, I3, C4), which need their flags turned on for internal
  accounts and design partners.
- WAF `observe` stays generally available (ADR-831 amendment 9) and outside
  this gate; paranoia level 2 stays preview.

## Amendment 1 (2026-10-24): alerts, runbooks and the false-positive review

Criterion C3 asked for alerts and runbooks covering each feature's failure
modes; W4 needed a way to review production detections. Both are built.

**Metrics added** (gatewayd-internal):

- `gateway_edge_penalty_share_total{op,outcome}` (op flush, sync, prune;
  outcome ok, error), `gateway_edge_penalty_share_dropped_total`, and
  `gateway_edge_penalty_share_seconds{stage}`: `queued` (made until written)
  and `propagated` (written until read by this gateway, on the database
  clock, counting only boxes read for the first time after the start-up
  load). Their sum is the time a box takes to hold everywhere, so P2's
  production timing reads from it.
- `gateway_edge_attack_mode_total{app,outcome}` (issued, passed, bypassed,
  rejected): attack mode's decisions, a subset of
  `gateway_edge_challenge_total`, for A3's reviews.

**Alerts** (`faas_edge_security` group in `faas.rules.yml`, family
`edge_security`, all internal; none pages, because every feature fails open
or goes inert):

| Alert | Fires when | Severity | Runbook |
|---|---|---|---|
| FaasEdgeWAFChecksSkipped | busy skips over 0.1% of a gateway's checks for 15m (W2) | warn | FaasEdgeWAFInline |
| FaasEdgeWAFCheckSlow | check p95 over 2 ms for 15m (W1) | warn | FaasEdgeWAFInline |
| FaasEdgeWAFCheckErrors | checks erroring for 10m | warn | FaasEdgeWAFInline |
| FaasEdgeChallengeSolveRateLow | fleet solved/issued under 0.5 for 30m | warn | FaasEdgeChallenge |
| FaasEdgeChallengeSolutionsRefused | a gateway refuses over half its solutions for 15m | warn | FaasEdgeChallenge |
| FaasEdgePenaltyShareFailing | flush, sync or prune erroring for 10m | warn | FaasEdgePenaltyShare |
| FaasEdgePenaltyShareDropped | boxes dropped at the queue bound | warn | FaasEdgePenaltyShare |
| FaasEdgePenaltyShareSlow | propagation p95 over 1 s for 15m (P2) | warn | FaasEdgePenaltyShare |
| FaasEdgeCrawlerRangesStale | every refresh of a crawler failed for 36h | warn | FaasEdgeCrawlerRangesStale |
| FaasEdgeAttackModeActive | an app in attack mode for 15m (A3 review) | info | FaasEdgeAttackMode |
| FaasEdgeAttackModeRefusingClients | attack mode refuses over half an app's requests for 15m | warn | FaasEdgeAttackMode |

`pkg/promqlrules/testdata/edge_security.test.yml` fires each and checks the
cases that must not fire. Each runbook names the flag that withdraws its
feature.

**The false-positive review** is ADR-831 amendment 13, with the W4
procedure in `docs/runbooks/edge-waf-false-positive-review.md`.

Criteria now: C3 met. W4's tooling is built; the 14-day traffic review
remains open, as do W2's production run, H3, H5, P2's production timing and
the stage reviews (P4, A3, I3, C4).

## Amendment 2 (2026-10-25): W1 and W2 measured on the reference node

`TestEdgeWAFInlineLoad_E2E` with `FAAS_E2E_WAF_LOAD_GATE=1`, built from
main at c0b566da41, passed three times on `gregale-compute-2` on
2026-10-24. At 100, 250 and 500 req/s, the in-path check's p95 was
1.48-1.58 ms and under 0.05% of checks were skipped. Every attack was
warned and no benign request was. The full results are in ADR-831
amendment 14.

Criteria now: W1 and W2 met. Still open: H3 (solve time on a low-end
phone); the 14-day reviews that need flags on in production (W4, H5);
P2's production timing; and the stage reviews (P4, A3, I3, C4).

## Amendment 3 (2026-10-25): H3 estimated before the device test

Before anyone measures H3 on a phone, it was estimated in throttled
Chrome (`docs/ops/evidence/20261025-edge-challenge-solve-time/`). The
estimate showed the solver as it stood would miss: about 3.8 s at p95 on a
2020 mid-range phone. ADR-831 amendment 15 made the solver about 5x faster,
for an estimated p95 of 0.88-1.12 s. The device measurement still decides
H3.

## Amendment 4 (2026-10-26): the stage reviews have their data

A check of each open criterion against the data it needs found three gaps:

- **P4** needed to know which clients were boxed. Only per-app counts
  existed, and a box's shared row is pruned an hour after it ends. Boxes are
  now events (ADR-20261022105846 amendment 2), read weekly by
  `docs/runbooks/edge-penalty-box-review.md`.
- **H5**'s metric counts every page served, bots that send browser-like
  headers included, so an attack lowers the ratio without any browser
  failing. `challenged` events record each page's User-Agent, and
  `docs/runbooks/edge-challenge-solve-rate-review.md` uses them to remove
  the bots.
- **I3**'s evidence could not see the crawlers it is about. The gateway
  counts only crawlers the catalog names, and request telemetry keeps only
  a User-Agent family. `docs/runbooks/edge-ai-crawler-catalog-review.md`
  samples the bots the catalog does not name with a log-mode rule on each
  stage-2 app.

Each review runs at least weekly, because events are kept 7 days. No
target changes.
