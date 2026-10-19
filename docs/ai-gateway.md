# AI gateway

The AI gateway lets your apps call LLM providers through a managed outbound
integration. Your app uses the provider's own SDK with a Gregale base URL; the
gateway holds the provider key, enforces token and cost caps before a call
reaches the provider, and records usage and cost per app, model and end user.

The AI gateway is in **preview** on Hobby, Pro and Scale. It is enabled per
account during the preview; contact support to have it turned on for yours.
`GET /v1/capabilities` shows whether it is available to your account.

## What you get

- **Providers.** OpenAI (and any OpenAI-compatible API), Anthropic, Google
  Gemini, Azure OpenAI, AWS Bedrock and Google Vertex AI.
- **Hard caps.** Daily token limits, daily and monthly cost limits, per-app
  token limits and per-end-user limits. A call is refused before it reaches the
  provider when its reservation would pass a cap.
- **Guardrails.** Per-app requests and tokens per minute, model allow-lists and
  output-token ceilings.
- **Content guardrails.** Redact or block e-mail addresses, payment card
  numbers, IBANs and API keys or tokens before a request leaves Gregale, and
  block requests containing your own terms.
- **Response guardrails.** Apply the same detectors, your terms and the
  moderation check to the model's answer before your app sees it, streamed
  answers included.
- **Moderation.** Check requests for prompt injection, harassment, hate,
  self-harm, sexual content, violence or illicit instructions with OpenAI's
  moderation endpoint or any chat model you choose, then flag or block them.
- **Fallback routes.** Try targets in order across providers. An
  OpenAI-format route translates chat completions, the Responses API and
  embeddings for Anthropic, Gemini, Bedrock and Vertex.
- **Route strategies.** Split a route's calls by weight, or send each call
  first to the target with the lowest recent latency or cost that is up.
- **Model canaries.** Send a share of a route's calls to a new model first and
  compare errors, latency and cost with the current one. Gregale rolls the
  canary back on its own when the new model does worse; promote it when it
  does not.
- **Evaluations.** Before a canary, replay your latest real calls on the new
  model and let a judge model compare its answers with the current ones. A
  passing evaluation can start the canary for you.
- **Access tokens.** Call the gateway from your laptop, CI or a notebook as one
  of your apps, with the same caps and usage, using an expiring token in place
  of the provider key.
- **Batches.** OpenAI, Azure OpenAI and Anthropic batch jobs are capped before
  they are created and settled at the batch price when they finish.
- **Key pools.** Extra provider keys with per-key cooldown after a 429.
- **Automatic prompt caching.** For Claude on Anthropic, Vertex and Bedrock,
  the gateway marks repeated system prompts and tool lists and continuing
  conversations for the provider's cache, so repeated input is billed at the
  cache-read price.
- **Response cache.** Identical calls, streamed or not, can be answered from
  the gateway's cache. Usage shows cache hits and what the response cache and
  prompt caching saved.
- **Visibility.** Usage and cost by app, model and end user, budget alerts at
  80% and 100%, an opt-in request log, opt-in capture of request and response
  bodies, and replay of a captured call against other models.
- **Usage tags.** Name up to five `key=value` tags per call, such as
  `feature=search` or `tier=free`, to see and export cost by your own
  dimensions, and cap one tag's daily cost with alerts at 80% and 100%.
- **Log export.** Send each call's metadata to your app's log drains, HTTP or
  OpenTelemetry, as one JSON line. Prompts and completions are never
  exported.
- **Media.** Image generation, transcription, speech and Veo video generation
  are metered and capped like chat.
- **Tool fees.** Web search, file search, code-interpreter containers and
  Gemini search grounding are counted, priced and capped like tokens.
- **Realtime voice.** OpenAI and Azure OpenAI Realtime sessions run through
  the gateway over WebSocket. Each response is checked against your caps and
  settled from its usage, with audio tokens at audio prices.
- **No unmetered spend.** Calls the gateway cannot meter, such as realtime
  client secrets, Gemini Live, vector stores or fine-tuning, are refused unless
  you allow them for the integration.

## Set up

1. Create an integration for the provider, with the AI provider set
   (`POST /v1/outbound/integrations`):

   ```json
   {
     "name": "llm",
     "origin": "https://api.openai.com",
     "allowed_methods": ["POST"],
     "allowed_path_prefixes": ["/v1"],
     "ai_provider": "openai",
     "ai_daily_token_limit": 2000000
   }
   ```

2. Store the provider key (`PUT /v1/outbound/integrations/{id}/credential`
   with `{"authorization": "Bearer sk-..."}`). Gregale seals it; it is never
   returned.
3. Bind the integration to your app
   (`PUT /v1/apps/{slug}/outbound-bindings/{id}`).
4. Point the SDK at the integration from inside the app. The key value is a
   placeholder; the gateway replaces it:

   ```sh
   OPENAI_BASE_URL="$GREGALE_OUTBOUND_URL/i/$INTEGRATION_ID/v1"   OPENAI_API_KEY=unused
   ANTHROPIC_BASE_URL="$GREGALE_OUTBOUND_URL/i/$INTEGRATION_ID"   ANTHROPIC_API_KEY=unused
   ```

