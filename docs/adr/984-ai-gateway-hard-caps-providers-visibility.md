# ADR-984 · AI gateway phase 3: hard caps, per-app guardrails, more providers, translation, key pools and visibility

- **Status:** accepted
- **Date:** 2026-10-11
- **Relates to:** ADR-979 (AI gateway), ADR-982 (app outbound relay), ADR-983 (phase 2), ADR-849 (consumer usage alerts), ADR-294 (response cache)
- **Decision:** Extend the AI gateway with:
  - admission-time reservations, so token and cost limits become hard caps;
  - per-app requests-per-minute and tokens-per-minute limits;
  - per-app model allow-lists and a per-request output-token ceiling;
  - four more providers: `gemini`, `azure_openai`, `bedrock` and `vertex`;
  - OpenAI-format routes that translate to Anthropic and Gemini targets;
  - pools of extra provider keys with per-key cooldown;
  - budget alerts at 80% and 100%, an opt-in per-request metadata log, and a Grafana dashboard.
- **Why:** Phase 2 admits a call while settled usage is under a limit, so concurrent or long calls can overshoot it. An app sharing an integration can burn its budget in one burst or call any model. Customers on Google, Azure or AWS cannot use the gateway at all. Fallback cannot cross providers. One key's rate limit throttles the whole integration. Customers learn about a spent budget only when calls start failing.
- **Consequences:**
  - Admission now rejects a call whose reservation does not fit, even when settled usage is below the limit. A caller that asks for a large `max_tokens` near the end of a budget is refused until the reset.
  - Bedrock and Vertex credentials are richer than one header value (an AWS key pair, a Google service-account key). They are still sealed exactly like other managed credentials, and only outboundd opens them.
  - Translation covers the common chat-completions surface, not every provider feature (see below).
- **Rejected alternatives:**
  - **Clamp `max_tokens` to the remaining budget instead of rejecting.** This changes the caller's request silently and still overshoots on input tokens.
  - **Count reservations in the settled counters and refund them later.** A crashed gateway would leak reservations into the day's spend. Lease rows already expire, so reservations live on the lease.
  - **Translate every provider into every other provider.** The useful direction is OpenAI chat completions in, other providers out, because nearly every client library speaks it.
  - **Share per-key cooldowns across replicas.** Keys rotate on the first 429, so a per-process cooldown is enough. The integration-wide shared cooldown still applies when every key is cooling.
  - **A Gregale-provided model key.** This needs its own pricing and billing decision.

## Release gate

The AI gateway from ADR-979, ADR-983 and this ADR ships behind one operator switch: `ai_gateway_enabled` in `apid.toml` and `outboundd.toml`. It is off by default. `FAAS_AI_GATEWAY_ENABLED` overrides it; `1` turns it on and any other value turns it off. The `ai-gateway` product capability names the switch.

While the gate is off:

- **apid** answers every AI gateway endpoint with `403 outbound_ai_gateway_unavailable`, and refuses to create an integration with an `ai_provider`. That covers the AI policy, usage, prices, per-app budget and guardrails, routes, key pools, alerts and the request log.
- **outboundd:**
  - refuses calls to an integration that has an `ai_provider` (`503 outbound_ai_gateway_disabled`), rather than serving them without budgets or accounting;
  - answers `/r/` routes with `404`;
  - uses only the primary credential;
  - refuses to start when an operator integration in its configuration declares an `ai_provider`.

The app relay (ADR-982) and integrations without an `ai_provider` are unaffected. Turning the gate off later stops AI traffic at once. Stored settings, usage and alerts stay and apply again when it is turned back on.

## Reservations (hard caps)

For an AI call, the handler estimates a reservation before admission:

- **Input tokens:** `ceil(body bytes / 4)` for a buffered body; for an unbuffered body, the declared length, or the 4 MiB buffer bound when no length is given.
- **Output tokens:** the request's own output ceiling (`max_tokens`, `max_completion_tokens`, `max_output_tokens`, `generationConfig.maxOutputTokens` or `inferenceConfig.maxTokens`). When there is none, the app's `ai_max_output_tokens` is used, else `OutboundAIDefaultOutputReserveTokens` (4096).
- **Cost:** the reservation priced from the current price list. The input estimate is charged at the input price and the output reserve at the output price. A model without a price reserves no cost.

