# ADR-996 · AI gateway phase 6: Responses API routes, forced tool choice on current Claude models, hard-cap gaps and opt-in content capture

- **Status:** accepted
- **Date:** 2026-10-13
- **Relates to:** ADR-991 (hard caps, translation, request log), ADR-994 (end users), ADR-995 (structured output, embeddings), ADR-988 (AI gateway)
- **Decision:**
  - **Translation.** openai-format routes accept the OpenAI Responses API (`POST …/responses`). OpenAI and Azure OpenAI targets get it unchanged. Other targets get it through the chat-completions translators, and the reply comes back as a Responses object or Responses stream events. A forced `tool_choice` (`required` or a named function) sent to a Claude model that rejects forced tool use becomes `auto` plus a system instruction naming the tool. OpenAI `strict` on a function tool now reaches Anthropic and Bedrock.
  - **Hard caps.**
    - A billable call whose usage the gateway cannot read now settles at its admission reservation instead of zero. Billable means generation or embeddings, including Gemini and Vertex embeddings.
    - With per-end-user limits set, an end user that is new on a day that already tracks 10,000 end users is refused instead of passing uncapped.
  - **Content capture.** An integration with the request log on may also keep the first 32 KiB of each logged call's request and response bodies, never headers. Captures are kept for 3 days, and at most 1,000 calls per integration per UTC day are captured. `GET …/ai-requests/{id}/content` and `gregale ai logs show` read them.
- **Why:**
  - Agents and current OpenAI SDK examples call the Responses API. Routes accepted only chat completions, so those apps could not use fallback routes or non-OpenAI targets at all.
  - Claude Fable 5.1, Mythos 5.1, Opus 5.5 and Sonnet 5.5 return 400 for `tool_choice` `any` or `tool`. A route that translated a forced choice therefore failed on exactly the models customers pick first.
  - Two leaks made the ADR-991 hard caps soft:
    - Gemini's `batchEmbedContents` and some streams report no usage. Such calls held a reservation at admission but settled at zero tokens and zero cost.
    - End users past the day's 10,000 tracked users landed in `~other`, where per-user caps do not apply. An app that minted a fresh user ID per call could bypass every per-user cap.
  - Debugging a bad completion needed the prompt and the answer. The request log (ADR-991) deliberately stored metadata only, so customers had to add their own logging inside the app.
- **Consequences:**
  - **Responses API.** Translated targets are stateless:
    - `previous_response_id`, `conversation`, stored `prompt` and `background` are refused with `400 outbound_ai_translation_unsupported`.
    - So are OpenAI-hosted tools (web search, file search, code interpreter, computer use, MCP), `input_file` and other content parts beyond text and images, non-string `function_call_output` values and unknown input item types.
    - Reasoning items in the input are dropped.
  - **Forced tool choice.** On the listed models, a forced tool choice becomes a strong request, not a guarantee. The model may still answer in text. Fable 5 and Opus 5 keep the native forced choice.
  - **Settling at the reservation.** A call with unreadable usage costs its reservation: estimated input plus the output ceiling or the 4,096-token default. That is usually more than the provider charged. The span attribute `gregale.ai.usage_estimated` marks these calls, and the request log shows outcome `unknown` with the estimate. Token counting and other free endpoints still settle at zero.
  - **New end users at the tracking cap.** On a day already tracking 10,000 end users, a new user's call gets `429` with `X-Gregale-Outbound-Rejection: user_tracking_full` until the next UTC day. This applies only while per-user limits are set; without them, new users still roll up under `~other`. Users already tracked that day are unaffected.
  - **Captured content.**
    - Content is stored in Postgres in plain form, like app logs. It is the body the gateway sent to the provider and the body it read back: for a translated route, the provider's format, not the caller's.
    - A request body the gateway streamed without buffering (over 4 MiB) is stored empty and marked truncated.
    - Turning capture off deletes the integration's captures at outboundd's next hourly prune. Deleting the integration deletes them immediately.
    - At most about 1,000 × 64 KiB = 64 MiB is stored per integration per day, and 3 days of that at most.
- **Rejected alternatives:**
  - **Implement Responses state (`previous_response_id`) in the gateway.** That would make outboundd a conversation store with its own retention, ownership and privacy rules. Apps can send the full input, which is what chat completions already require.
  - **Emulate forced tool choice with assistant prefill.** The same models reject prefill.
  - **Settle unknown usage at zero and alert.** Caps that a provider's missing usage field can bypass are not hard caps.
  - **Estimate output tokens from the response bytes.** Byte counts differ widely by tokenizer and language, and they are unknown for binary event streams. The reservation is the bound the caller already accepted at admission.
  - **Raise the tracked-users cap.** A higher cap only moves the bypass. Refusing new users while per-user caps are set keeps them honest; operators who want more users can remove per-user limits.
  - **Encrypt captured content with an age key pair as Run results are.** That needs a new key pair, key distribution to outboundd and decryption in apid. The same bytes already sit unencrypted in app logs, and retention is 3 days.
  - **Capture headers.** Headers carry credentials and the relay assertion. Bodies are enough to debug a completion.
  - **Capture every logged call.** An integration serving thousands of calls a minute would write gigabytes a day. 1,000 captures a day is enough to debug and bounds the table.

