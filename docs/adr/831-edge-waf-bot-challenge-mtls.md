# ADR-831: Edge WAF signatures, bot challenge, and client certificates

- **Status:** proposed
- **Date:** 2026-10-09
- **Related:** ADR-091 (edge rules), ADR-104 (throttle keying), ADR-520
  (self-hosted custom-domain TLS), ADR-829 (pre-auth observe default),
  `docs/api-hosting-roadmap.md` ("What not to do next" §1)
- **Decision needed:** product owner, on scope, sequencing, and plan gating.
  This ADR proposes; nothing is built until it is accepted.

## Context

The edge now covers authentication (`jwt`, consumer keys), source control
(`ip`, `geo`, ingress allowlist, `internal_only`), abuse limits (`throttle`
with per-IP keys, the pre-auth source limit), request shape (`validate` for
body, path, query, and headers; `limit`), and observability (per-app
rejection counters, the edge-protection summary, and three security alert
presets). Every one of those gates runs before a VM wakes.

Three protections are still missing:

1. **Attack-signature detection.** Nothing recognises SQL injection, XSS,
   path traversal, or protocol-abuse payloads. `validate` only checks a
   declared contract, and most apps declare none for most routes.
2. **Bot mitigation for browser routes.** Credential stuffing and scraping
   can only be slowed by rate limits; there is no way to make a client prove
   it is a browser.
3. **Client-certificate authentication (mTLS).** B2B, fintech, and
   regulated APIs often require it.

Two facts shape every option:

- **Not all traffic passes through a CDN.** `*.gregale.dev` app hosts are
  proxied by Cloudflare, but customer custom domains reach the platform's
  own Caddy edge directly (ADR-520). A Cloudflare-provided WAF or bot
  product would leave every custom domain uncovered, and the platform must
  not depend on Cloudflare for customer domains (ADR-520).
- **TLS terminates before Gregale code.** `gatewayd-public` serves plain
  HTTP behind Caddy and Cloudflare (ADR-070), so a client certificate is
  only visible to whichever layer terminates TLS.

The roadmap also says not to add more isolated edge-rule kinds before the
core API journeys are coherent. This ADR therefore proposes a sequence and
explicit gates rather than shipping all three.

## Proposal

### 1. `kind=waf`: OWASP Core Rule Set at the edge (first)

- **Engine:** [Coraza](https://coraza.io) (OWASP project, Go, Apache-2.0)
  embedded in `gatewayd-internal`, with the OWASP Core Rule Set v4 vendored
  at a pinned version and checksum. No runtime downloads.
- **Rule shape:** per-route like every edge rule. Action fields: paranoia
  level (1 or 2), inbound anomaly threshold (default 5), rule-ID exclusions
  for false positives, and an inspected-body cap (default 64 KiB; larger and
  streaming bodies are inspected up to the cap only). Modes reuse
  `validate_mode`: `observe` (default), `warn`, `block`.
- **Placement:** after `throttle` and the body limits, before `validate`. A
  flood is rate-limited before it costs signature-matching CPU, and a
  request rejected as an attack never wakes the app.
- **Cost controls:** one compiled ruleset per paranoia level shared by all
  apps; per-rule state is only the exclusion list. A latency budget is a
  gate for leaving preview: p95 added latency at PL1 ≤ 2 ms for an 8 KiB
  request on the reference node, measured before launch.
- **Signals:** `gateway_edge_rejections_total{kind="waf"}` for blocks,
  observe-mode matches in a per-app counter with a bounded rule-ID label,
  and the edge-protection summary and `edge_rejection_pressure` preset pick
  them up. Matched payload text is never logged or audited.
- **Plan gating (to decide):** Pro and above, because the CPU cost scales
  with traffic.

### 2. `kind=challenge`: proof-of-work interstitial (second, browser routes)

- **Mechanism:** for a matched route without a valid challenge cookie, the
  gateway serves a small HTML page that computes a proof of work in the
  browser and then sets a signed, short-lived cookie bound to the client
  address. No third-party script, so it works on custom domains and has no
  privacy or availability dependency.
- **Scope:** browser navigation routes only. Requests carrying a consumer
  key or a verified JWT, and non-HTML `Accept` headers, bypass it, so API
  clients are never challenged.
- **Rejected for now:** Cloudflare Turnstile. It needs a third-party script
  and site keys, and on custom domains it would make Gregale depend on
  Cloudflare after all.
- **Plan gating (to decide):** Hobby and above.

### 3. Client certificates: custom domains only, on demand (third)

- **Feasibility:** Gregale code never sees TLS, so client certificates can
  only be checked by the layer that terminates it. For custom domains that
  is Gregale's own Caddy (ADR-520), which can request and verify client
  certificates against a per-domain CA pool and forward the verified
  subject and fingerprint in a header that `gatewayd-public` strips from
  inbound requests and trusts only from Caddy. For `*.gregale.dev` hosts
  Cloudflare terminates TLS; client-certificate checks there would require a
  Cloudflare product and are out of scope.
- **Shape:** a per-domain trusted-CA upload plus a `kind=mtls` rule that
  requires a verified certificate (optionally matching a subject pattern)
  on matched routes.
- **Prerequisite:** ADR-520 must be accepted and running in production.
- **Recommendation:** do not build until a customer needs it; this section
  records the constraints so the design is not rediscovered.

## Sequencing and gates

1. `kind=waf` as a preview in `observe`-only mode, with the latency budget
   measured on the reference node and a false-positive review on real
   traffic before `block` is offered.
2. `kind=challenge` after `waf` reaches beta.
3. `kind=mtls` only on customer demand and after ADR-520.

Each step needs its own implementation ADR amendment with the measured
numbers, as required for leaving `preview`.

## Risks

- **False positives** block legitimate traffic: default `observe`, rule-ID
  exclusions, and a review gate before `block`.
- **CPU exhaustion through expensive payloads:** throttle and body limits
  run first, and the inspected-body cap bounds work per request.
- **Rule-set supply chain:** vendored, pinned, checksummed CRS; upgrades are
  reviewed changes, not runtime fetches.
- **Scope creep against the roadmap:** the sequence above adds one kind at a
  time behind measured gates instead of three at once.

## Rejected alternatives

- **Rely on Cloudflare WAF and Bot Management.** Leaves custom domains
  unprotected and contradicts ADR-520's independence from Cloudflare for
  customer domains.
- **A hand-written signature list.** Re-implements the maintained OWASP rule
  set with less coverage and no community review.
- **Ship all three at once.** Too much new surface before the first one has
  production evidence.