Admission stores the reservation on the call's lease row, along with the binding app. A limit rejects the call when:

- settled usage has already reached it; or
- settled usage, plus the reservations of live leases, plus this reservation, is above it.

The reject reasons stay `daily_token_limit`, `daily_cost_limit` and `monthly_cost_limit`. For the app token limit, only leases of the same app count.

Settlement records actual usage before the lease is released, so a call is never missing from both totals at once. An expired lease drops its reservation with it. The cap is therefore exact for the gateway's own estimate: the only possible overshoot comes from a provider exceeding the requested output ceiling, or from input above four bytes per token.

## Per-app per-minute limits

An app binding may set `ai_requests_per_minute` and `ai_tokens_per_minute`. Both use fixed UTC-minute windows on the binding usage row.

- **Requests per minute** counts every admitted call through the binding.
- **Tokens per minute** counts tokens settled in the window, plus the app's in-flight reservations, plus this call's reservation.

A rejected call gets `429` with reason `requests_per_minute_limit` or `tokens_per_minute_limit`, and `Retry-After` set to the end of the minute. Tokens count toward the minute in which they settle.

## Per-app guardrails

An app binding may set:

- **`ai_allowed_models`:** 1–32 entries, each an exact model name or a prefix ending in `*`.
- **`ai_max_output_tokens`:** 1 to 1,000,000.

How the model is found depends on the format:

| Format | Model source |
| --- | --- |
| OpenAI, Anthropic | the body `model` |
| Azure OpenAI deployment paths | the deployment name |
| Gemini and Vertex | the `models/{model}` path segment |
| Bedrock | the `model/{modelId}` path segment |

Guardrails are checked before admission:

- A model that is not allowed, or cannot be determined while an allow-list is set, gets `403 outbound_ai_model_not_allowed`.
- An explicit output ceiling above the limit gets `400 outbound_ai_max_tokens_exceeded`.
- When the request has no ceiling, the gateway adds one in the format's field:

  | Format | Field |
  | --- | --- |
  | OpenAI chat | `max_completion_tokens` |
  | OpenAI Responses | `max_output_tokens` |
  | Legacy OpenAI completions, Anthropic, Bedrock `invoke` for Anthropic models | `max_tokens` |
  | Gemini and Vertex Gemini | `generationConfig.maxOutputTokens` |
  | Bedrock Converse | `inferenceConfig.maxTokens` |

- A guarded app's body must be buffered (at most 4 MiB). A larger body, or a Bedrock `invoke` body for a model family whose ceiling field is unknown, is refused rather than sent unchecked.

## Providers

`ai_provider` adds four values. Each picks the credential handling and the usage format.

- **`gemini` (Google AI Studio):**
  - Sends the key in `x-goog-api-key`.
  - Usage comes from `usageMetadata`. Thinking tokens count as output, and `cachedContentTokenCount` counts as cached input.
- **`azure_openai`:**
  - Sends the key in `api-key`. A stored value starting with `Bearer ` (a Microsoft Entra token) is sent as `Authorization` instead.
  - Uses the OpenAI wire and usage format.
- **`bedrock`:**
  - The sealed value is `aws:<access-key-id>:<secret-access-key>[:<session-token>]`.
  - outboundd signs each call with AWS Signature Version 4. The service is `bedrock`; the region comes from the `bedrock-runtime[-fips].<region>.amazonaws.com` origin. Caller `Authorization` and `X-Amz-*` headers are dropped first.
  - Signing needs the body hash, so Bedrock calls must be buffered (at most 4 MiB).
  - Usage comes from:
    - the Converse `usage` object;
    - the `X-Amzn-Bedrock-Input-Token-Count` and `X-Amzn-Bedrock-Output-Token-Count` headers of `invoke`;
    - the `metadata` and `amazon-bedrock-invocationMetrics` events of the binary AWS event stream that `converse-stream` and `invoke-with-response-stream` return.