Then set limits and policies with the `gregale ai` commands:

```sh
gregale ai policy <id> --daily-tokens 2000000 --daily-cost 25 --request-log on
gregale ai policy <id> --user-daily-tokens 200000      # per end user (X-Gregale-AI-User)
gregale ai guardrails <id> --app my-api --rpm 60 --max-output-tokens 2048
gregale ai content-policy <id> --email redact --secrets block
gregale ai content-policy <id> --response-email redact --response-secrets block --response-terms block
gregale ai content-policy <id> --moderation block --moderation-kind classifier \
  --moderation-integration <check-id> --moderation-model gpt-5-nano --moderation-categories prompt_injection
gregale ai policy <id> --log-export on                  # to the app's log drains
gregale ai policy <id> --prompt-cache on                # cache repeated Claude prompts
gregale ai routes set <route-id> --target <a> --target <b> --strategy weighted --weights 70,30
gregale ai routes set <route-id> --target <a> --target <b> --strategy cost --max-ttfb-ms 1500
gregale ai routes canary <route-id> --position 2 --percent 10
gregale ai usage <id>
```

Name the end user of a call with the `X-Gregale-AI-User` header (an opaque ID,
not an e-mail address) to get per-user usage and limits.

Tag a call with `X-Gregale-AI-Tags: feature=search,tier=free` to see its
usage by tag, and cap one tag's daily cost:

```sh
gregale ai tags <id> --key feature
gregale ai tag-limits <id> --set feature=search:20     # USD per UTC day
gregale ai tags export <id> --from 2026-10-01 --to 2026-10-31 > ai-tags.csv
```

### Outside Gregale

To call the gateway from your laptop, CI or a notebook as one of your apps,
create an access token and use it as the SDK's API key:

```sh
gregale ai tokens create --app my-api --name laptop --expires-in-hours 24
OPENAI_BASE_URL="https://ai.gregale.dev/i/$INTEGRATION_ID/v1"   OPENAI_API_KEY="$GREGALE_AI_TOKEN"
```

The token is shown once. Revoke it with `gregale ai tokens revoke --app my-api
<token-id>`.

### Before a canary

```sh
gregale ai evaluate <route-id> --position 2 --judge <id> --judge-model gpt-5 --canary-percent 10
```

This replays up to 50 of your latest captured calls (turn on
`--request-log-content` for the route's first integration) and starts the
canary only if the new model does not do worse.

## Limits and errors

| Response | Meaning |
| --- | --- |
| `429` with `X-Gregale-Outbound-Rejection` | A cap or rate limit was reached; `Retry-After` says when it resets. |
| `400 outbound_ai_content_blocked` | The content policy blocked the request. The problem names the detector, never the content. |
| `402 plan_feature_gated` | Your plan does not include the AI gateway. |
| `403 outbound_ai_gateway_unavailable` | The AI gateway is not enabled for your account yet. |
| `400 outbound_ai_translation_unsupported` | A route cannot translate this request for its target. |
| `400 outbound_ai_content_flagged` | The moderation check found a blocked category. `X-Gregale-AI-Moderation` names it. |
| `503 outbound_ai_moderation_unavailable` | The moderation check failed and its policy blocks on failure. |
| `401 outbound_access_token_invalid` | The access token is missing, unknown, revoked or expired. |
| `403 outbound_ai_endpoint_unmetered` | The gateway cannot meter this call against your caps. `gregale ai policy <id> --allow-unmetered on` lets such calls through, outside the caps. |
| `400 outbound_ai_batch_unreadable` | The gateway could not read the batch's requests to size them against your caps. |
| `413 outbound_ai_batch_too_large` | The batch is larger than the gateway can size. |
| `403 outbound_ai_realtime_policy_unenforceable` | The integration's content policy checks requests or responses, which a realtime session's audio cannot be checked against. |
| Realtime `error` event, then close code `1008` | A cap refused the session's next response (`outbound_budget_exhausted`), the session reached its one-hour limit, or the provider sent a message the gateway could not read. |

Request bodies a content policy must inspect are limited to 4 MiB. Usage,
alerts, the request log and captures each have their own retention; see the
[operator guide](ops/outbound-integrations.md) for the full reference.

## Plans

The AI gateway is included on **Hobby**, **Pro** and **Scale**. Your plan's
outbound request policy (rate, burst and concurrency per integration) applies
to AI calls too. Provider charges are billed by your provider to your own key;
Gregale's cost figures are estimates from your price list or the built-in
catalog, used for budgets and reports.

## Preview notes

- Calls for one integration share its admission state, so very high call rates
  are better spread across integrations.
- Captured content is redacted and kept for 3 days; turn capture on only for
  traffic you may store.
- A moderation check costs one call of the check integration before each
  checked call; the provider bills it to that integration's key.
- Evaluation calls (the new model and the judge) are billed by your providers
  too, and count toward your caps.
- OpenAI batch input files are not checked by the content policy, and
  batches are not moderated.
- Free tool allowances, such as Gemini's free search grounding, are charged
  like the rest, so costs can read high for low-volume use.
- Feedback and incidents: contact support with the integration ID and request
  ID from `gregale ai logs`.
