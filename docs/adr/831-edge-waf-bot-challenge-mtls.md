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

## Amendment 1: step 1 as built on `feat/edge-waf-observe` (2026-10-09)

Step 1 was implemented ahead of acceptance, as an observe-only preview for
review. This ADR is still `proposed`: the branch must not merge until the
product owner accepts it, including the plan gating below. Where the build
differs from the proposal:

- **Inspection is off the request path.** The gateway records the body
  prefix while the proxy streams it upstream, and hands headers and prefix to
  a node-wide pool of `EdgeWAFWorkers` (2) after the request finishes. Observe
  mode therefore adds no request latency; the cost to gate on is worker CPU
  and queue pressure (`gateway_waf_inspection_seconds`, the `dropped` and
  `sampled_out` outcomes). The p95 ≤ 2 ms latency gate still applies, to the
  in-path evaluation that `block` and `warn` will need; that needs its own
  amendment and measurement on the reference node.
- **Body cap: default 8 KiB, maximum 64 KiB** (`inspect_body_bytes`), not a
  64 KiB default, because body inspection dominates the cost (measured in
  amendment 2; an earlier estimate of ~3 µs per byte was wrong by 10-30x).
  Each app's inspections are budgeted in worker milliseconds (amendment 2,
  item 3), so a larger cap buys fewer inspections, not more CPU.
- **Signals.** `gateway_waf_inspections_total{app,outcome}`,
  `gateway_waf_detections_total{app,category}` and
  `gateway_waf_rule_matches_total{app,rule_id}`. `rule_id` is limited to CRS
  detection rules (911000–948999); the vendored CRS has 391, pinned by a
  test. The edge-protection summary gains a `waf` section (inspected,
  detected, not inspected, categories, top 10 rule IDs).
- **Alerting is a separate preset.** Observe-only detections are not
  rejections, so they are not added to `edge_rejection_pressure`. A new
  opt-in, webhook-only `edge_waf_detections` preset (more than 25 in 15
  minutes, Pro and above) covers them. Once `block` ships, its rejections go
  to `gateway_edge_rejections_total{kind="waf"}` and the existing preset
  counts them without changes.
- **Plan gating (still to decide):** built as Pro 5 rules per app, Scale 20,
  Free and Hobby 0, in `pkg/api/limits.go`.

## Amendment 2: measured inspection cost (2026-10-10)

Measured with `pkg/edgewaf` benchmarks (`BenchmarkEvaluate`,
`BenchmarkEvaluateBodyShape`, `TestInspectorLoad`) on
`gregale-internal-test-1`: one Intel Xeon 2.8 GHz vCPU per worker, CRS
v4.25 through Coraza, Go 1.26.9. That node is shared and not the reference
node; user CPU was 96% of wall time, and repeat runs varied by up to 1.6x.

| Request | PL1 | PL2 |
|---|---|---|
| headers only, no body | 2.1 ms | 4.0 ms |
| 1 KiB JSON body | 29 ms | 54 ms |
| 8 KiB body, one long string or text/plain | 52-58 ms | — |
| 8 KiB JSON, ~100 objects × 5 fields | 140-230 ms | 440-460 ms |
| 32-64 KiB JSON | ~0.57-0.59 s | 0.66-1.05 s |

Cost stops growing above ~32 KiB of many-field JSON, consistent with
Coraza's argument limit. Go's `regexp` engine is most of the CPU profile.

Pool behaviour with the shipped settings (2 workers, 20 inspections per
second per app, burst 40, queue 256), 20 apps each sending 50 matched
requests per second for 15 s:

- headers only: 452 inspections/s, 54.8% sampled out by the per-app
  budget, 0.03% dropped. The budget, not CPU, is the limit.
- 8 KiB JSON bodies: 16 inspections/s for the whole node (1.8% of matched
  requests), 75.5% sampled out, 22.2% dropped on a full queue.

Consequences:

1. **The in-path latency gate cannot be met as built.** A headers-only
   inspection at PL1 already takes ~2.1 ms of CPU against the ADR's
   p95 ≤ 2 ms budget, before any body. `warn` and `block` need a cheaper
   engine configuration (fewer rules, a faster regex backend, or
   headers/URI-only blocking) and a new measurement before they are offered.
