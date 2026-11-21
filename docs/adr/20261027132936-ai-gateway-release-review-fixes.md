# ADR 20261027132936 · AI gateway: fixes from the pre-release review

- **Status:** accepted
- **Date:** 2026-10-27
- **Relates to:** ADR-990 (settlement), ADR-991 (hard caps), ADR-994 (price catalog), ADR-996 (estimated usage), ADR-998 (preview rollout), ADR-1340 (moderation), ADR-1431 (access tokens), ADR-1432 (no unmetered spend), ADR-1440 (response cache)
- **Context:** an end-to-end review of the AI gateway before its first account found:
  - **Timeouts.** A new integration got the Free plan's request policy whatever the account's plan: 30 s, 10 calls in flight, 10 per second. The timeout bounded the whole call, streamed answer included, so a long or reasoning answer was cut off. A call that outlived its admission lease (the same 30 s) stopped counting toward the caps while it ran. A call cut before its response headers recorded nothing, though the provider may still run and bill it.
  - **Unpriced models.** A model in neither the integration's price list nor the catalog cost zero. Every cost cap let it through: a model released after the catalog was generated, or any OpenAI-compatible provider's.
  - **Response cache.** Its key covered every request header, so `traceparent`, `baggage` and other per-call telemetry made every call unique. Apps with tracing, which AI traces ask for, never got a hit. A hit still paid for a moderation call.
  - **Setup.** Creating an integration, storing its key and binding it took three raw API calls. Apps had to hard-code the integration ID. The dashboard told users to run a command that does not exist.
  - **Access.** Access tokens worked for suspended accounts. Each unknown token cost a database lookup, and a flood of them cleared the cache of good ones. Captured prompts and answers needed only `apps:read`.
- **Decision:**
  - **AI call bounds.** For a billable AI call:
    - The integration's request timeout bounds the wait for the response headers, as it bounds retries, route fallback and the circuit breaker today.
    - Once the headers arrive, the call may run until `OutboundAICallMaxSeconds` (30 minutes) after it started. A streamed answer is also cut after `OutboundAIStreamIdleSeconds` (10 minutes) without a byte.
    - The response's write deadline moves to the call's end, so the daemon's write timeout cannot cut it.
    - The admission lease is renewed while the call runs: every third of max(request timeout, 15 s), to that span from now. The reservation and the in-flight slot count until settlement, and a crashed outboundd's leases still expire within that span.
    - A call whose request was sent but that ended before its headers, on the gateway's deadline or because the caller left, settles at its reservation as unknown usage (ADR-996). The request log records `504` or `499`.
    - Other calls keep the integration's timeout for the whole call.
  - **Plan defaults.** An integration created with an AI provider and no request policy gets the plan's ceilings. `gregale ai request-policy` shows and sets the policy.
  - **Unpriced models.** A billable call whose model has no price is refused with `403 outbound_ai_model_unpriced` when a cost cap applies to it:
    - The caps that apply: the integration's daily or monthly cost limit; the end-user cost limit when the call names an end user; a tag cost limit on one of its tags; a trace cost limit when it has a trace.
    - `allow_unmetered` lets such a call through outside the cost caps, as it does for other calls the gateway cannot meter (ADR-1432). Token caps still apply.
    - Settlement prices an answer whose model has no price at the requested model's price.
    - `outbound_ai_unpriced_calls_total` counts settled calls that cost zero for want of a price.
  - **Response cache.**
    - The key leaves out per-call telemetry headers: `traceparent`, `tracestate`, `baggage`, `sentry-trace`, `b3`, `x-b3-*`, `x-request-id`, `x-correlation-id`, `x-amzn-trace-id`, `x-cloud-trace-context`, `user-agent`, `idempotency-key` and `x-stainless-*`. They are still forwarded.
    - The moderation check runs only on a cache miss: a hit's request matched one already checked under the same policy revision.
  - **Setup.** `gregale add ai --app <slug> --provider <provider>` does setup in one command:
    - It creates an integration with the provider's origin and paths, or reuses one with `--integration`.
    - It stores the key from an environment variable, stdin or a file, never a flag, and binds the integration to the app.
    - It sets the app's `GREGALE_AI_BASE_URL`, and for OpenAI and Anthropic the SDK's base URL and a placeholder key, to the app's relay. `--no-env` skips this.
    - The dashboard's empty state names the command.
  - **SDK helpers.** The Node and Python helpers name a call's tags and trace and send feedback, and the docs describe them.
  - **Suspended accounts.** Calls through a customer integration of an account that is `suspended` or `deleted_pending` are refused with `403 outbound_account_suspended`.
  - **Token lookups.** Known and unknown tokens are cached apart, so unknown ones never push out known ones. At most `OutboundAIAccessTokenLookupsMax` (8) database lookups of uncached tokens run at once; past that, `503` with `Retry-After: 1`.
  - **Captured content.** Reading a captured call's bodies needs `secrets:read` or `admin`.
  - **Alerts.** Online evaluations failing, access-token lookups failing and unpriced calls get warn alerts with runbook sections.
