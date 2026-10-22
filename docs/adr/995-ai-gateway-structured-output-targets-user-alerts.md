# ADR-995 · AI gateway phase 5: structured output, Bedrock and Vertex route targets, embeddings routes, end-user alerts and export, app helpers

- **Status:** accepted
- **Date:** 2026-10-12
- **Relates to:** ADR-991 (translation, alerts), ADR-994 (end users, key affinity), ADR-988 (AI gateway)
- **Decision:** Close the gaps phase 4 left in translation and per-end-user budgets:
  - translate OpenAI `response_format` to each target's structured output instead of dropping it;
  - let openai-format routes target Bedrock (Converse) and Vertex AI (Claude and Gemini);
  - let openai-format routes accept OpenAI embeddings requests;
  - alert at 80% and 100% of each end user's daily budget, and export per-end-user usage for a day range;
  - give the Node and Python SDKs helpers that build relay URLs and the end-user and session headers.

  It also fixes route attempts double-escaping an escaped provider path.
- **Why:**
  - ADR-991 dropped `response_format` for translated targets. A caller asking for JSON got free text from Anthropic or Gemini with a 200, so its parser failed far from the cause.
  - Customers on AWS or Google Cloud could use Bedrock and Vertex only on native routes, so they could not fall back across providers.
  - Embeddings had no route, so retrieval traffic could not use a route at all.
  - ADR-994 limits end users but tells nobody when one approaches a limit. Apps that bill their own users had to page the users endpoint one day and at most 1,000 rows at a time.
  - Apps had to hand-build `$GREGALE_OUTBOUND_URL/r/{id}` and the headers, and often sent raw e-mail addresses as user IDs.
  - A native route to a Bedrock ARN model (`%2F` in the path) reached the provider escaped twice and failed.
- **Consequences:**
  - A request whose `response_format` a target cannot express is refused (`400 outbound_ai_translation_unsupported`) instead of being sent without it. The problem `detail` now says why.
  - An end user's alerts can reach 40,000 rows per integration per day: 10,000 users × 2 budgets × 2 thresholds. They are kept 31 days like the per-user rollup, not 90 days like integration alerts.
  - Falling back between embeddings models of different providers returns vectors from a different space. The docs require one model per embeddings route; `X-Gregale-AI-Route-Target` shows which target served.
- **Rejected alternatives:**
  - **Emulate `json_object` for Anthropic and Bedrock** with a forced tool call or prefill. Claude Opus 5.5, Sonnet 5.5 and Fable 5.1 reject forced `tool_choice` and assistant prefill with a 400. A prompt-only instruction does not guarantee JSON. Refusing is honest; `json_schema` works everywhere.
  - **Strip schema keywords a provider does not support.** That silently changes the contract the caller asked for. The provider's 400 names the keyword.
  - **Bedrock invoke for Claude models and Converse for the rest.** Converse serves every Bedrock chat model, including structured output, with one translator.
  - **Vertex AI's OpenAI-compatible endpoint for Gemini.** It needs `google/`-prefixed model names that the price catalog does not use, and it would bypass the tested Gemini translator.
  - **A separate route format for embeddings.** The provider path already says which API is called; one openai format keeps route setup simple.
  - **Notify every bound app of an end user's alert.** End users belong to the app that names them; only the calling app is notified.
  - **Raise the users page size for export.** A streamed, keyset-paged download does not hold a month of rows in apid or the client.

## Structured output

`response_format` is read for every translated target:

| `response_format` | Anthropic, Claude on Vertex | Gemini, Gemini on Vertex | Bedrock Converse |
| --- | --- | --- | --- |
| absent or `text` | unchanged | unchanged | unchanged |
| `json_object` | refused | `generationConfig.responseMimeType: application/json` | refused |
| `json_schema` | `output_config.format: {type: json_schema, schema}` | `responseMimeType` plus `responseJsonSchema: schema` | `outputConfig.textFormat` `json_schema`, with the schema as a JSON string under `structure.jsonSchema` and its `name` and `description` |

OpenAI and Azure OpenAI targets receive the field unchanged. A `json_schema` without a `schema` object, or an unknown type, is refused. Schemas pass through unaltered; each provider supports its own subset of JSON Schema.

## Bedrock targets

A Bedrock target's model is a Bedrock model ID, inference profile or ARN. The route sends `POST /model/{id}/converse`, or `/converse-stream` for streams. outboundd still signs the call with SigV4 (ADR-991).

**Request:** messages become Converse `messages`:
- system and developer text becomes `system`;
- tool calls become `toolUse`, tool results `toolResult`;
- `data:` images in PNG, JPEG, GIF or WebP become `image` blocks;
- tools become `toolConfig`, with `auto`, `required` and named choices mapped to `auto`, `any` and `tool`.

