# WAF false-positive review (GA criterion W4)

Procedure, not an alert. ADR-20261024082000 criterion W4 asks that WAF warn
and block show no false positives beyond the documented ones in 14 days of
production traffic. Detection events are kept 7 days, so run this review at
least once a week during the 14 days, and record each run in
`docs/STATUS.md` under the edge security release gate.

Tools: the WAF review (ADR-831 amendment 13): `GET
/v1/apps/{slug}/edge-rules/waf-review`, `gregale edge-rules waf-review`, and
the WAF review section of the dashboard's Edge rules page.

## 1. Find the apps to review

Apps whose WAF rules detected anything in the last 7 days:

```promql
sum by (app) (increase(gateway_waf_detections_total[7d])) > 0
```

The CRS rules that scored across the fleet, and on how many apps:

```promql
topk(20, sum by (rule_id) (increase(gateway_waf_rule_matches_total[7d])))
count by (rule_id) (sum by (app, rule_id) (increase(gateway_waf_rule_matches_total[7d])) > 0)
```

A CRS rule that scores on many apps is the first suspect for a platform-wide
false positive.

## 2. Review each app

With the customer's consent, or on an internal or design-partner account:

```bash
gregale edge-rules waf-review --app <slug> --since 7d
```

Each group is one WAF rule's detections by one CRS rule on one path:

- **likely_false_positive**: a documented false positive (the table in
  `docs/security.md`), or at least 3 clients flagged on one path with none
  sending most of the detections. Open the example requests
  (`gregale edge-rules events --app <slug> --request-id <id>`) and confirm.
- **likely_attack**: most detections came from clients flagged on 5 or more
  paths. Spot-check one example; scanners send the same probes everywhere.
- **review**: too few clients to tell; open the examples.
- **excluded**: the rule already excludes it.

If `truncated` is set, narrow with `--since` or `--rule`.

## 3. Classify and record

For every group that is not an attack, decide:

| Finding | Action |
|---|---|
| A documented false positive | Expected. The customer excludes it (`gregale edge-rules waf-exclude <rule> --add <id>`) or narrows the rule's path. |
| A new false positive caused by the app's own traffic shape | Add a benign request reproducing it to `pkg/edgewaf/benign_test.go`, add the rule to `api.EdgeWAFKnownFalsePositive` and the table in `docs/security.md` (the test keeps all three equal), and tell the customer. |
| A new false positive on ordinary traffic any app sends | A platform finding: fix the engine configuration (as ADR-831 amendment 6 did for PUT and text bodies), with a benign corpus entry, before W4 can be met. |

Record per run: date, apps reviewed, groups by assessment, new false
positives and what was done. W4 is met after 14 days with none unexplained.