- **Why:**
  - A hard cap that a missing price turns off is not a hard cap. Refusing until a price exists matches ADR-1432's rule for calls the gateway cannot meter.
  - The header bound keeps what customers tune the timeout for: retries, fallback to another provider, and the circuit breaker. A long answer that is flowing is not a slow provider.
  - Renewing the lease, rather than taking a 30-minute one, keeps crash recovery as quick as before.
  - Setting the SDK's own variables means an app needs no code change to use the gateway.
- **Consequences:**
  - Azure deployments whose names are not model names need a price list entry when a cost cap is set, or the calls are refused.
  - A call running longer than its renewal span adds one lease write per renewal.
  - A non-streamed answer still has to arrive within the request timeout (60 s on Hobby, 120 s on Pro, 300 s on Scale); a longer one should stream.
  - API keys with only `apps:read` can no longer read captured content.

## Amendment 1 (2026-11-04): second review

A second end-to-end review found:

- **Content through replays and evaluations.** Captured content moved to `secrets:read`, but a replay returned the captured answer and each replayed response, and an evaluation returned its samples' answer texts, at the read scope.
- **Evaluation visibility.** The request log, traces and replays withhold calls of apps the reader may no longer see; an evaluation's samples did not.
- **Realtime concurrency.** A WebSocket session held its admission lease only for the request timeout, then stopped counting toward `max_in_flight` while it ran for up to an hour.
- **Retried AI calls.** With `max_retries` set, a buffered AI `POST` was sent again after a reset or EOF that followed the request, and after `408` or `504`. The provider may have run and charged the first attempt; the gateway counted only the last.
- **Public host timeouts.** The public host for access tokens runs behind Cloudflare, which waits at most 100 s for an origin's response; non-streamed calls on Pro and Scale may wait 120 s and 300 s.
- **Relay off in production.** Apps reach the gateway only through the app relay (ADR-989), which production has not configured. Turning on the flag alone would serve no app, and nothing said so.

Decision:

1. Replays and evaluations leave out answer texts and response bodies unless the reader has `admin` or `secrets:read`, the content route's scopes, and report `content_withheld`. Evaluations drop the samples of calls whose app the reader may not see, by a batch read of the calls' callers; a sample whose call has left the log stays.
2. A realtime session renews its upgrade lease, like a long call, until it closes.
3. A billable AI call is retried only when the provider cannot have run it: the request failed before it was written, or the provider answered `425`, `429`, `502` or `503`.
4. A call through the public host waits at most `OutboundAIPublicHeaderTimeoutSeconds` (90 s) for headers, so its caller gets the gateway's `504` rather than Cloudflare's `524`. Longer answers stream.
5. `outboundd` exports `outbound_ai_gateway_enabled` and `outbound_ai_app_relay_ready` and logs a warning at startup when the flag is on without the relay. `FaasAIGatewayEnabledWithoutAppRelay` warns after 10 minutes. The operator guide lists the relay steps that must come before the flag. Enabling the relay in production stays an operator step: it needs the workload-identity key.

