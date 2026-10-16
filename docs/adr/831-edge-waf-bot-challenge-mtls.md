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
   trimmed rule set plus 913 and 921 (see the addendum) at PL1, behind its own latency measurement on the
   reference node.
2. Bodies stay observe-only and sampled off-path, budgeted in worker time
   (amendment 2), with the full rule set.
3. Adopt RE2 only if body samples turn out to be mostly long text fields;
   for JSON APIs it adds a dependency without a gain.

### Amendment 3 addendum: which rule files a headers/URI blocker needs

The trimmed set drops the families that matter most for headers-and-URI
blocking, so each was added back and measured (same idle node, PL1, 3000
requests). Probes: sqlmap and Nikto user agents, CRLF injection in the
query, HTTP response splitting in the query, Transfer-Encoding plus
Content-Length, encoded path traversal, and two benign requests.

| Rule files | Scanners | CRLF / splitting | Traversal | TE+CL | p95 / p99 |
|---|---|---|---|---|---|
| full CRS | yes | yes | yes | 3 pts (below 5) | 1.63 / 3.52 ms |
| trimmed | no | no | yes | — | 0.99 / 1.94 ms |
| trimmed + 913 | yes | no | yes | — | 1.01 / 2.03 ms |
| trimmed + 913 + 921 | yes | yes | yes | — | 1.07 / 2.06 ms |
| trimmed + 913 + 920 + 921 | yes | yes | yes | 3 pts (below 5) | 1.19 / 2.59 ms |

No configuration flagged the benign requests, and the body probes from the
table above still detected in every configuration. Run-to-run variation
on this node is about 0.15 ms at p95 (the full set measured 1.46 ms
earlier).

Trimmed plus 913 and 921 catches every header and URI probe the full rule
set catches, at 1.07 ms p95 against 1.63 ms. 920 adds 0.12 ms p95 for one
protocol warning that scores below the threshold on its own, so it is
left out. The proposed in-path set is therefore: 901, 913, 921, 930, 931,
932, 941 and 942 at PL1.

## Amendment 4: warn and block on headers and URI (2026-10-10)

Built on `feat/edge-waf-observe`, still ahead of acceptance; the branch must
not merge until this ADR is accepted, including this amendment.

