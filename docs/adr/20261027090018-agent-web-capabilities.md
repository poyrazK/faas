# ADR-20261027090018 · Agent web capabilities: better fetch, bring-your-own web-data providers and egress, and web monitors

- **Status:** proposed (owner decision D19, 2026-10-19).
- **Date:** 2026-10-19
- **Amends (when accepted):** ADR-20261027090014 §4.
  - §4.2 gains richer fetch results and a per-account cache.
  - Its search note and open question 2 are answered: bring-your-own providers only.
  - §4.3 and §4.4 gain a contract-only bring-your-own egress proxy.
  - §4.7 gains limits.
- **Builds on:**
  - ADR-20261027090016 (capability gateway: web tools become MCP tools there);
  - ADR-249 and ADR-253 (platform-held and customer-sealed credentials);
  - ADR-372 (dedicated egress gateway);
  - ADR-488, ADR-574 and ADR-575 (automations), the cron limits, and ADR-489 (outbound workflow steps).
- **Decision:**
  1. **Better results.** `web.fetch` returns clean Markdown with provenance (final URL, fetch time, content hash) and converts PDF and Office documents. `web.screenshot` renders a page through the tier 2 browser.
  2. **A per-account fetch cache** respects `Cache-Control` within bounds. It is never shared across accounts.
  3. **Bring-your-own web-data providers.**
     - A seller can register search, scrape or crawl APIs as `web_provider` integrations.
     - `web.search` and `web.fetch` route through them by policy, for example as a fallback when a site blocks Gregale's egress.
     - Gregale itself offers no search API in this release.
  4. **A bring-your-own egress proxy, by contract only.** For sellers who need their own IP reputation or geolocation:
     - tier 2 browser traffic, and tier 1 direct fetches, may leave through the seller's proxy;
     - Gregale's destination rules still apply first.
  5. **Web monitors.** A template watches pages or searches on a schedule and delivers changes. It runs with no model at all when nothing changed.
- **Why:**
  - **Agents need the live web, and the hard part is access.** Rate limits, bot checks and blocks are what keep agents from the live web. Companies now sell exactly that access as an API.
  - **Gregale must not run that arms race.** ADR-20261027090014 rejected address rotation as evasion, and the AUP forbids CAPTCHA bypass. Unmanaged agent traffic from Gregale's own addresses is what led to the 2026-09-24 suspension.
  - **The answer:** let sellers plug in the access providers they choose, outside the VM and under Gregale's policy and budgets. Gregale makes its own direct fetch good and polite.
  - **Change monitoring is a common job.** "Tell me when this changes" (prices, docs, competitors, filings) is a frequent agent use. It is mostly fetch and diff, so it should not pay for a model call when nothing changed.
- **Consequences:**
  - outboundd gains converters, the cache, provider routing and normalization.
  - The egress gateway gains a seller-proxy chain mode.
  - The automation catalog gains the monitor template.
- **Rejected alternatives:**
  - **A Gregale-run proxy network, CAPTCHA solving, or address rotation:** evasion, legal exposure, and egress reputation risk (ADR-20261027090014 §4.4, §4.5).
  - **Gregale reselling a search or scraping API:** that is not compute (the pricing principle), and it carries third-party terms that sellers can hold directly.
  - **Rendering JavaScript pages outside the VM in a Gregale browser pool, in this release:** it duplicates the tier 2 browser, and every render would leave from shared Gregale addresses. Reconsider after measuring.

## Decision

### 1. `web.fetch` results

The result is `{url, final_url, status, fetched_at, content_type, content_hash, title, markdown | text, links?, truncated}`.

- **HTML:** readability extraction to Markdown, keeping headings, lists, tables and links. Scripts and styles are dropped.
- **PDF, DOCX, XLSX and PPTX:** text extraction with page or sheet markers. It runs in outboundd's converter sandbox: a separate process with no network, CPU, memory and time limits, and a seccomp profile.
- **Provenance:** every result carries `final_url`, `fetched_at` and `content_hash`, so agents can cite and sellers can verify.
- **Unchanged from ADR-20261027090014 §4.2:** GET only, no credentials, robots.txt, per-domain politeness, the identifying user agent, message signatures and logging.

`web.screenshot {url, viewport}` is a tier 2 tool. It runs the run's own browser preset, so it is available only where `AgentBrowserAllowed` is on.

### 2. The per-account cache

- **Key:** URL, plus `Accept` and `Accept-Language`. The cache belongs to one account and is never shared.
- **TTL:** follows `Cache-Control` and `Expires`, clamped to `AgentWebCacheTTLSec` (minimum 60 s, maximum 24 h). `no-store` is honored.
- **Storage:** object storage, counted against the plan's included storage (ADR-1350). A result reports `cache: hit | miss`.
- **Cost:** a cache hit costs no fetch from the site and counts once against `AgentWebFetchesPerDay`.

### 3. Bring-your-own web-data providers

