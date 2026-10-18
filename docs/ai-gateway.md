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
- **Fallback routes.** Try targets in order across providers. An
  OpenAI-format route translates chat completions, the Responses API and
  embeddings for Anthropic, Gemini, Bedrock and Vertex.
- **Key pools.** Extra provider keys with per-key cooldown after a 429.
- **Visibility.** Usage and cost by app, model and end user, budget alerts at
  80% and 100%, an opt-in request log, opt-in capture of request and response
  bodies, and replay of a captured call against other models.
- **Media.** Image generation, transcription and speech are metered and capped
  like chat.

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
gregale ai usage <id>
```

Name the end user of a call with the `X-Gregale-AI-User` header (an opaque ID,
not an e-mail address) to get per-user usage and limits.

## Limits and errors

| Response | Meaning |
| --- | --- |
| `429` with `X-Gregale-Outbound-Rejection` | A cap or rate limit was reached; `Retry-After` says when it resets. |
| `400 outbound_ai_content_blocked` | The content policy blocked the request. The problem names the detector, never the content. |
| `402 plan_feature_gated` | Your plan does not include the AI gateway. |
| `403 outbound_ai_gateway_unavailable` | The AI gateway is not enabled for your account yet. |
| `400 outbound_ai_translation_unsupported` | A route cannot translate this request for its target. |

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
- Feedback and incidents: contact support with the integration ID and request
  ID from `gregale ai logs`.