## Responses API routes

`POST /r/{route}/v1/responses` (or any route path ending in `/responses`) on an openai-format route sends:

| Target | Request |
| --- | --- |
| OpenAI | unchanged, with the target's model override |
| Azure OpenAI | `/openai/v1/responses` with the deployment as `model` |
| Anthropic, Gemini, Bedrock, Vertex | converted to chat completions, then the target's translator |

**Conversion to chat completions:**
- `instructions` becomes a system message.
- `input` is one user string, or a list of items:
  - `message` items keep their role. `input_text` and `output_text` parts become text, and `input_image` parts become image parts.
  - `function_call` items become assistant tool calls.
  - `function_call_output` items become tool messages.
- `tools` become function tools, with `strict`. `tool_choice` maps to `auto`, `none`, `required` or a named function.
- `max_output_tokens` becomes `max_completion_tokens`; `temperature`, `top_p`, `parallel_tool_calls` and `user` pass through.
- `text.format` becomes `response_format`, so ADR-995 structured output applies.

**Reply:**
- A JSON reply becomes a `response` object: `output` holds a `message` item with an `output_text` part and one `function_call` item per tool call. `usage` reports `input_tokens`, `output_tokens` and `total_tokens`. `status` is `incomplete`, with `incomplete_details.reason` `max_output_tokens` or `content_filter`, when the target stopped at the ceiling or was filtered.
- A stream is re-emitted as Responses SSE events:
  - `response.created` and `response.in_progress` open it;
  - text becomes `response.output_item.added`, `response.content_part.added` and `response.output_text.delta`;
  - tool calls become `response.function_call_arguments.delta`;
  - the matching `.done` events close each item;
  - `response.completed` or `response.incomplete` carries usage.
  - An error from the target becomes an `error` event.

Usage accounting reads the provider's own response, so budgets and the request log are unaffected by the conversion.

## Forced tool choice on current Claude models

For `claude-fable-5-1`, `claude-mythos-5-1`, `claude-opus-5-5` and `claude-sonnet-5-5`, the forced choice is rewritten for Anthropic, Vertex Claude and Bedrock Converse targets. The model name is normalized first: Bedrock ARN, `anthropic.` and region prefixes, `@date` and `-v1:0` suffixes are ignored.
- **Choice:** `tool_choice` becomes `auto`.
- **Instruction:** a system instruction says "Answer by calling the {name} tool.", or "Answer by calling one of the provided tools." for `required`.

Other Claude models keep `any` and `tool`.

## Hard-cap gaps

- **Billable endpoints.** Generation, OpenAI `/embeddings`, Gemini `embedContent` and `batchEmbedContents`, and Vertex `predict` on models whose name contains `embedding` are billable. Token counting and model listings are not. Every AI POST still reserves its estimated input at admission, and generation its output ceiling (ADR-991).
- **Unknown usage.** When a billable call ends with outcome `unknown`, settlement records the reservation's input and output tokens and its reserved cost. Errors still settle at zero.
- **Tracking cap.** Admission checks whether the end user already has a row for the day. If not, and the day already holds `api.OutboundAIUsersPerDay` rows, it refuses with `user_tracking_full` while either per-user limit is set.

## Content capture

`PUT /v1/outbound/integrations/{id}/ai` takes `request_log_content`, which requires `request_log_enabled`. CLI: `gregale ai policy <id> --request-log-content on|off`; `--request-log off` turns capture off too. Offers report `ai_request_log_content`.

**Capture.** outboundd copies the first `api.OutboundAIRequestContentMaxBytes` (32 KiB) of each body:
- the buffered request body;
- the provider response as the gateway reads it;
- the response `Content-Type`.

The copy is stored with the request-log row in the settlement transaction, in `outbound_ai_request_content`. An advisory lock per integration keeps concurrent settlements under `api.OutboundAIContentCapturesPerDay` (1,000) per UTC day. Later calls are logged without content.

**Read.** Request-log entries report `content_captured`. `GET /v1/outbound/integrations/{id}/ai-requests/{request_id}/content` returns `request` and `response`, each with:
- `body`;
- `encoding`: `utf-8`, or `base64` for bytes that are not UTF-8. A character the cut split in half is dropped.
- `truncated`;
- `content_type`: response only.

It answers `404` for a call without a capture. CLI: `gregale ai logs show <integration-id> <request-id>`; `gregale ai logs` now shows request IDs and a CONTENT column.

**Retention.** outboundd's hourly prune deletes captures older than `api.OutboundAIRequestContentRetentionDays` (3), and every capture of an integration whose capture is off.