Consequences:

- Keys with only read scopes see replay and evaluation numbers but not answers.
- A long realtime session adds one lease write per renewal span.
- A billable call that fails after it was sent is not retried; the app sees the error.

## Amendment 2 (2026-11-12): third review

A third review found eight issues. #6826 fixed five of them (long-context tiers, one-hour cache writes, the Claude output ceiling of translated calls, blocking moderation failing closed and CSV formula cells) and documented `outbound_stage_not_allowed`. This amendment covers the rest:

- **Non-production scopes.** The gateway serves production workloads only, yet `gregale add ai --scope <other>` wrote base URLs and credentials for a scope whose calls the gateway refuses.
- **Account throughput.** Rates applied per integration; an account could multiply its share of the gateway, and of its database writes, by opening integrations.
- **Budget alerts** reached only the app's webhooks (`outbound.ai_budget.threshold`). An account without a webhook receiver learned of a spent budget when calls started failing.

Decision:

1. `gregale add ai` refuses a non-production `--scope` unless `--no-env`, and says to give that scope its own provider key.
2. Each account has an in-process AI call rate across its customer integrations, `OutboundAIAccountRatePerSecondMax` by plan (Free 20, Hobby 40, Pro 200, Scale 1000; burst twice that), counted after the caller is authorized so nobody else can spend it. Excess calls get `429 outbound_ai_account_rate_limited` with `Retry-After: 1`. The gateway's own checks and probes are exempt.
3. Alert rules gain the app-scoped, notification-only metric `ai_budget_alerts`: the count of AI budget threshold crossings (the 80% and 100% rows `outbound_ai_budget_alerts` already records, for every budget kind) within the window, of the integrations bound to the rule's app, leaving out other apps' own budgets. It takes the route alert windows (`5m` to `24h`), needs the `ai-gateway` capability, and a firing notification lists up to five crossings with integration, budget, use and end user or tag. A Hobby preset (`gt 0` over 15 minutes) adds it. Rules on it fire through notification channels like any other alert rule, so email, Slack and the other channels reach people who never read webhooks. The webhook stays.

Consequences:

- The account rate is per `outboundd` process; with several gateway nodes an account may reach that many times the rate.
- `ai_budget_alerts` is evaluated on meterd's alert cadence, so a channel hears of a crossing up to one evaluation after the webhook.
- An integration budget's crossing is counted by the rule of every app bound to the integration.

## Amendment 3 (2026-11-13): visible and customer-set price tiers

The long-context tiers and the one-hour cache-write rule of #6826 applied to the built-in catalog only, and no API showed them: a customer could not see why a long call cost more, and an integration's own price list could express neither, so long prompts were billed flat against its caps.

- **Decision:**
  - A price entry gains `cache_write_1h_millicents_per_mtok` and `long_context` (`above_input_tokens`, `input_percent`, `output_percent`), both optional, in the catalog response and in customer price lists.
  - The catalog fills them from what billing charges: each model's long-context tier, and for Claude models the one-hour write price, the higher of the cache-write price and twice the input price.
  - A customer list entry's tier and one-hour write price are stored (`outbound_ai_price_entries`, nullable columns with CHECKs) and applied like the catalog's. An entry without them keeps today's behaviour: no tier, and the one-hour rule above.
  - Bounds live in `pkg/api/limits.go`: thresholds 1,000 to 2,000,000 input tokens, percentages 100 to 1,000.
- **Consequences:** price lists are immutable versions, so a list set before this keeps billing as before; `gregale ai catalog` and `gregale ai prices` show and set the new fields.

