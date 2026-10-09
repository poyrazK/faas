# ADR-909: Throttle composite keys and response-status counting

- **Status:** accepted
- **Date:** 2026-10-09
- **Related:** ADR-104 (dimensional throttles), ADR-091 (edge rules)

## Context

ADR-104 throttles key on one dimension and charge every request. The
attacks they should stop do not fit that shape: credential stuffing is
many failed logins per IP *and* per username, and limiting every request
to a login route also limits users who log in successfully. Cloudflare's
rate-limiting rules count on several characteristics and can count only
responses with given statuses.

## Decision

1. **Composite keys.** `key_by: "composite"` with `key_fields` (1–4 of
   `ip`, `country`, `api_key`, `consumer_id`, `jwt_subject`, `jwt_claim`,
   `method`, `path`, `header:<name>`) keys one bucket per combination of
   values. Each field resolves with the same trust rules as the single
   dimension (trusted client IP, IPv6 by /64, GeoIP, verified auth); an
   unavailable field fails closed as before, and a field the request does
   not carry makes the identity missing, so `missing_key_policy` applies.
   Identities longer than 128 bytes are hashed. The composite is one more
   dimensional `key_by`, so the ADR-104 cardinality cap, pinned
   `__other__` bucket and central sharding apply unchanged.

2. **Response-status counting.** `count_statuses` (≤16 codes) makes a rule
   admit a request while its bucket has a token, without spending it, and
   charge the bucket after the response only when the final status is one
   it counts. Our own 429 is not charged (the request never reached the
   app); a later edge rule's rejection, such as a 401 from a JWT rule, is.
   Charges run inline with the local limiter and off the request path in
   central mode, so a Postgres round trip never delays a response.

3. **Bounds.** Concurrent in-flight requests can all pass the check before
   any is charged, so a burst of N parallel failures can exceed the bucket
   by up to N−1 once. In central mode each gateway checks against the
   balance the shared counter last reported to it. Both are the accepted
   cost of not holding a token across the upstream call.

4. **Fallback.** A caller without the per-request hook set (anything
   outside `ServeHTTP`) charges up front: a response-counted rule degrades
   to an ordinary throttle, never to no throttle.

## Consequences

- "5 failed logins per minute per IP and username" is one rule:
  `key_by=composite`, `key_fields=[ip, header:x-username]`,
  `count_statuses=[401]`.
- No schema change: throttle actions are stored as JSON.