2. **Observe mode on bodies is sampling, not coverage.** At 8 KiB a node
   inspects on the order of 10-20 body-carrying requests per second in
   total, so detection counts are a sample. The summary and docs already
   report `not_inspected`; the docs must say plainly that it is a sample.
3. **Budget pricing undercharged bodies.** An 8 KiB body costs 25-100x a
   headers-only inspection but was charged 2 tokens. Done (2026-10-10): the
   per-app budget is now worker time, 400 ms per second (20% of the
   2-worker pool) with a 2000 ms burst. A sample is admitted while the
   app's balance is positive, charged an estimate from the table above
   (2 ms plus 25 µs per body byte, doubled at PL2), and trued up to its
   measured time after inspection. Overruns become debt of up to one burst;
   dropped samples are refunded. Headers-only traffic gets ~200
   inspections/s per app and 8 KiB JSON ~2/s, instead of 20/s for both.

Items 1 and 2 are open decisions for the product owner before step 1
leaves preview.

## Amendment 3: cheaper engine configurations (spike, 2026-10-10)

Four configurations at PL1 on `gregale-internal-test-1` while it was idle
(load 0.0, one 2.8 GHz Xeon core, CRS v4.25, Coraza v3.8.1). Every
configuration detected all five probe attacks (SQLi in the query, XSS and
RCE in a JSON body, LFI in the query, SQLi inside 8 KiB of JSON) and none
flagged a benign 8 KiB JSON body. "RE2" registers the
`coraza-wasilibs` v0.2.0 operators (RE2, Aho-Corasick and libinjection
compiled to WebAssembly, run by wazero; pure Go, no cgo). "Trimmed" loads
only REQUEST-901 initialization and the LFI, RFI, RCE, XSS and SQLi files
(930, 931, 932, 941, 942).

| Configuration | headers+URI p50 / p95 / p99 | 8 KiB JSON | 8 KiB text | 64 KiB JSON |
|---|---|---|---|---|
| shipped (full CRS) | 1.27 / 1.46 / 3.30 ms | 134 ms | 59 ms | 326 ms |
| RE2 | 1.45 / 1.68 / 2.72 ms | 120 ms | 8.4 ms | 296 ms |
| trimmed | 0.76 / 0.89 / 1.78 ms | 97 ms | 38 ms | 243 ms |
| RE2 + trimmed | 0.86 / 1.08 / 1.69 ms | 91 ms | 5.3 ms | 233 ms |

Headers+URI latency is over 3000 requests across five realistic paths; body
columns are means. The 2.1 ms headers-only figure in amendment 2 was a mean
taken while the node was busy.

Findings:

- **In-path blocking on headers and URI meets the 2 ms p95 gate.** Even
  the full rule set has a 1.46 ms p95 on an idle core; the trimmed set has
  0.89 ms, leaving headroom for a loaded node. p99 is 1.7-3.3 ms.
- **Bodies cannot be blocked in-path with any of these.** Many-field JSON
  stays at 90-134 ms per 8 KiB. Its profile is 55% Go `regexp` calls on
  hundreds of short values and 30% hashing in Coraza's transformation
  cache: per-argument overhead, which RE2-in-WebAssembly does not reduce.
- **RE2 is a large win only for long text fields** (7x on 8 KiB of text)
  and costs ~0.2 ms on headers-only. Operator registration is process-wide,
  so a gateway cannot use RE2 for bodies and the Go engine for headers.
- **Trimming trades coverage for speed.** It drops scanner detection (913),
  protocol enforcement and attack (920, 921), multipart (922), PHP (933),
  generic (934), session fixation (943) and Java (944).

Proposed shape for the next step, for the product owner to decide:

1. `warn` and `block` evaluate headers and URI only, in-path, with the
   trimmed rule set at PL1, behind its own latency measurement on the
   reference node.
2. Bodies stay observe-only and sampled off-path, budgeted in worker time
   (amendment 2), with the full rule set.
3. Adopt RE2 only if body samples turn out to be mostly long text fields;
   for JSON APIs it adds a dependency without a gain.