Converse has no "none" choice: `tool_choice: "none"` leaves tools out, which is refused once the conversation has tool calls. The output ceiling, temperature, `top_p` and stop sequences go to `inferenceConfig`.

**Response:** a JSON response becomes a `chat.completion`. Prompt tokens include cache reads and writes, matching accounting. The binary `converse-stream` event stream is decoded and re-emitted as `chat.completion.chunk` SSE:
- text and tool-input deltas become chunks;
- `messageStop` sets the finish reason;
- `metadata` carries usage;
- an exception ends the stream with an OpenAI error event.

Bedrock's `{"message": ...}` errors get the OpenAI error envelope.

## Vertex AI targets

A Vertex integration's allowed path prefixes must name exactly one `/v1/projects/{project}/locations/{location}` (or `v1beta1`). Route creation refuses an openai route whose Vertex target does not. The model picks the publisher: an `anthropic/` or `google/` prefix names it, otherwise a `claude` model is Anthropic's and anything else Google's.
- **Claude:** `…/publishers/anthropic/models/{model}:rawPredict` (`:streamRawPredict` for streams). The Anthropic translation is used without `model` and with `anthropic_version: vertex-2023-10-16`.
- **Gemini:** `…/publishers/google/models/{model}:generateContent` (`:streamGenerateContent?alt=sse`), with the Gemini translation.

## Embeddings routes

openai-format routes also accept `POST …/embeddings`, with `input` as a string or an array of strings.

| Target | Request |
| --- | --- |
| OpenAI | unchanged |
| Azure OpenAI | `/openai/v1/embeddings` with the deployment as `model` |
| Gemini | `/v1beta/models/{model}:batchEmbedContents`, one request per input; `dimensions` becomes `outputDimensionality` |
| Vertex AI (Google models) | `…/publishers/google/models/{model}:predict` with one instance per input; `dimensions` becomes `parameters.outputDimensionality` |
| Anthropic, Bedrock | refused |

Responses become an OpenAI embeddings list. Float values pass through as the provider wrote them. With `encoding_format: base64`, which the OpenAI SDKs request by default, vectors are encoded as little-endian float32s, as OpenAI does. Token arrays as input stay OpenAI-only.

Vertex `predict` responses report `statistics.token_count` per input. Accounting now reads it, so Vertex embeddings count toward token budgets and cost. Gemini's `batchEmbedContents` reports no usage, so those calls settle as `usage_unknown`.

## End-user alerts

When per-end-user limits are set, settlement compares the end user's UTC-day totals before and after the call against each limit. Budgets `user_daily_tokens` and `user_daily_cost` alert once per end user, day and threshold (80%, 100%):
- `outbound_ai_budget_alerts.end_user` names the user, and the once-per-period key includes it;
- the `~other` overflow row has no per-user limits and never alerts.

The `outbound.ai_budget.threshold` webhook carries `end_user` and goes to the calling app only; Runs have no app to notify. `GET …/ai-alerts` and `gregale ai alerts` show the end user. These alerts are pruned after 31 days.

## Usage export

`GET /v1/outbound/integrations/{id}/ai-usage/users/export?from=&to=&format=` streams every per-user row of an inclusive range of UTC days within the 31-day retention:
- **Formats:** NDJSON by default, or CSV with the columns `usage_date, end_user, requests, error_requests, input_tokens, output_tokens, tokens, cost_millicents`.
- **Order:** rows are ordered by day and end user.
- **Paging:** apid reads them in keyset pages of 2,000, so no range is held in memory.

The CLI command is `gregale ai users export <id> --from D --to D [--format csv|ndjson]`; it writes CSV to stdout.

## App helpers

The Node (`@gregale/sdk-node`) and Python (`faas_sdk`) SDKs export:
- `aiGatewayURL` / `ai_gateway_url`: `$GREGALE_OUTBOUND_URL/i/{integration}` or `/r/{route}`;
- `aiGatewayHeaders` / `ai_gateway_headers`: the end-user and session headers, validated like the gateway;
- `opaqueAIUserId` / `opaque_ai_user_id`: a stable `u_`-prefixed SHA-256 or HMAC-SHA-256 ID for raw identifiers such as e-mail addresses;
- Node only, `withAIGatewayHeaders`: a fetch wrapper for client libraries that take a `fetch`.

## Route path escaping

A route attempt keeps the caller's escaped provider path, or the path translation built, as the escaped form, and sets the unescaped path from it. A Bedrock ARN model's `%2F` now reaches the provider once-escaped, on native and translated routes alike.
