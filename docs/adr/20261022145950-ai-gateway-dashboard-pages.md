# ADR 20261022145950 · AI gateway: dashboard pages

- **Status:** accepted
- **Date:** 2026-10-22
- **Relates to:** ADR-011 (server-rendered dashboard), ADR-982 (apid domain packages), ADR-991 (request log), ADR-1436 (usage tags), ADR 20261020202035 (AI traces), ADR 20261021171712 (quality signals)
- **Context:**
  - The AI gateway reports usage, traces and quality signals through the API and the CLI only.
  - A customer debugging an agent wants to see a task's calls on one screen. One working out why a bill grew wants cost by model, tag and end user side by side.
- **Decision:**
  - **Four account-scoped dashboard pages**, served by a new `pkg/apid/aiconsole` package through a host interface that apidserver implements, as `automationconsole` does:

    | Page | Shows |
    | --- | --- |
    | `/dashboard/ai` | The account's AI integrations, each with today's calls, error rate, cost, quality flags and mean score. |
    | `/dashboard/ai/{integration}` | One UTC day: cost and quality by model and by caller, the most expensive tags and end users, latency and error rate by model, recent drift alerts and traces. |
    | `/dashboard/ai/{integration}/traces` | The trace explorer: traces newest first, filtered by app or end user, with older pages. |
    | `/dashboard/ai/{integration}/traces/{trace}` | One trace as a timeline: each logged call as a bar from its start to its end, with its time to first byte, model, tokens, cost, requested tools, quality flags, rating and score. |

  - **Latency and error rate** come from the request log.
    - `GetOutboundAICallStats` takes an integration's latest `OutboundAICallStatsCallsMax` logged calls from the last `OutboundAICallStatsWindowHours` hours.
    - For each model it returns calls, errors, and the p50 and p95 time to first byte and duration.
    - The cap bounds the read on busy integrations; the page says how many calls it used.
  - **Every other figure** comes from reads the API already serves: daily usage, tag and end-user usage, traces and quality alerts.
  - **Gating:** the pages follow the AI gateway's gate: the platform flag, the account's rollout and the plan. A gated account sees why instead of the page.
  - **Rendering:** the pages are plain `html/template` with no script. The timeline is an SVG whose bars are attributes, so the pages keep the dashboard's content security policy.
- **Why:**
  - Each page answers a question the API answers in several calls: what did this task do, what does this integration cost and why, and is it getting worse.
  - **A bounded sample of recent calls** is enough to see latency and errors move. Exact percentiles over every call would need a new rollup that settlement writes on every call.
- **Consequences:**
  - The pages read the request log, so latency and the trace timeline need it on. Without it, each says so instead of showing empty charts.
  - The latency read uses the request log's existing index on integration and time, and reads at most `OutboundAICallStatsCallsMax` rows.
  - No writes: settings stay in the API and the CLI.
- **Rejected alternatives:**
  - **A client-side chart library.** The dashboard has no build chain and its CSP allows no inline scripts (ADR-011).
  - **A latency rollup at settlement.** It adds a statement to every call for a page that a sample serves.
