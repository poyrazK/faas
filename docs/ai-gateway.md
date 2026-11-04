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
- **Deploys.** See each deployment's AI calls, errors, flagged answers and
  cost per call with `gregale ai deployments`, and roll back a release whose
  AI calls get worse with an `ai` release gate.
- **Dashboard.** See each integration's cost by model, app, tag and end user,
  its latency, error rate, quality and drift alerts, and every agent trace
  as a timeline of its calls, at `/dashboard/ai`.
- **Quality signals.** Every call returns its ID in `X-Gregale-AI-Call` and is
  marked when its answer stopped at the token limit, was refused or filtered,
  was empty, or broke the JSON it was asked for. Apps rate calls by ID, a judge
  model can score a sample of them, and you can be alerted when a model's
  quality drifts from its last week.
- **AI traces.** Group an agent's calls into one task with
  `X-Gregale-AI-Trace` or your existing `traceparent`, see each task's calls,
  cost and requested tools in order, and stop a task that makes too many
  calls, costs too much or asks for the same tool call again and again.
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
- **Realtime voice.** OpenAI and Azure OpenAI Realtime sessions, and Gemini
  and Vertex AI Live sessions, run through the gateway over WebSocket. Each
  response or model turn is checked against your caps and settled from its
  usage, with audio tokens at audio prices.
- **No unmetered spend.** Calls the gateway cannot meter, such as realtime
  client secrets, Live ephemeral tokens, vector stores or fine-tuning, are
  refused unless you allow them for the integration.

## Set up

One command sets up an app:

```sh
OPENAI_API_KEY=sk-... gregale add ai --app my-api --provider openai
```

It creates an AI integration for the provider (or reuses the one of the same
name), stores the key, binds the integration to the app and sets the app's
`OPENAI_BASE_URL` and a placeholder `OPENAI_API_KEY`, so the official SDK
calls the gateway with no code change. Anthropic gets `ANTHROPIC_BASE_URL`
and `ANTHROPIC_API_KEY` the same way; every provider gets
`GREGALE_AI_BASE_URL`. The app sees the settings on its next deploy or
restart.

- The key is read from the provider's usual variable (`OPENAI_API_KEY`,
  `ANTHROPIC_API_KEY`, `GEMINI_API_KEY`, `AZURE_OPENAI_API_KEY`, the `AWS_`
  variables for Bedrock), `--key-env`, `--key-stdin` or `--key-file`; never a
  flag value.
- Azure OpenAI takes `--origin https://<resource>.openai.azure.com`; Bedrock
  and Vertex take `--region`; Vertex takes its service-account key with
  `--key-file`.
- `--no-env` leaves the app's settings alone.

To do the same through the API:

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

### Timeouts

The integration's request timeout bounds the wait for a response's headers,
and it is the plan's ceiling for a new AI integration: 60 s on Hobby, 120 s
on Pro and 300 s on Scale. Once a streamed answer starts, it may run for up
to 30 minutes, as long as the provider sends something at least every 10
minutes. A non-streamed answer has to arrive within the timeout, so stream
long answers. `gregale ai request-policy <id>` shows and sets the timeout,
rate and concurrency.

A call the provider received but never answered, because the timeout ran out
or your app went away, counts toward your caps at its reservation: the
provider may still bill it.

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

Send `X-Gregale-AI-Trace: <task-id>` on every call of one agent task (or just
your usual `traceparent`) to see the task as one trace, and limit each task:

```sh
gregale ai traces <id>
gregale ai traces <id> <trace-id>
gregale ai policy <id> --trace-max-calls 50 --trace-max-cost 2 --trace-max-repeated-tool-calls 3
```

Each response carries `X-Gregale-AI-Call: <call-id>`. With the request log on,
your app can rate the call through the gateway, the same way it calls the
model:

```sh
curl -X POST "$GREGALE_OUTBOUND_URL/i/$INTEGRATION_ID/_gregale/feedback" \
  -H 'Content-Type: application/json' \
  -d '{"call_id": "<call-id>", "rating": "bad", "label": "wrong_answer"}'
```

Score a sample of calls with a judge model of your choice, and get
`outbound.ai_quality.drift` webhooks when a model's quality drifts:

```sh
gregale ai policy <id> --request-log-content on --online-eval-percent 5 \
  --online-eval-judge <openai-id> --online-eval-model gpt-5-mini --quality-alerts on
gregale ai usage <id>            # QUALITY and SCORE columns
gregale ai quality-alerts <id>
```

### SDK helpers

Gregale's SDKs build the gateway URL and headers for you, and send
feedback:

```ts
import OpenAI from 'openai';
import { aiGatewayURL, aiGatewayHeaders, aiGatewayFeedback } from '@gregale/sdk-node/preview';

const openai = new OpenAI({ baseURL: `${aiGatewayURL({ integration: id })}/v1`, apiKey: 'unused' });
const { data, response } = await openai.chat.completions
  .create({ model: 'gpt-5-mini', messages }, { headers: aiGatewayHeaders({ user, tags: { feature: 'search' }, trace: taskId }) })
  .withResponse();
await aiGatewayFeedback({ integration: id, callId: response.headers.get('x-gregale-ai-call')!, rating: 'good' });
```

```python
from faas_sdk.preview import ai_gateway_url, ai_gateway_headers, ai_gateway_feedback

client = OpenAI(base_url=ai_gateway_url(integration=id) + "/v1", api_key="unused")
headers = ai_gateway_headers(user=user, tags={"feature": "search"}, trace=task_id)
ai_gateway_feedback(integration=id, call_id=call_id, rating="bad", label="wrong_answer")
```

