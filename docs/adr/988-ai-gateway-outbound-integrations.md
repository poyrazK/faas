# ADR-988 · AI gateway on managed outbound integrations

- **Status:** accepted (phase 1)
- **Date:** 2026-10-11
- **Relates to:** ADR-249 (platform-held provider authorization), ADR-250 (workload identity), ADR-253 (customer-sealed credentials), ADR-257 (daily request budgets), ADR-294 (response cache), ADR-295 (circuit breaker), ADR-424 (Runs outbound broker)
- **Decision:** Build the AI gateway as an optional LLM-aware mode of an existing outbound integration, not as a new daemon. An integration may declare `ai_provider` (`openai` for the OpenAI-compatible wire format, or `anthropic`). For those integrations `outboundd` streams event-stream responses as they arrive, extracts the response model and token usage from JSON and server-sent-event bodies without holding them back, records UTC-day usage per caller and model, and enforces an optional daily token limit at admission.
- **Why:** `outboundd` already owns what an AI gateway needs at the edge of a workload: keys that never reach the app, workload identity, fixed-origin public dialing, route policy, rate, concurrency and request budgets, retries, a circuit breaker and provider cooldowns. ADR-257 left token counts and dollar spend to provider adapters. Those adapters, plus streaming, are the missing AI-specific layer.
- **Consequences:** Phase 1 serves bring-your-own-key integrations. No new listener, credential path or network route is added, and integrations without `ai_provider` keep their existing behavior except that every event-stream response is now flushed promptly.
- **Rejected alternatives:** A separate AI gateway daemon would duplicate credential custody, identity verification and admission state. Inferring the provider from the origin host would misread OpenAI-compatible providers and self-hosted endpoints, and token accounting must not depend on a hostname. Estimating tokens from request or response sizes is not reliable enough to enforce a budget.

## Usage extraction

The adapter observes the bytes already being copied to the caller. It never
delays, buffers or rewrites the response stream.

- **JSON responses** are buffered for parsing only up to 1 MiB. Larger bodies
  are delivered unchanged and recorded with unknown usage.
- **Event streams** are parsed one event at a time. An event whose data exceeds
  256 KiB is skipped.
- **OpenAI-compatible:** the chat completions and Responses APIs report
  `prompt_tokens`/`input_tokens`, `completion_tokens`/`output_tokens` and the
  cached-token details. Streaming chat completions report usage only when the
  caller sets `stream_options.include_usage`. Otherwise the request is counted
  with unknown usage.
- **Anthropic:** the Messages API reports input tokens excluding cache reads and
  writes. Gregale normalizes input tokens to include both, and keeps the cache
  read and cache write counts separately. Streaming uses `message_start` and the
  cumulative `message_delta` usage.
- **Model names** are bounded to 128 printable characters. One integration
  records at most 32 distinct models per UTC day; further models aggregate under
  `other`. This stops a customer-chosen origin from inflating the usage table.

Only `POST` calls are recorded. Non-2xx responses count as errors without tokens.

## Budgets and records

- `ai_daily_token_limit` counts normalized input plus output tokens. The counter
  shares the integration's admission-state row and its UTC-day reset with the
  daily request counter.
- Tokens are known only after a response. Admission therefore rejects new calls
  once the day's settled total reaches the limit (`429`, reason
  `daily_token_limit`, `Retry-After` to the next UTC midnight). Calls already in
  flight may exceed it by at most their own usage. This is a spend guard, not an
  exact cap.
- Each settled call adds its tokens to the counter and upserts one UTC-day row
  per integration, caller (app ID or `runs`) and model. Rows hold request,
  error and unknown-usage counts plus the four token counts. `outboundd`
  deletes rows older than 90 days.
- The dependency span gains OpenTelemetry GenAI attributes (`gen_ai.system`,
  `gen_ai.response.model`, `gen_ai.usage.*`) and a time-to-first-byte attribute.
- Prometheus metrics keep the existing bounded integration label. They never
  carry model names.

## Streaming

`text/event-stream` and `application/x-ndjson` responses are flushed after every
upstream read for all integrations. Their write deadline is extended to the
admitted request deadline, so the daemon's general write timeout does not cut
off a long generation. The plan's outbound request-timeout ceiling still bounds
every call.

## Deferred

These are not part of phase 1 and need their own decisions:

- A default network path from app VMs to `outboundd` and a drop-in loopback
  base URL for stock SDKs. Today an operator routes a guest-reachable origin,
  and Runs use the buffered ADR-424 broker.
- Per-binding token limits.
- Price tables and money budgets.
- Model routes with cross-provider fallback.
- An exact-match `POST` cache and retries before the first response byte.
- Injecting `stream_options.include_usage`.
- A public endpoint for callers outside Gregale.
- Platform-paid provider keys.
