# Challenge solve-rate review (GA criterion H5)

Procedure, not an alert. ADR-20261024082000 criterion H5 asks that at least
90% of the browser navigations that get the challenge page go on to solve
it, over 14 days of real traffic. Run this at the end of the 14 days, and
weekly if the rate is near 90%. Record each run in `docs/STATUS.md` under the
edge security release gate. A fleet-wide drop pages nobody but fires
`FaasEdgeChallengeSolveRateLow`; see `FaasEdgeChallenge.md`.

## 1. The raw rate per app

```promql
sum by (app) (increase(gateway_edge_challenge_total{outcome="solved"}[14d]))
  / sum by (app) (increase(gateway_edge_challenge_total{outcome="issued"}[14d]))
```

`issued` counts every navigation that got the page, bots that send
browser-like headers included, and bots do not solve it. The raw rate is
therefore a lower bound on what browsers achieve. At 90% or above, H5 holds
for that app; record it and stop.

## 2. Remove the bots

Below 90%, estimate the share of pages that went to browsers from the
`challenged` events, which record each page's User-Agent. They are sampled
(10 per rule per minute on each gateway), so read a week of them:

```bash
gregale edge-rules events --app <slug> --outcome challenged --since 7d --limit 200
```

Page with `--cursor` for more. Count a User-Agent as a browser if it names a
browser engine (`Mozilla/5.0 ... Chrome/`, `Firefox/`, `Safari/`) and none of
`bot`, `crawler`, `spider`, `headless`, `python`, `curl`, `wget`,
`go-http-client`, `java`, the families `ua_family` uses. Then:

    browser solve rate = solved / (issued x browser share of challenged events)

A bot that claims to be a browser still counts as one, so this estimate is
still conservative.

## 3. When browsers fall short

- **Solve time:** check the app's audience against H3. Most visitors on old
  phones, or a difficulty raised above 16 (`--challenge-difficulty`, or
  `gateway_edge_challenge_raised_total` during an attack), slow the page and
  people leave.
- **The page fails:** `invalid` or `expired` outcomes in
  `gateway_edge_challenge_total` mean solutions arrive and are refused.
  Check the gateway's clock, and whether a proxy in front of the app strips
  the cookie or the POST to `/.well-known/gregale-challenge`.
- **JavaScript off:** visitors without JavaScript cannot solve it. The page
  gives them a reference to send the site's owner (ADR-831 amendment 8),
  and the rule can exempt them with `--match`.

## 4. Record

In `docs/STATUS.md`: the date, each app's raw rate, its estimated browser
rate when you needed step 2, and the share of `challenged` events read. H5
is met when every challenged app's browser rate is at least 90% over 14 days.
