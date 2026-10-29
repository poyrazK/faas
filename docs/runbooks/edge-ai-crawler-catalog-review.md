# AI crawler catalog review (GA criterion I3)

Procedure, not an alert. ADR-20261024082000 criterion I3 asks that the AI
crawler catalog match the crawlers that actually visit. No AI crawler missing
from it may exceed 1% of crawler traffic over 14 days of stage 2. The
gateway counts only the crawlers it recognises
(`gateway_ai_crawler_requests_total`), and request telemetry keeps only a
User-Agent family, so the missing ones have to be sampled with a log-mode
rule. Record each run in `docs/STATUS.md` under the edge security release
gate.

## 1. Sample the unrecognised bots

On each stage-2 app, with the customer's consent, add a rule that matches
bots the catalog does not name and only counts them. A log-mode rule never
acts; this one denies every address, so it matches every such request:

```bash
gregale edge-rules create --app <slug> --kind ip --ip-deny 0.0.0.0/0 --ip-deny ::/0 \
  --match-host <host> --match-path '/*' --mode log \
  --match '{"all":[{"field":"ua_family","op":"eq","value":"bot"},{"field":"ai_crawler","op":"missing"}]}'
```

The `ai_crawler` field needs the account to have `edge_ai_crawlers`, which
stage 2 gives it. Leave the rule in place for the 14 days, then delete it.

## 2. Measure the two sides

Recognised AI crawlers, by crawler (`app` is the app's ID):

```promql
sum by (crawler) (increase(gateway_ai_crawler_requests_total{app="<app-id>"}[7d]))
```

Unrecognised bots: the rule's logged count, and a sample of their requests.
Logged events are sampled (10 per rule per minute on each gateway) and kept
7 days, so read them weekly:

```bash
gregale edge-rules stats --app <slug> --window 7d
gregale edge-rules events --app <slug> --rule <rule-id> --outcome logged --since 7d --limit 200
```

## 3. Find AI crawlers among them

Group the sampled User-Agents by product token (`ExampleBot/1.2` gives
`examplebot`). A token is an AI crawler if its operator documents it as
gathering data for AI training, AI search or an AI assistant's live
fetching. Search engines, uptime monitors, SEO tools and feed readers are
not. For each AI crawler found:

    share = logged count x (its share of the sampled events)
            / (recognised AI requests + logged count x AI share of the sample)

## 4. Act and record

- Any AI crawler at 1% or more of crawler traffic fails I3 until the catalog
  names it. Add it to `pkg/api/edge_ai_crawlers.go`, with its purpose and,
  where its operator publishes them, its address ranges for verification
  (ADR-20261022083601). Then the review starts its 14 days again.
- Smaller ones go in the catalog too, but don't block I3.
- In `docs/STATUS.md`: the date, the apps sampled, the logged count, each AI
  crawler found with its share, and the catalog changes.
