# ADR-20261022083601: AI crawler controls

- **Status:** accepted
- **Date:** 2026-10-22
- **Related:** ADR-973 (bot signals), ADR-962 (match expressions), ADR-986
  (protection recipes), ADR-960 (log mode), ADR-831 amendment 7
  (`kind=challenge`), ADR 20261021092138 (edge store), ADR-143 (GeoIP data
  is staged, never fetched by the gateway)

## Context

The most common edge request in 2026 is "keep AI companies from training
on my site, but stay visible in AI search." ADR-973's fields do not answer
it:

- `ua_family eq bot` catches every crawler, search engines included, and
  misses AI fetchers whose User-Agent has no bot marker (`ChatGPT-User`,
  `Perplexity-User`, `meta-externalagent`).
- `verified_bot` covers search engines only. Adding AI crawlers to it would
  change what existing rules do: "block unverified bots", `challenge-path`
  and attack mode all exempt `verified_bot`, so a verified GPTBot would
  start passing rules written to keep it out.
- OpenAI, Perplexity, DuckDuckGo and Mistral verify their crawlers by
  published IP ranges, not reverse DNS, so the gateway cannot verify them
  at all.

## Decision

1. **A catalog in code.** `api.EdgeAICrawlers` lists each AI crawler with
   its operator, its purpose, its User-Agent tokens and, when the operator
   publishes one, its IP-range feed. Purposes: `training` (collects pages
   to train models), `search` (indexes pages for AI search) and
   `assistant` (fetches a page a user asked an assistant about). Adding a
   crawler is a reviewed change, as ADR-973 decided for search crawlers.

2. **Three match fields.** All take `eq`, `ne`, `in`, `not_in`, `exists`
   and `missing` over closed value sets, like ADR-973's:
   - `ai_crawler`: the AI crawler the User-Agent claims.
   - `ai_crawler_purpose`: that crawler's purpose.
   - `verified_ai_crawler`: the claimed crawler, set only when the client
     IP is in its operator's published ranges. A crawler that publishes no
     ranges can never verify, and naming one is a validation error that
     says so.

   The claim fields suit blocking, where a spoofer being blocked too is
   harmless. The verified field suits letting a crawler through, or
   blocking impersonators: `ai_crawler eq gptbot` and
   `verified_ai_crawler missing`. `verified_bot` is unchanged in meaning.

3. **Published ranges are fetched by apid and stored.**
   - A refresh job in apid (`pkg/apid/crawlerranges`) fetches every feed
     every 12 hours: Google, Bing and Apple for `verified_bot`, and
     OpenAI (three feeds), Perplexity (two), DuckDuckGo and Mistral for AI
     crawlers.
   - All feeds share one JSON format: `creationTime` and a list of
     `ipv4Prefix`/`ipv6Prefix`.
   - It fetches over HTTPS only, follows only HTTPS redirects, and reads at
     most 1 MiB.
   - It refuses a whole feed for any unparseable prefix, more than 5,000
     prefixes, or a prefix broader than /16 (IPv4) or /32 (IPv6). The
     previous ranges then stay.
   - Several apid nodes can run the job: a feed fetched within half the
     interval is skipped, and writes are upserts.
   - `FAAS_EDGE_CRAWLER_RANGES_REFRESH=0` turns the job off for installs
     without outbound internet.
   - Gateways never fetch; they reread the table every 10 minutes
     (ADR-143's posture for external data).

4. **Storage.** A new platform table, `edge_crawler_ip_ranges`, holds one
   row per crawler: `crawler`, `source_url`, `prefixes text[]`,
   `source_created_at` and `fetched_at`, with CHECKs mirroring the bounds.
   The gateway read is a `pgCore` method, so an edge store carries it
   (ADR 20261021092138).

5. **Verification at the gateway.**
   - Ranges whose `fetched_at` is older than 7 days stop verifying, so a
     feed that stopped refreshing cannot vouch for addresses its operator
     has given up.
   - A claimed search crawler inside its operator's ranges is
     `verified_bot` without a DNS lookup. Outside them the ADR-973 DNS
     check still runs, because the feeds do not list every crawler
     address (Google's special crawlers, for example).
   - `verified_ai_crawler` never looks up DNS.
   - Both are resolved at most once per request, and only when a rule
     evaluates them.

6. **Visibility.** The gateway counts every request whose User-Agent names
   an AI crawler, before any gate acts, in
   `gateway_ai_crawler_requests_total{app,crawler}`. The crawler label is a
   catalog name, so the label set is closed. The edge-protection summary
   (API, dashboard, `gregale edge-rules summary`) gains `ai_crawlers`: per
   crawler, its operator, purpose and count, largest first.
   `edge_crawler_ranges_refresh_total{crawler,outcome}` shows the refresh
   job's health.

7. **Recipe `control-ai-crawlers`.**
   - Params: `purposes` (default `training`; any of the three, or `all`)
     and `action` (`block`, the default, or `challenge`), plus the usual
     path.
   - It builds a deny-all `kind=ip` rule or a `kind=challenge` rule with
     the condition `ai_crawler_purpose in [purposes]`.
   - Like every recipe it starts in log mode unless enforced, and it
     follows the plan gates of those kinds (Hobby and above).
   - The default answers the open product question of what to suggest:
     block training crawlers, leave AI search and assistants alone.

8. **Simulation.** `gregale edge-rules trace` classifies the simulated
   User-Agent for `ai_crawler` and `ai_crawler_purpose`.
   `--verified-ai-crawler` simulates a client IP inside the claimed
   crawler's ranges. The simulator does no network access.

## Consequences

- A claimed-crawler request that a rule checks against ranges costs a scan
  of that crawler's prefixes (at most 5,000; about 500 today), with no
  network round trip.
- Every request pays one User-Agent scan for the counter. Telemetry already
  lowercases and scans the header, and the catalog is about 25 short
  substrings.
- Crawlers that publish no ranges (ClaudeBot, CCBot, Bytespider and others)
  can be blocked or challenged by claim but never let through as verified.
- New AI crawlers need a code change. The feeds, which change weekly, do
  not.

## Not in this step

- An AI crawler suggestion from request telemetry (ADR-20261020105000):
  telemetry records `ua_family`, not the crawler.
- `robots.txt` management and pay-per-crawl.
- Verifying crawlers whose operators document reverse DNS but publish no
  ranges (Amazonbot): only the range method is implemented for AI
  crawlers.
