# ADR-1340 · AI gateway phase 8: model canary routes, request-log export and moderation guardrails

- **Status:** accepted
- **Date:** 2026-10-15
- **Relates to:** ADR-990 (fallback routes), ADR-991 (request log), ADR-997 (content guardrails, replay), ADR-1120 (settlement round trips); customer runtime log drains (#1585)
- **Decision:**
  - **Model canaries on routes.** A route can name one target, other than its first, as a canary that receives a percentage of the route's calls first.
    - The rest of the route's order is unchanged, so a failing canary call still falls back.
    - While the canary runs, every settled route attempt adds to a per-minute row for its target.
    - outboundd compares the canary with the first target over the canary's window and rolls it back when:
      - its error rate exceeds the first target's by more than the configured percentage points; or
      - its mean time to first byte exceeds the first target's by more than the configured percentage.
    - A customer can promote a canary (it becomes the first target) or stop it.
  - **Request-log export to the app's log drains.** An AI integration can turn on log export.
    - Each settled call of an app then adds one JSON line to a Postgres outbox, `outbound_ai_log_exports`, in settlement's first batch.
    - The gateway that already delivers the app's runtime-log drains (#1585) reads the outbox and enqueues each line in every enabled drain's durable sender, as source `ai-gateway` with the row ID as its sequence.
    - OTLP drains also receive the call's OpenTelemetry GenAI attributes.
    - The line holds metadata only: model, status, tokens, cost, timings, route, canary and moderation results. It never holds prompts or completions.
  - **Moderation guardrails.** An AI integration's content policy can add a moderation check.
    - The check runs through another of the account's own AI integrations, in process, after admission and before the provider call:
      - `openai_moderation` calls OpenAI's `/v1/moderations`;
      - `classifier` asks a chat model, through the route translators, which categories apply.
    - Categories are `prompt_injection`, `harassment`, `hate`, `self_harm`, `sexual`, `violence` and `illicit`. `openai_moderation` cannot detect `prompt_injection`.
    - The action is `flag`, which lets the call through and names the categories in a header, or `block`, which refuses it with `400 outbound_ai_content_flagged`.
    - `on_error` decides what a failed check does: `allow` or `block`.
    - The moderation call is metered and capped like any other call of its integration, under the caller `moderation`.
- **Why:**
  - **Canaries.** Changing a model is the riskiest change an AI app makes, and Gregale already has every piece needed to make it safe: ordered routes, translation across providers, replay of captured calls, and release-style canaries for deployments.
    - Peers offer A/B splits (Cloudflare) or confidence-based escalation (Vercel), but none ties the split to an automatic rollback.
  - **Export.** AI request logs never left Gregale. Customers who keep their logs in Datadog, Grafana or any OTLP collector had to poll the request-log API.
    - Cloudflare exports gateway logs to the customer's storage; Gregale's drains already deliver runtime logs durably to both kinds of endpoint.
  - **Moderation.** The ADR-997 detectors match patterns: e-mail addresses, cards, IBANs, secrets and terms. They cannot recognise a prompt injection or abusive text.
    - Cloudflare, OpenRouter and Portkey all offer model-based checks.
    - Running the check through the customer's own integration keeps Gregale out of model hosting and billing, and the check inherits that integration's caps.
- **Consequences:**
  - **Settlement.** A settlement adds at most two statements to batches it already sends:
    - the export insert, in the first batch;
    - the canary row, last in the second batch.
    `TestAIGatewayRoundTripBudget` still holds at 11 and 12 round trips per call. A route without a canary writes nothing new.
  - **Canary contention.** All calls of one canary target in one minute update one row. That row is locked after the admission-state row and only in settlement's second batch, which keeps ADR-1120's lock order.
  - **Canary decisions are statistical.** A canary is judged only after `min_calls` settled attempts. Gateway rejections (caps, open breakers, cooldowns) are never settled, so they do not count against the canary.
  - **Export delay.** A line is exported once its row is at least `OutboundAILogExportVisibilitySeconds` (15 s) old.
    - The cursor is the row ID, and IDs are assigned before commit. Waiting longer than a settlement attempt can live means a lower ID cannot become visible after a higher one was read.
    - On a new gateway (an ownership handoff), the cursor starts one reconcile interval back, so a handoff can repeat or skip that interval's lines, as it can for runtime logs.
  - **Moderation cost and latency.** The check adds one provider round trip before the call and is billed by the moderation integration's provider.
    - Only the request is checked, not the response: streamed output cannot be held back without losing streaming.
    - At most `OutboundAIModerationTextMaxBytes` of user, tool and input text are checked, taking the latest text first.
    - Moderation and replay calls are never moderated.
    - A classifier model reads the text it judges, so a crafted input can mislead it too. The check lowers the risk; it does not remove it.
  - **New tables:** `outbound_ai_route_canaries`, `outbound_ai_route_target_minutes` and `outbound_ai_log_exports`. All three are operational and are not cloned.
  - **Retention.** outboundd's hourly prune deletes canary rows older than `OutboundAIRouteStatsRetentionHours` and outbox rows older than `OutboundAILogExportRetentionHours`.
- **Rejected alternatives:**
  - **Canaries.**
    - **Weights on every target.** A full weighted split makes fallback order ambiguous and has no baseline to compare with. One canary against the first target answers the question customers ask: is the new model as good as the current one?
    - **Judge canaries from the request log.** The log is opt-in and keeps one row per call. A per-minute row per target needs no opt-in and stays small.
    - **Record target rows for every route.** Routes without a canary would pay a contended upsert on every call for data nobody reads.
  - **Export.**
    - **outboundd delivers to drains itself.** It would need the drains' auth-header key and its own durable queues on the control plane. The gateways already own both, so delivery keeps one owner.
    - **One outbox row per drain.** Every drain of an app gets the same line; each drain's sender keeps its own cursor and dedupes by sequence.
  - **Moderation.**
    - **Moderate responses.** Holding a streamed response until it is checked breaks streaming; checking afterwards cannot stop what was already sent.
    - **A Gregale-hosted moderation model.** That needs model hosting and pricing decisions. The customer's own integration works today.
    - **Moderate before admission.** A call refused by its caps would still pay for the check.

## Canary rules

- A canary names a target position from 2 to 5 and a percentage from 1 to `OutboundAIRouteCanaryPercentMax` (50).
- A call goes to the canary first when a hash of the route and the call's `X-Gregale-AI-Session` (or else `X-Gregale-AI-User`) falls under the percentage. A call with neither header is chosen at random.
- The response names the chosen target in `X-Gregale-AI-Route-Target`, and `X-Gregale-AI-Route-Canary: 1` marks a canary call.
- An attempt is an error when its settlement outcome is `error`: any provider status of 400 or above. A model that rejects requests the current model accepts is exactly what a canary should catch.
- Latency is the mean time to first byte of successful attempts. A canary with `max_latency_increase_percent` unset is judged on errors only.
- A rollback keeps the canary's configuration and records the reason and time. The canary then receives no calls until it is replaced, promoted or stopped.
- Replacing a route's targets stops its canary and clears its rows.

## Export line

```json
{"source":"ai_gateway","integration_id":"…","model":"gpt-5","status":200,"outcome":"known",
 "input_tokens":812,"cached_input_tokens":0,"cache_write_tokens":0,"output_tokens":164,
 "cost_millicents":91,"duration_ms":1840,"ttfb_ms":420,"attempts":1,
 "route_id":"…","route_target":2,"canary":true,"end_user":"u_123","moderation":"passed"}
```

Optional fields are omitted when empty. OTLP drains also receive `gen_ai.system`, `gen_ai.response.model`, `gen_ai.usage.input_tokens` and `gen_ai.usage.output_tokens` as attributes.
