# FaasEdgeCrawlerRangesStale

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`, group
`faas_edge_security` (ADR-20261024082000 amendment 1).
Metric: `edge_crawler_ranges_refresh_total{crawler,outcome}` (apid; ok,
fresh, fetch_error, invalid, store_error).
Design: ADR-20261022083601.
Severity: warn.

## Symptom

apid fetches each crawler operator's published IP ranges every 12 hours
(`pkg/apid/crawlerranges`) into `edge_crawler_ranges`, and gateways reread
them every 10 minutes. For 36 hours every attempt for one crawler failed:
no apid fetched it (`ok`) or saw a fresh copy another apid fetched
(`fresh`). Gateways keep the last good ranges for 7 days, then stop using
them: that crawler no longer verifies, so `verified_ai_crawler` rules stop
matching it, and Googlebot, Bingbot and Applebot fall back to DNS checks.

## Verify

```promql
sum by (crawler, outcome) (increase(edge_crawler_ranges_refresh_total[36h]))
```

```bash
journalctl -u faas-apid --since '-36h' --no-pager | grep 'crawler ranges refresh failed'
```

The log line names the feed URL and the outcome.

## Check

- **fetch_error**: the feed URL moved or is unreachable from the control plane.
  Fetch it by hand from the apid host. Operators move these files (Google
  moved its file to `/static/crawling/ipranges/` in 2026); the URLs live in
  `pkg/api/edge_ai_crawlers.go`.
- **invalid**: the feed answered but its body was refused as a whole: over
  1 MiB, more than 5,000 prefixes, not the `{"prefixes": [...]}` format, or a
  malformed prefix. apid keeps the previous ranges.
- **store_error**: apid could not write `edge_crawler_ranges`; check the
  database path.
- `FAAS_EDGE_CRAWLER_RANGES_REFRESH=0` turns the refresh off on installs
  without outbound internet; then no attempts are made and this alert does
  not fire.

## Silence

```bash
amtool silence add --matchers='alertname=FaasEdgeCrawlerRangesStale' --duration=24h \
  --comment='crawler feed moved; fix in progress'
```

## Recover

1. A moved feed: update its URL in `pkg/api/edge_ai_crawlers.go` and release
   apid; the next refresh fetches it.
2. A changed format: adapt the parser in `pkg/apid/crawlerranges` with a test
   fixture from the new feed.
3. If the ranges went stale and rules misbehave, withdraw AI crawler
   conditions with
   `gregalectl flags disable --key edge_ai_crawlers --reason "..." --yes`
   (the conditions read as absent; rules are kept).
