# ADR-908: Edge-rule security events

- **Status:** accepted
- **Date:** 2026-10-09
- **Related:** ADR-904 (log mode and hit counts), ADR-906 (match
  expressions), ADR-907 (lists)

## Context

ADR-904 counts how many requests each rule matched. Tuning a rule, above
all a log-mode rule before enforcing it, needs to know *which* requests it
matched: client, country, path, user agent. Cloudflare's security events
view is the reference.

## Decision

1. **Sampled events.** When the gateway counts a rule match (ADR-904, at
   most once per rule per request) it also records a full event for the
   first 10 matches of that rule in each one-minute flush interval, with at
   most 2,000 events pending per gateway. Events are telemetry: a failed
   flush drops them instead of retrying, and the hit counts stay the source
   of truth for volume.

2. **Contents.** Rule and app IDs, outcome (`matched` for enforced rules,
   `logged` for log-mode rules), time, request ID, method, host, the request
   path without its query string (≤1,024 bytes), the trusted client IP and
   country when known, and the user agent (≤256 bytes). No other headers,
   no query string, no body: these can carry credentials.

3. **Storage.** `edge_rule_events`, written in one batch per flush and
   pruned after 7 days. Like hit counts, rows carry no foreign keys: a
   deleted rule's events age out with the rest.

4. **Read API.** `GET /v1/apps/{slug}/edge-rules/events`, newest first,
   filtered by rule, outcome and `since`, with cursor paging (≤200 per
   page). How far back a plan can read is `EdgeRuleEventsWindowHours` in
   `pkg/api/limits.go` (Free 24 · Hobby 72 · Pro 168 · Scale 168); a longer
   `since` is clamped and the response reports the effective start.
   `gregale edge-rules events` wraps it.

## Consequences

- Rare matches are captured completely; a hot rule shows up to 10 examples
  per minute per gateway, enough to see who it hits without storing every
  request.
- Client IPs are personal data: they are kept 7 days at most, readable only
  by the app's account, and outlive an account deletion by at most that
  window.
