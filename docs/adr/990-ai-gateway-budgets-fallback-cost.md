# ADR-990 · AI gateway phase 2: per-app limits, fallback routes, retries, cache and cost

- **Status:** accepted
- **Date:** 2026-10-11
- **Relates to:** ADR-988 (AI gateway), ADR-989 (app outbound relay), ADR-424 (Runs outbound broker), ADR-294 (response cache), ADR-296 (retry budget)
- **Decision:** Extend the AI gateway with:
  - per-app daily token limits;
  - complete OpenAI streaming usage;
  - a replayable request body that enables POST retries, an exact-match POST cache and multi-provider fallback routes;
  - customer price tables with cost budgets;
  - Runs streaming through the app relay;
  - a `gregale ai` CLI.
- **Why:** Phase 1 accounts for tokens per integration only, loses usage on OpenAI streams that do not ask for it, sends each call to exactly one provider, and has no notion of money. Runs still buffer provider responses.
- **Consequences:** Every new behavior is opt-in per integration or route, except the OpenAI usage injection, which is invisible to callers. Costs are customer-priced and rounded up per call, so they are a spend guard rather than an invoice.
- **Rejected alternatives:**
  - A Gregale-maintained global price list would be wrong for negotiated or self-hosted pricing and would need constant upkeep.
  - Cross-format translation (OpenAI ↔ Anthropic) inside routes would be a large compatibility surface. Routes therefore require one wire format.
  - A semantic cache would need embeddings and a vector store.

## Replayable AI request body

For an AI integration, a `POST` body of at most 4 MiB (`OutboundAIRequestBufferMaxBytes`) is read once before admission and kept in memory for the call. Larger bodies stream as before and get none of the features below.

## Per-app token limits

An app binding may set `ai_daily_token_limit`. Its UTC-day counter lives on the binding usage row next to the per-binding request counter. Admission rejects the app's new calls once its settled tokens reach the limit (`429`, `daily_token_limit`). Settlement adds each call's tokens to both the integration and the binding counter. As in ADR-988, calls already in flight may finish above the limit.

## OpenAI streaming usage

For an `openai` integration, the gateway checks buffered JSON bodies with `"stream": true` sent to a path ending in `/completions`. If `stream_options.include_usage` is unset, it adds it upstream. The extra final chunk, which has empty `choices` and a non-null `usage`, is read for accounting and removed from the stream the caller sees, so client code that always reads `choices[0]` keeps working. Callers that request usage themselves receive the chunk unchanged.

## POST retries and cache

- **Retries:** an AI integration's `max_retries` and retry budget also apply to buffered `POST` calls. They follow the existing transient-failure rules. Every retry happens before any response byte reaches the caller.
- **Cache:** with `response_cache_ttl_seconds` greater than 0, buffered non-streaming JSON `POST` calls are cached when the response is a `200`. The key covers the integration, caller, policy, method, URL, forwarded headers and the exact body.
  - Streaming requests and requests marked `no-store` or `no-cache` bypass the cache.
  - A hit costs no provider tokens and is not recorded as AI usage.

## Fallback routes

An account defines an AI route: a name and 1–5 ordered targets. Each target is a managed integration, optionally with a `model` override. Every target must use the same `ai_provider`.

Callers use `/r/{route-id}/{provider path}`. This works only through the relay, whose identity is not tied to one integration. For each target in order:

- the gateway rewrites `model` when the target overrides it;
- it sends the call through the normal integration path, with that target's bindings, budgets, retries and accounting;
- it moves to the next target when the result is a gateway budget or availability rejection (`429`, or `503` from an open circuit or provider cooldown), a provider `408`, `429` or `5xx`, or a transport error.

Fallback happens only before any response byte reaches the caller. A `403` or `400` is returned as is, because it is a configuration or request error. The response carries `X-Gregale-AI-Route-Target: <integration-id>`. Routes without a buffered body use only their first target.

## Prices and cost budgets

An integration owner can store a price list:

- Each entry has a model prefix (`*` matches any model) and prices in millicents per million input, cached-input, cache-write and output tokens.
- A replacement takes effect immediately, and earlier lists are kept with their effective time.

Settlement prices each call with the entry effective at that moment. The longest matching prefix wins. Cached-input tokens are charged at the cached price instead of the input price. The cost is rounded up to whole millicents.

Rollups store `cost_millicents`. With no price list, the cost is zero.

An AI integration may set `ai_daily_cost_limit_millicents` and `ai_monthly_cost_limit_millicents` (UTC day and calendar month). Their counters share the admission-state row. Admission rejects new calls at the limit with `daily_cost_limit` or `monthly_cost_limit`.

The usage API reports cost per item, today's and this month's cost, and the limits.

## Runs streaming

When the active Run on an execution instance has outbound enabled, vmmd's relay receiver mints an execution relay assertion: the execution subject with the relay audience. `outboundd` treats such connections as that execution. Every request still goes through the ADR-424 execution authorizer, which checks the current grant and lease.

The executor then points `GREGALE_OUTBOUND_URL` at guest-init's relay instead of the buffered broker helper, so Runs get the same streaming, accounting and routes as apps. Runs without outbound enabled are refused by vmmd.

## CLI

`gregale ai` covers:

- provider, token and cost limits for an integration;
- per-app token limits;
- price lists;
- daily usage with cost;
- AI routes.