`opaqueAIUserId` / `opaque_ai_user_id` turns an e-mail address into an
end-user ID that is not personal data.

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

### By deployment

Each call from one of your app's instances counts toward the deployment that
instance runs (ADR 20261022153155). List the last 14 days by deployment:

```bash
gregale ai deployments --app my-api
```

```
DEPLOYMENT                            LAST CALL             CALLS  ERRORS  FLAGGED  COST           PER CALL       MEAN TTFB
6f1c0e1e-6c1b-4c7a-9c8e-2f0b7c1d9a10  2026-10-22T15:41:00Z  4210   0.40%   1.10%    USD 4.21000    USD 0.00100    380
2b9d4c55-1f0e-4a3b-8d6e-5a7c9e0b1f22  2026-10-21T09:12:00Z  9877   0.35%   1.05%    USD 7.90160    USD 0.00080    365
```

Calls through an access token, a Run, a replay or the response cache have no
deployment and are not counted. An `ai` release gate stops a canary, or rolls
back a release, whose error rate, flagged rate, cost per call or time to first
byte rises past a bound over the previous release's:

```bash
gregale release-gates add --app my-api --name ai-cost --ai cost_per_call --max-increase 30
```

### In the dashboard

`/dashboard/ai` lists your AI integrations with today's calls, cost, error
rate and quality. Open one to see a UTC day by model and app, the most
expensive tags and end users, latency and errors by model, drift alerts and
recent traces. Its **Traces** page lists agent tasks, newest first; open one
to see its calls on a timeline with their models, tokens, cost, requested
tools, quality flags, ratings and scores.

Latency and the trace timeline come from the request log, so turn it on with
`gregale ai policy <id> --request-log on`.

## Limits and errors

| Response | Meaning |
| --- | --- |
| `403 outbound_ai_model_unpriced` | A cost cap applies to the call and its model has no price, so the cap could not count it. Add the model to the integration's price list (`gregale ai prices`), or let such calls through outside the cost caps with `--allow-unmetered on`. |
| `429` with `X-Gregale-Outbound-Rejection` | A cap or rate limit was reached; `Retry-After` says when it resets. Trace limits (`trace_call_limit`, `trace_cost_limit`, `trace_tool_loop`) never reset: start a new trace. |
| `404 outbound_ai_call_not_found` | Feedback named a call that is not in the integration's request log, or that another app made. |
| `400 outbound_ai_trace_invalid` | `X-Gregale-AI-Trace` is empty, longer than 64 characters or has a character outside `A-Z a-z 0-9 . _ : @ + = / \| -`. |
| `400 outbound_ai_content_blocked` | The content policy blocked the request. The problem names the detector, never the content. |
| `402 plan_feature_gated` | From the API: your plan does not include the AI gateway. |
| `403 outbound_ai_gateway_unavailable` | From the gateway: the AI gateway is not enabled for your account yet, or your plan does not include it. |
| `503 outbound_ai_gateway_disabled` | The AI gateway is turned off on the platform. |
| `403 outbound_account_suspended` | The account is suspended or being deleted. |
| `403 outbound_ai_model_not_allowed` | The app's guardrails do not allow this model. |
| `400 outbound_ai_max_tokens_exceeded` | The request asks for more output tokens than the app's guardrails allow. |
| `413 outbound_ai_request_too_large` | The body is larger than the gateway can check or sign (4 MiB), for a guarded app or a Bedrock integration. |
| `400 outbound_ai_user_invalid`, `400 outbound_ai_tags_invalid` | `X-Gregale-AI-User`, `X-Gregale-AI-Session` or `X-Gregale-AI-Tags` is malformed. |
| `422 outbound_ai_response_blocked` | A response guardrail blocked the answer. The problem names the detector, never the content. |
| `422 outbound_ai_response_flagged` | The response moderation check found a blocked category. |
| `502 outbound_ai_response_unchecked` | A response guardrail could not check the answer. |
| `400 outbound_ai_feedback_invalid`, `403 outbound_ai_feedback_not_authorized` | Feedback was malformed, or did not come from an app. |
| `400 outbound_ai_translation_unsupported` | A route cannot translate this request for its target. |
| `400 outbound_ai_content_flagged` | The moderation check found a blocked category. `X-Gregale-AI-Moderation` names it. |
| `503 outbound_ai_moderation_unavailable` | The moderation check failed and its policy blocks on failure. |
| `401 outbound_access_token_invalid` | The access token is missing, unknown, revoked or expired. |
| `403 outbound_ai_endpoint_unmetered` | The gateway cannot meter this call against your caps. `gregale ai policy <id> --allow-unmetered on` lets such calls through, outside the caps. |
| `400 outbound_ai_batch_unreadable` | The gateway could not read the batch's requests to size them against your caps. |
| `413 outbound_ai_batch_too_large` | The batch is larger than the gateway can size. |
| `403 outbound_ai_realtime_policy_unenforceable` | The integration's content policy checks requests or responses, which a realtime session's audio cannot be checked against. |
| Realtime `error` event, then close code `1008` | A cap refused the session's next response (`outbound_budget_exhausted`), the session reached its one-hour limit, or the provider sent a message the gateway could not read. |

A model with no price in the integration's list or the built-in catalog
costs nothing in usage reports; under a cost cap it is refused (above).
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
  traffic you may store. Reading it needs an API key with `secrets:read`.
- The response cache keys a call by its body and headers, leaving out
  per-call telemetry such as `traceparent`, `baggage` and `user-agent`.
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