- **`vertex`:**
  - The sealed value is the service-account key JSON on one line, or a literal `Bearer` access token.
  - For a key, outboundd signs an RS256 JWT, exchanges it at `https://oauth2.googleapis.com/token`, and keeps the access token in memory until five minutes before it expires. Any other `token_uri` is refused.
  - The usage format follows the path:
    - `publishers/google` → Gemini;
    - `publishers/anthropic` → Anthropic;
    - `endpoints/openapi` → OpenAI.

Usage normalization is chosen per call from the provider and path, because Anthropic and the OpenAI Responses API use the same field names with different cache semantics.

## OpenAI-format routes

A route gains `format`:

- **`native`** (the default) keeps ADR-983: every target uses one provider.
- **`openai`** routes accept OpenAI chat completions (a buffered JSON body to a path ending in `/chat/completions`). Their targets may be `openai`, `azure_openai`, `anthropic` or `gemini`. Every non-`openai` target must name its model.

Per target:

- **`openai`:** the caller's path, unchanged.
- **`azure_openai`:** `/openai/v1/chat/completions`, with the deployment as `model`.
- **`anthropic`:** translated to `/v1/messages`.
  - Request:
    - system and developer messages become `system`;
    - image URLs and data URLs become image blocks;
    - tool definitions, tool calls and tool results map to `tools`, `tool_use` and `tool_result`;
    - `tool_choice` maps to `auto`, `none`, `any` or `tool`;
    - `stop` becomes `stop_sequences`, and a missing output ceiling becomes 4096.
  - Response: JSON and SSE are translated back into `chat.completion` and `chat.completion.chunk` objects, with finish reasons, tool-call deltas and usage. Cached input is reported in `prompt_tokens_details.cached_tokens`.
- **`gemini`:** translated to `/v1beta/models/{model}:generateContent`, or to `:streamGenerateContent?alt=sse` for streams. Contents, `systemInstruction`, function declarations, `functionCall` and `functionResponse` parts, `toolConfig` and `generationConfig` are mapped, and responses are translated back the same way.

Translation limits:

- `n` above 1 is refused with `400 outbound_ai_translation_unsupported`.
- Fields with no equivalent, such as `logprobs`, `seed` and `response_format`, are dropped.
- Provider errors are wrapped in the OpenAI `error` envelope.
- A usage chunk is emitted only when the caller asked for one.

Accounting always reads the provider's native bytes.

## Key pools

A managed customer-sealed integration may hold up to 8 extra sealed keys next to its primary credential. They share every policy and budget.

- outboundd picks keys round-robin, skipping keys in local cooldown.
- A `429` puts that key in cooldown for its `Retry-After`, or 30 seconds when there is none, capped at one hour.
- The call is then retried at once with the next available key, when the request is replayable and retries remain.
- The integration-wide provider cooldown is recorded only when no other key is available.
- Keys are write-only: the API returns IDs and labels, never values.

## Budget alerts

Settlement compares each limited budget before and after the call:

- the integration's daily tokens;
- the app's daily tokens;
- the daily cost;
- the monthly cost.

The first settlement that reaches 80% or 100% of a limit within its period records one row in `outbound_ai_budget_alerts`; a unique key prevents duplicates.

For each recorded alert, the same transaction enqueues an `outbound.ai_budget.threshold` app webhook:

- an app budget notifies that app's subscribed webhooks;
- an integration-wide budget notifies the subscribed webhooks of every bound app.

The payload carries a stable `alert_id`. Alerts are listed at `GET /v1/outbound/integrations/{id}/ai-alerts`, counted in `outbound_ai_budget_alerts_total`, and pruned with the usage rollups after 90 days.

## Request log

With `request_log_enabled` in the AI policy, every settled AI call writes one row. The row holds:

- the caller, route, model and status;
- the usage outcome, the four token counts and the cost;
- the duration, time to first byte, attempts and retries.

Prompts, completions and headers are never stored. Cache hits are not settled, so they are not logged. Rows are kept for 7 days (`OutboundAIRequestLogRetentionDays`) and listed newest first at `GET /v1/outbound/integrations/{id}/ai-requests`.

## Visibility

`deploy/grafana/ai-gateway.json` charts the following from the outbound metrics:

- AI requests by outcome, tokens by kind, and cost;
- time to first byte;
- route fallbacks and translations;
- budget rejections and alerts;
- key cooldowns.

This repository has no web console, so console pages are out of scope.