- **Integration kind.** `web_provider` is an outbound integration with a fixed origin and a customer-sealed key (ADR-253). It comes with presets for common search, scrape and crawl APIs, and a generic adapter.
- **Routing policy,** per agent version (ADR-20261027090016 §3):

| `web.search` | `web.fetch` |
|---|---|
| `provider:<integration>` (required: Gregale has no search of its own) | `direct` (the default: Gregale's polite fetch), `provider:<integration>`, or `direct_then_provider`, which uses the provider after a 403, 429 or block page |

- **Normalization.** Provider results map to the `web.fetch` and `web.search` result shapes, with `source: provider:<name>`, so harnesses see one schema.
- **Crawl.** `web.crawl {url, limits}` exists only through a provider. Gregale's direct path has no bulk crawler (ADR-20261027090014 §4.5).
- **Budgets and audit.** Provider calls count against ADR-20261027090016 budgets and appear in the audit. The provider bills the seller directly.
- **Compliance.** The seller is responsible for the provider's terms and for their own use of what it returns. Gregale's AUP still applies to the agent's behavior.

### 4. Bring-your-own egress proxy (contract only)

- **Who.** Sellers on a contract, with an AUP addendum.
- **Proxy.** They register an HTTP(S) proxy as a customer-sealed credential (`egress_proxy`).
- **Path.** For their tier 2 browser runs, and tier 1 direct fetches when chosen:
  1. the agent egress gateway (ADR-20261027090014 §4.4) first applies the ADR-361 deny ranges, the mail-port block and the complaint blocklist;
  2. it then chains the connection to the seller's proxy, rather than leaving from Gregale's address pool.
- **What stays:** logging (run, tenant, destination, bytes) and the politeness caps.
- **What it is not:** Gregale does not choose, rotate or operate the proxy's addresses. The traffic is the seller's, under their own provider's terms.
- **Failure.** If the proxy fails, the connection fails closed. It never falls back to Gregale's addresses.

### 5. Web monitors

- **Template.** `gregale agent create --template web-monitor` creates an automation (ADR-488) with:
  - a cron trigger, within the per-plan cron limits;
  - targets: URLs, searches or provider queries;
  - a change rule: content hash, CSS-selected region, or numeric field;
  - a delivery: webhook, Slack, email or PR comment.
- **No model when nothing changed.**
  1. The first step is an ADR-489 outbound workflow step. It fetches through `web.fetch` or the provider and compares the result with the last state, kept in object storage.
  2. Only when something changed does an agent step run (optional), to summarize or judge the change.
  3. The delivery then fires.
- **Quiet runs.** Unchanged checks cost one fetch and no VM.
- **Limits.** Monitors count against cron limits and `AgentWebFetchesPerDay`.

### 6. Security

- **The converter sandbox** parses hostile documents with no network and tight resource limits, and outside vmmd, the only root daemon.
- **Taint.** Every web result, direct or provider, is untrusted for taint (ADR-20261027090016 §4).
- **Cache isolation.** Cache entries never cross accounts, so the cache cannot be used as an oracle across tenants.
- **Seller proxies** cannot bypass Gregale's deny ranges, mail block or blocklist, because those are applied before the chain.

## Limits (`pkg/api/limits.go`, proposed; Free is 0)

| Name | Hobby / Pro / Scale |
|---|---|
| `AgentWebCacheTTLSec` | 60–86,400 (clamp) |
| `AgentWebFetchMaxBytes` | 5 MB / 20 MB / 50 MB |
| `AgentWebConvertTimeoutMs` | 10,000 |
| `AgentWebProvidersPerAccount` | 2 / 10 / 25 |
| `AgentWebMonitorsPerAccount` | 10 / 100 / 1,000 |
| `AgentEgressProxyAllowed` | no / no / by contract |

The ADR-20261027090014 §4.7 limits are unchanged.

## Metrics (§12 additions)

- `agent_web_fetch_total{source,cache,outcome}`
- `agent_web_convert_seconds{kind}`
- `agent_web_provider_calls_total{provider,outcome}`
- `agent_web_monitor_checks_total{changed}`
- `agent_egress_proxy_conns_total{outcome}`

## Rollout

All steps stay on local branches, behind the internal `agents` capability. Web access for customers still waits for ADR-20261027090014's egress gateway, AUP and an owner test on production.

1. This ADR, after ADR-20261027090016.
2. Fetch results with HTML-to-Markdown and provenance, then the converter sandbox for PDF and Office documents.
3. The per-account cache.
4. `web_provider` integrations, routing, normalization, and the first presets.
5. The web-monitor template and the no-model check step.
6. `web.screenshot` with the tier 2 browser.
7. The seller egress-proxy chain, for contract accounts only.

## Open questions

1. Which search, scrape and crawl providers get first-class presets.
2. A Gregale-run render pool for JavaScript pages, decided after measuring tier 2 cost.
3. Whether monitors may run more often than the cron minimum on Scale.
