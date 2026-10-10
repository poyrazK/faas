# ADR-968: Bot signals in edge-rule conditions

- **Status:** accepted
- **Date:** 2026-10-10
- **Related:** ADR-962 (match expressions), ADR-966 (ASN), ADR-960 (log mode)

## Context

The most common edge-rule request after rate limits is "keep scrapers and
scripts out, but let search engines in." Match conditions can test the
`User-Agent` header with `contains` or `regex`, but every customer then
maintains their own bot patterns, and a header test cannot tell real
Googlebot from a scraper that sends Googlebot's User-Agent.

## Decision

1. **`ua_family` field.** The gateway classifies the `User-Agent` header as
   one of `browser`, `mobile`, `bot`, `tool` (curl, wget, HTTP client
   libraries) or `other`. Bot markers (`bot`, `crawl`, `spider`, headless
   browsers, a `+http` contact URL, …) win over everything else. No header
   means the field is absent. Classification reads at most 512 bytes.

2. **`verified_bot` field.** Names the known crawler a request was verified
   as: `googlebot`, `bingbot`, `applebot`, `yandexbot`, `baiduspider`,
   `yahoo`, `petalbot`. A request is verified only when its User-Agent
   claims that crawler, the client IP's reverse DNS name ends in the
   crawler's published domain (e.g. `.googlebot.com`), and that name
   resolves back to the IP — each operator's documented verification
   method. Otherwise the field is absent. It uses the trusted client IP
   (ADR-962 §5); an untrusted IP never verifies.

3. **Bounded lookups.** Verification runs only when a rule's condition
   evaluates `verified_bot` and the User-Agent claims a known crawler, at
   most once per request. Verdicts are cached per IP and crawler (1 h
   positive, 10 min negative, 16k entries); concurrent requests for one IP
   share a lookup; at most 64 lookups are in flight per gateway and each
   has a 300 ms deadline. A spent budget, timeout or DNS error fails closed
   (unverified) and is not cached. The worst case is one capped DNS
   round trip on a claimed-crawler request, never on ordinary traffic.

4. **Closed value sets.** Both fields take `eq`, `ne`, `in`, `not_in`,
   `exists`, `missing`, compared case-insensitively; an unknown value is a
   validation error rather than a rule that silently never matches. Neither
   field takes `in_list`.

5. **Simulation.** `gregale edge-rules trace` classifies the simulated
   User-Agent; `--verified-bot` simulates a client IP that passes
   verification. The simulator does no DNS.

## Consequences

- "Block unverified bots" is one rule:
  `all: [{ua_family eq bot}, {verified_bot missing}]` — real Googlebot
  passes, a scraper using its User-Agent does not. Log mode (ADR-960)
  shows what it would catch before enforcing.
- The crawler table is code; adding a crawler is a reviewed change, not a
  customer setting. Crawlers verifiable only by published IP ranges (e.g.
  DuckDuckBot) are not covered yet.
- gatewayd-internal now performs outbound DNS through the host resolver
  for claimed-crawler requests.