- **Modes.** `mode` is `observe` (default), `warn` or `block`. Every mode
  keeps the off-path sampling of amendment 2 (headers and body, full rule
  set, rule's paranoia level). `warn` and `block` add an in-path check of
  headers and URI with the amendment 3 rule set (901, 913, 921, 930, 931,
  932, 941, 942) at paranoia level 1, with request body access off so the
  body keeps streaming to the proxy. `warn` and `block` therefore require
  `paranoia_level` 1.
- **Verdicts.** `block` answers a detection with 403 (`forbidden` problem
  naming the edge rule, not the CRS rule, so a caller does not learn which
  signature fired), counted in `gateway_edge_rejections_total{kind="waf"}`;
  the app never wakes. `warn` sets `X-WAF-Warning: <edge rule id>` on the
  response and passes the request. A request detected in-path is not also
  sampled, so it is not counted twice. Bodies are never blocked.
- **Fail open.** In-path checks run on the request goroutine, at most
  `EdgeWAFInlineConcurrency` (2) per node at once, within a per-app budget
  of 500 worker-ms per second (burst 1000; a check is ~1 ms). A check with
  no free slot or budget is skipped and the request passes, counted as
  `outcome="skipped"`. An attacker can therefore exceed roughly 500 checked
  requests per second per app to push traffic through unchecked; throttle
  rules and the pre-auth limit are the defence against floods, not the WAF.
  `FAAS_EDGE_WAF_INLINE_DISABLED=1` on gatewayd-internal turns in-path
  checks off; warn and block rules then only observe.
- **Signals.** `gateway_waf_inline_checks_total{app,outcome}` (clean,
  warned, blocked, skipped, error) and `gateway_waf_inline_seconds`. The
  edge-protection summary reports `warned`, `blocked` and `inline_skipped`;
  `edge_waf_detections` counts sampled detections plus warned and blocked.
- **Before leaving preview:** measure `gateway_waf_inline_seconds` p95 on
  the reference node under production-like load against the 2 ms gate,
  and review false positives in `observe` on real traffic before
  recommending `block`.

## Amendment 5: detections as security events (2026-10-13)

Customers could only see WAF detections as aggregate metrics, which made
false-positive review (the gate before recommending `block`) impractical.
Detections are now ADR-964 security events:

- New event outcomes: `detected` (a detection in an off-path sample, any
  mode), `warned` and `blocked` (in-path verdicts of warn and block rules).
- A detection event carries the CRS rule IDs that scored (at most 32), their
  attack categories (at most 16) and the anomaly score, plus the usual
  request ID, method, host, path without query, client IP, country and user
  agent. Never the matched payload.
- Detections have their own sampling budget, 10 per rule per minute per
  gateway, separate from match sampling. A WAF rule's plain matches are
  still counted (ADR-960) but no longer sampled as events: every inspected
  request matches, and those events would bury the detections.
- Off-path detections resolve the client's country only when a detection is
  recorded, through the request's match context, so matched requests pay no
  extra GeoIP lookup.
- Retention and plan windows are ADR-964's (7 days; read window by plan).

## Amendment 6: preview gate evidence (2026-10-14)

Amendment 4 set two conditions for leaving preview: a false-positive review
and the in-path p95 under production-like load. Production traffic is too
thin for the review, so this amendment replays the CRS regression corpus
and a corpus of legitimate requests, and measures in-path latency on
`gregale-internal-test-1`. The detection events of amendment 5 make the
review possible once real traffic arrives.

### Detection: the CRS regression corpus

`TestCRSRegressionCorpus` (opt-in, `FAAS_WAF_FTW_DIR`) replays the go-ftw
corpus of CRS v4.25.0 (318 rule files). 4,621 single-stage tests replay;
53 are skipped (multi-stage, or raw and encoded requests the gateway's
HTTP server would reject first). Each test is checked two ways: did its
own rule fire (go-ftw's pass criterion), and did the request reach the
anomaly threshold of 5.

| Configuration | Attack tests | Own rule fired | Detected at 5 | Near-miss tests where the rule fired |
|---|---|---|---|---|
| sampled, PL1 | 2,056 | 99.4% | 97.9% | 2 of 539 |
| sampled, PL2 | 3,103 | 99.6% | 98.2% | 2 of 781 |
| in-path, PL1 (URI and header tests of its rule files) | 529 | 100% | 100% | 1 of 129 |

- At PL1 every attack family (913, 921, 922, 930-934, 941-944) is detected
  at 100%. Only protocol enforcement (920) is lower, 75 of 119: most 920
  rules score 2 or 3 points and need a second rule to reach the threshold.
- The 12 tests whose own rule did not fire cover CRS settings that are off
  by default (UTF-8 validation 920250, argument count and length limits
  920360-920410), a request without a Host header (920280), and an HTTP/0.9
  request line (920430). The gateway never forwards the last two.
- The near-miss failures (920181-1, 921140-1) expect Apache to rewrite or
  reject the request before CRS sees it, which the harness does not do.

### False positives: legitimate traffic

`TestBenignCorpus` runs in `make test`. It has 38 legitimate requests. 32
are ordinary traffic: browser page loads, REST calls with every method,
OAuth callbacks, Stripe and GitHub webhooks, GraphQL, form posts with
apostrophes, NDJSON, binary uploads, plain-text logs, and a body without a
Content-Type. 6 carry code-like content (a docs search for SQL, CMS rich
text). At PL1 the
test pins the exact rules that flag each request, so a CRS upgrade that
adds or removes a false positive fails the test.

CRS's defaults assume a website, and at PL1 they scored ordinary API
traffic as critical:

- every PUT, PATCH and DELETE (911100: only GET, HEAD, POST and OPTIONS are
  allowed);
- every body that is not form, multipart, XML or JSON, such as text/plain,
  octet-stream, NDJSON or `application/merge-patch+json` (920420);
- every body without a Content-Type (920340 and 920640), which is what
  Python's `requests.post(url, data=json.dumps(...))` sends;
- any multi-line body of another type without `=` (921150, CR/LF in an
  argument name). CRS parses such a body as URL-encoded so its other rules
  still see it, which makes the whole body one argument name.

The sampled engine now allows GET, HEAD, POST, OPTIONS, PUT, PATCH and
DELETE. It drops 920340, 920420 and 920640; 920470 still flags a
malformed Content-Type header. It also re-declares 921150 on query argument
names only, under the same ID, so `exclude_rule_ids` and the `rule_id`
metric label are unchanged. The in-path rule set loads none of 911 or 920
and never sees a body, so it is unchanged.

Known false positives that remain at PL1:

| Request | Sampled (headers and body) | In-path (headers and URI) |
|---|---|---|
| GraphQL query in the URL | 942190 | 942190 |
| GraphQL POST body | 932235, 942290 | — |
| Prose with SQL keywords after a semicolon | 942350 | — |
| Docs search for `SELECT * FROM users WHERE id = 1` | 942100 | 942100 |
| CMS rich text (HTML) | 941160 | — |
| SQL snippet in a JSON field | 942100 | — |

At PL2, 20 of the 38 requests are flagged. They include apostrophes in a
search (932240), any URL in a parameter such as an OAuth callback
(931130), filter syntax (942131) and a postal address (942200). The table
and its exclusions are in `docs/security.md`.

### In-path latency under load

`TestInlineLoad` (opt-in, `FAAS_WAF_INLINE_LOAD=1`) calls `CheckInline` on
one goroutine per request, as gatewayd-internal does. Requests arrive as a
Poisson process spread over 10 apps, 5% of them attacks, with the shipped
2 slots and per-app budget. It reports the times
`gateway_waf_inline_seconds` records, and process CPU per check.

The node was `gregale-internal-test-1`: 2 vCPUs (one physical core, Xeon
2.8 GHz), Go 1.26.9, GOMAXPROCS 2 unless noted, 10-20 s per rate, two
passes. Other sessions kept its load average at 3-3.6 throughout; it was
not idle as in amendment 3. Ranges cover both passes, which differed by
up to 2x.

| Run | Rate (req/s) | p50 | p95 | p99 | CPU per check | Skipped |
|---|---|---|---|---|---|---|
| near-sequential | 50 | 0.97-0.99 ms | 2.70-2.85 ms | 3.9-4.1 ms | 1.4 ms | 0-0.1% |
| default | 100 | 1.6-2.0 ms | 11.6-13.1 ms | 21-46 ms | 2.0 ms | 2.2-5.2% |
| default | 250 | 1.5-2.5 ms | 10.3-11.2 ms | 17-24 ms | 1.7-1.9 ms | 6.6-11.2% |
| default | 500 | 1.3-1.5 ms | 4.6-6.3 ms | 7.6-11.3 ms | 1.7-1.8 ms | 3.4-9.2% |
| default | 1000 | 1.3-1.8 ms | 5.1-9.9 ms | 9.1-23 ms | 1.8 ms | 18.7-46.4% |
| default | 2000 | 2.6-2.8 ms | 9.3-14.3 ms | 17.7-24 ms | 1.9 ms | 72-78% |
| one core busy in another process | 100-2000 | 3.0-4.8 ms | 14.5-18.3 ms | 25-31 ms | 1.9-2.0 ms | 5.8-82.9% |
| GOMAXPROCS=1 | 100 / 500 | 0.88 / 0.90 ms | 2.56 / 3.37 ms | 3.9 / 7.2 ms | 1.24 / 1.31 ms | 0 / 3.8% |
| GOGC=400 | 100 / 500 / 1000 | 1.42 / 1.13 / 1.27 ms | 6.48 / 4.04 / 3.75 ms | 10.9 / 6.5 / 5.6 ms | 1.5-1.7 ms | 0.5 / 1.5 / 8.6% |

No benign request was flagged in any run. Skipped checks let the request
through unchecked, attacks included: at 1000 requests per second, 273 of
495 and 411 of 498 attacks were detected.

Findings:

1. **The 2 ms p95 gate is not met on this node.** The check still costs
   about 1 ms (p50 0.88-0.99 ms when checks rarely overlap, as in amendment
   3). But p95 is 2.6-2.9 ms even at 50 requests per second, and 4.6-14 ms
   at 100-2000. The check is pure CPU, so its wall time absorbs every wait
   for a CPU: other processes, the other hyperthread, and Go's garbage
   collector.
2. **Two concurrent checks share one physical core here.** With GOMAXPROCS
   2 a check costs 1.7-2.0 ms of CPU; with 1, 1.24-1.31 ms. On a 2-vCPU
   node the two slots are hyperthread siblings.
3. **GC is a large part of the tail.** With the default GOGC the harness
   collected every 130-200 checks. GOGC=400 cut collections 4-5x, and
   p95 at 500-1000 requests per second fell to 3.75-4.0 ms. gatewayd-internal
   runs with the default GOGC under `MemoryMax=512M`. Its heap is larger
   than the harness's, so its collection rate per check has to be measured
   in the daemon itself.
4. **Fail-open starts well below amendment 4's estimate.** Skips appear at
   100 requests per second whenever tail latency keeps both slots busy, and
   reach 19-46% at 1000 (65% with a busy core). The effective ceiling on
   this node is a few hundred checked requests per second per node, not
   about 500 per app.
5. **Tenant CPU competes with the gateway as an equal.** `faas-cp.slice`
   and `faas-tenant.slice` both have the default CPUWeight, so a node with
   busy tenants behaves like the "one core busy" row.

### In-path latency inside gatewayd-internal

`TestEdgeWAFInlineLoad_E2E` (`cmd/e2e`, opt-in `FAAS_E2E_WAF_LOAD=1`)
runs apid and the real gatewayd-internal against Postgres. Each of four
preview apps has a `kind=waf` warn rule and a `kind=respond` rule on its
host, so every request is checked in-path and then answered at the edge
without waking a VM. The test reads `gateway_waf_inline_seconds` and the
check outcomes from the daemon's `/metrics`, and the daemon's CPU time
from `/proc`. Same node, 15 s per rate, variants interleaved and the
baseline repeated.

Other sessions' load rose from 4 to 13 during the run, so only 100 and 250
requests per second are usable. At 500 and above, the gateway, the load
generator, Postgres and other sessions saturated the node's 2 vCPUs, and
requests queued for seconds.

| Variant, at 100 req/s (250 req/s) | Gateway CPU per request | Check p50 | Check p95 | Skipped |
|---|---|---|---|---|
| no WAF rule | 1.13 ms (0.95) | — | — | — |
| warn, as built | 5.5-5.8 ms (5.3) | 1.8-2.3 ms (1.9-2.5) | 4.4-7.4 ms (5.1-8.9) | 0-0.1% (1.1-3.1%) |
| warn, bodiless requests not sampled | 3.0-3.1 ms (2.7-2.8) | 1.9-2.1 ms (2.5-2.7) | 4.4-5.0 ms (8.1-8.9) | 0% (0.2-0.4%) |
| not sampled, GOGC=400 and GOMEMLIMIT=384MiB | 3.05 ms (2.7) | 2.2 ms (2.5) | 5.5 ms (7.8) | 0% (0%) |
| not sampled, 1 check slot | 3.2 ms (3.0) | 2.2 ms (2.6) | 5.8 ms (8.3) | 10.2% (28.7%) |
| not sampled, a core busy at equal weight | 3.2 ms (2.7) | 2.7 ms (3.7) | 8.9 ms (10.5) | 0.1% (1.1%) |
| not sampled, a core busy at a quarter of the weight | 3.1 ms (2.8) | 2.6 ms (2.9) | 9.0 ms (9.3) | 0.1% (0.6%) |
| as built, a core busy at equal weight | 5.8 ms (4.4) | 3.8 ms (4.1) | 13.5 ms (19.5) | 1.0% (13.9%) |

No run warned on a benign request. The attacks that were not warned are in
line with the skipped fraction.

Findings:

1. **Inside the daemon a check costs about 2 ms of CPU**, not the 1 ms of
   the isolated benchmark: 3.0-3.1 ms per request with the check against
   1.0-1.1 ms without. Its median is 1.8-2.3 ms and its p95 4.4-7.4 ms at
   100 requests per second. No lever brought p95 under 2 ms on this node.
2. **As built, warn and block also sample every clean request off-path**
   with the full rule set. That costs another 2.5 ms of CPU per request,
   45% of what a warn rule costs, mostly to re-read headers the in-path
   check has just scored. With a busy core it turned 1.0% skipped checks
   into 13.9% at 250 requests per second and timed requests out; without
   it, 1.1%.

   **Changed:** a warn or block rule no longer samples a request that has
   no body when its in-path check ran clean. Requests with a body are
   still sampled, and so are requests whose check was skipped or failed.
   The cost is coverage. For bodiless requests under warn and block, the
   rule families the in-path set leaves out (911, 920, 933, 934, 943 and
   944, including Log4Shell in a header) are no longer reported. `observe`
   still runs the full rule set on every request it samples.
3. **GOGC=400 with GOMEMLIMIT=384MiB made no measurable difference** inside
   the daemon. Its heap is larger than the benchmark's, so it already
   collects less often per check. Not changed.
4. **One check slot instead of two** skipped 10-29% of checks at 100-250
   requests per second, with no latency gain. Not changed.
5. **CPU weight could not be measured here.** Giving the gateway four times
   the weight of a competing process changed little, because other
   sessions' processes, at full weight, were most of the competition. On a
   production node, `faas-cp-build.slice` (builder VMs) is a child of
   `faas-cp.slice`. Raising that slice's weight would therefore also let
   builds outrank tenants, which CLAUDE.md forbids. The lever would need a
   gateway slice of its own.

### Where an in-path check spends its time

Profiled with `BenchmarkInlinePrefilter` and `TestInlineRuleSetCost` on a
laptop (one current desktop-class core, load about 7). There the same check
takes 550-680 µs per benign header and URI request; on the test node it took
about 1 ms.

- **Regex evaluation is 56% of a check.** Coraza runs every CRS regex with
  capture tracking because almost every rule has the `capture` action, but
  matching without captures first saves only 10-15% of the regex time.
  Header and cookie parsing is 11%, variable lookups 8%, transformations 7%.
  A check makes about 1,800 allocations (106 KB).
- **By family**, removing it from the in-path set saves: 932 (RCE) 29-36%,
  942 (SQLi) 24%, 941 (XSS) 19%, 930/931 and 913/921 1-2% each. Coraza's
  per-request work is the remaining fifth.
- **Five overlapping unix command-injection rules** (932230, 932235, 932250,
  932260, 932340) cost 25% of every check. Without them the in-path set
  misses 19 of the 529 in-scope corpus attacks (3.6%). Without just the
  three largest (932230, 932250, 932260) it is 10% faster and misses 8.
- **Coraza has a regex pre-filter, off by default** (`SecRxPreFilter`). It
  skips a regex when the input lacks literals every match must contain.
  It cuts in-path checks by 19% (680 to 550 µs), header-only samples by
  17%, and 8 KiB JSON bodies by 34% (81 to 53 ms). **It is not safe in
  Coraza v3.8.1:** with it on, rule 942220 no longer matches
  `2.2250738585072011e-308` (a float-parsing DoS payload). That happens in
  all three engines, on 1 of the 4,621 corpus tests. The minimal pattern
  is `2(?:x|.2250)`: the pre-filter demands `22250` across the `.`. It
  stays off. `engineOptions` can switch it on, the corpus equivalence test
  compares the engines, and `TestRxPrefilterKnownFalseNegative` fails once
  the bug is fixed upstream.
- **The test node inflates every number.** Its 2 vCPUs are one physical
  core. Two concurrent checks cost 1.7-2.0 ms of CPU each against 1.24-1.31
  ms alone, which explains why the daemon's check costs about 2 ms. The
  2 ms gate has never been measured on dedicated cores.

Proposed, for the product owner to decide (only finding 2 is built):

1. `observe` at PL1 can leave preview: it adds no latency, it detects 98%
   of the corpus, and its false positives are known and documented.
2. `warn` and `block` stay in preview. The 2 ms target is not met on this
   shared 2-vCPU node, with or without the levers, and this node cannot
   give an uncontended number. Run `TestEdgeWAFInlineLoad_E2E` on an idle
   production compute node (n2-standard-4) before deciding.
3. Report the pre-filter false negative to Coraza with the minimal pattern.
   Once a fixed release passes `TestCRSRegressionCorpusEngineEquivalence`,
   turn the pre-filter on: 19% off every in-path check, a third off body
   inspection.
4. Consider dropping the largest unix command-injection rules from the
   in-path set: 10% faster for 8 missed corpus attacks (three rules), or
   25% for 19 (five). Since bodiless requests under warn and block are no
   longer sampled, those attacks would then go unreported, not just
   unblocked, for such requests.
5. Next experiment: memoize regex results across requests by rule and
   transformed value. User-Agent, cookie and most header values repeat
   between requests, and regex evaluation is the 56% ceiling. It needs a
   custom `@rx` operator. Loading CRS with `@rx` renamed would keep the
   stock engine alongside for the equivalence test. Its gain depends on
   real traffic's repetition, which production cannot show yet.
6. `warn` could run its check alongside the proxied request, since it only
   adds a response header; the check then hides behind the app's own
   response time. `block` cannot: it must decide before the app sees the
   request.
7. If p95 is still above 2 ms on a production node after these, restate
   the gate as p95 at most 5 ms with at most 1% skipped at the expected
   per-node rate. A check is still far cheaper than a wake, and the WAF
   fails open.
8. On that node, also try a CPUWeight for gatewayd-internal above the
   tenant slice, in a slice of its own so builder VMs keep their place
   below tenants.

## Amendment 7: `kind=challenge` as built (2026-10-15)

Built as a preview at the product owner's request, ahead of the sequencing
gate above (step 2 was to wait for `waf` beta). Section 2's mechanism
stands; this amendment fixes the details and changes one rule.

- **Action.** `difficulty` is the number of leading zero bits the browser
  must find in a SHA-256 hash: 10 to 22, default 16 (about 65,000 hashes,
  tens to a few hundred milliseconds in a browser). `pass_ttl_seconds` is
  how long a solved challenge lasts: 300 to 86,400, default 3,600. Both
  bounds live in `pkg/api/limits.go`.
- **Dry run.** A rule in log mode (ADR-960) records every request it would
  challenge and acts on none. There is no separate observe mode.
- **Placement.** After `require_authn` and `public_auth`, before `respond`
  and before any wake or admission. A request with a verified API key,
  consumer key or JWT subject bypasses the challenge. Unverified
  credentials do not: a bot could otherwise send a junk `Authorization`
  header to skip it.
- **No Accept bypass (changes section 2).** Section 2 let non-HTML `Accept`
  headers bypass the challenge. Any client can send `Accept:
  application/json` and still receive the page, so that bypass would void
  the protection. Instead a matched request without a valid pass gets:
  - for a navigation (GET or HEAD, with `Sec-Fetch-Mode: navigate` or an
    `Accept` naming `text/html`), a 403 HTML page that does the work;
  - for anything else, a 403 problem with code `challenge_required`.
  API clients are kept out by verified identity and by the rule's scope
  (host, path, methods and ADR-973 conditions), not by a header they
  choose.
- **Work.** The page carries a signed challenge token: app, random nonce,
  difficulty, pass lifetime and a five-minute expiry. The browser finds a
  counter for which `SHA-256(token + ":" + counter)` starts with
  `difficulty` zero bits, then posts the token, the counter and the page's
  path to `/.well-known/gregale-challenge` on the same host. The gateway
  checks the signature, expiry, binding, work and return path (a relative
  path on the same host), sets the pass and redirects back with 303. The
  endpoint acts only on hosts with a challenge rule; elsewhere the path
  belongs to the app. Without JavaScript the page says JavaScript is
  required.
- **Pass.** A cookie `gregale_challenge` (HttpOnly, Secure, SameSite=Lax,
  path `/`, host-only) holding the app and expiry, signed with HMAC-SHA256
  over those and the client's address prefix (/24 for IPv4, /48 for IPv6)
  and a hash of its User-Agent. One pass covers every challenge rule of
  the app on that host. The key is derived from the session key every
  gateway already shares (`FAAS_SESSION_KEY`); rotating it ends all
  passes.
- **Cost and state.** Checking a pass is one HMAC; checking a solution is
  one HMAC and one SHA-256. The gateway keeps no state. A token can be
  reused within its five minutes from the same address prefix and
  User-Agent, which only earns that client another pass.
- **Signals.** `gateway_edge_challenge_total{app,outcome}` with outcomes
  `passed`, `bypassed`, `issued`, `rejected`, `solved` and `invalid`. Both
  403 responses also count in `gateway_edge_rejections_total{kind="challenge"}`.
- **Plan gating.** Hobby and above, as section 2 proposed:
  `EdgeRulesChallengePerApp` is Free 0, Hobby 3, Pro 10, Scale 25.
- **Suggested rule.** Match the browser routes with `verified_bot missing`
  (ADR-973), so real search crawlers are never challenged.
- **Not in this step.** Security events (ADR-964) for challenge outcomes, a
  challenge section in the edge-protection summary, raising the difficulty
  under attack, and an alternative for clients without JavaScript.
