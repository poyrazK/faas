# ADR 20261021171712 · AI gateway: quality signals

- **Status:** accepted
- **Date:** 2026-10-21
- **Relates to:** ADR-991 (request log, budget alerts), ADR-996 (content capture), ADR-1340 (moderation, log export), ADR-1431 (evaluations), ADR 20261020202035 (AI traces)
- **Context:**
  - The gateway meters what a call cost. It does not say whether the answer was any good.
  - Most bad answers leave marks the gateway already sees:
    - the model stopped at its output limit;
    - the provider refused or filtered the answer;
    - the answer was empty;
    - JSON was asked for and the answer, or a tool call's arguments, did not parse;
    - the app reported that a tool the model called failed.
  - Other answers are wrong in ways only a person or a judge model can tell. Teams already collect thumbs up and down in their apps, but have nowhere to join them to the calls.
  - Quality also changes without any deploy, when a provider updates a model behind the same name.
- **Decision:**
  - **Every AI call gets an ID.** The gateway gives each AI call an ID before it calls the provider and returns it in `X-Gregale-AI-Call`.
    - The ID is the call's request-log entry ID, so feedback, evaluations and the log refer to the same call. The log-export line carries it as `call_id`, and OTLP drains as `gregale.ai.call_id`.
    - Each realtime turn gets its own ID in the log, but a WebSocket has no per-turn header, so realtime turns cannot take feedback.
  - **Quality flags.** Settlement marks a call that answered (2xx with known usage) with any of:

    | Flag | Set when |
    | --- | --- |
    | `truncated` | the model stopped at the output-token limit: `finish_reason` `length`, Responses `incomplete_details.reason` `max_output_tokens`, Anthropic `stop_reason` `max_tokens`, Gemini `finishReason` `MAX_TOKENS`, Bedrock `stopReason` `max_tokens` |
    | `refused` | the provider refused or filtered it: an OpenAI `refusal` or `finish_reason` `content_filter`, a Responses `refusal` part, Anthropic `stop_reason` `refusal`, a Gemini block reason or `finishReason` `SAFETY`, `RECITATION`, `BLOCKLIST`, `PROHIBITED_CONTENT` or `SPII`, Bedrock `stopReason` `guardrail_intervened` or `content_filtered` |
    | `empty` | the answer carried no text and requested no tools |
    | `invalid_json` | the request asked for JSON (OpenAI `response_format` or Responses `text.format` of type `json_object` or `json_schema`, Gemini `responseMimeType` `application/json`) and the answer text is not JSON; a requested tool call's arguments are not JSON; or Gemini reports `MALFORMED_FUNCTION_CALL` |
    | `tool_error` | the request's newest turn reports a failed tool: an Anthropic `tool_result` with `is_error: true`, or a Bedrock Converse `toolResult` with `status` `error` |

    - Only the newest turn counts for `tool_error`. Every later request repeats the history, and that would count the same failure again.
    - The JSON check reads at most `OutboundAIQualityTextMaxBytes` of answer text, and at most `OutboundAIQualityToolArgumentsMaxBytes` of each tool call's arguments. A longer answer or argument string is not checked. The text is checked as it streams and then discarded.
    - OpenAI and Gemini formats have no marker for a failed tool, so their calls never get `tool_error`.
    - **Where flags appear:**
      - the call's request-log entry (`quality_flags`);
      - the log-export line, and `gregale.ai.quality_flags` for OTLP drains;
      - the daily usage rollup, which counts calls per flag for each caller and model;
      - `outbound_ai_quality_flags_total{integration_id, flag}`.
  - **Feedback.** An app rates one of its calls with `POST /i/{integration}/_gregale/feedback`, as `{"call_id": "...", "rating": "good"|"bad", "label": "wrong_answer"}`.
    - The app authenticates as it does for AI calls: by its workload identity, or by an access token.
    - The call must be in the integration's request log and made by the same app; otherwise the reply is `404 outbound_ai_call_not_found`.
    - For a route call, the integration is the one `X-Gregale-AI-Route-Target` names.
    - A call has one rating, and a later one replaces it.
    - The label is optional: up to `OutboundAIFeedbackLabelMaxBytes` lowercase letters, digits, `_`, `.` or `-`, starting with a letter. There is no free-text field, so feedback carries no end-user data.
    - Feedback is kept on the request-log entry and leaves with it after `OutboundAIRequestLogRetentionDays`. The daily rollup counts good and bad ratings on the day and model of the call.
    - A person can rate calls too: `PUT /v1/outbound/integrations/{id}/ai-requests/{request}/feedback`, and `gregale ai feedback`.
  - **Online evaluations.** An integration's AI policy may set `online_evaluation`:
    - `sample_percent`, from 1 to 100;
    - `judge_integration_id` and `judge_model`;
    - an optional `rubric` of up to `OutboundAIOnlineEvaluationRubricMaxBytes` describing what a good answer does for this app.

    **Sampling.** A sampled call is one that answered, was made by an app, and had both bodies captured whole. It needs `request_log_content` to be on.
    - Settlement samples it at random and marks its log entry pending.
    - An integration evaluates at most `OutboundAIOnlineEvaluationsPerDay` calls per UTC day.

    **Judging.** A worker in outboundd claims pending calls (a claim older than `OutboundAIOnlineEvaluationClaimMinutes` is taken again) and sends each call's redacted request text and answer text to the judge.
    - It uses the ADR-1431 machinery: an in-process call through the customer's judge integration, metered and capped under the caller `evaluation`.
    - The judge replies `{"score": 1-5}`. The score goes on the log entry.
    - The daily rollup adds evaluated calls, the sum of scores and judge errors.
    - Calls whose text the gateway cannot read, and captures that expired first, count as judge errors.
  - **Drift alerts.** With the AI policy's `quality_alerts` on, outboundd checks each integration every `OutboundAIQualityDriftCheckMinutes`.
    - **What it compares:** each model's UTC day so far against its previous `OutboundAIQualityDriftBaselineDays` days, summed over callers.
    - **Rate metrics** are the five flag rates and the share of bad ratings. One alerts when all of these hold:
      - the day has at least `OutboundAIQualityDriftMinCalls` calls (or rated calls, for feedback);
      - the baseline has at least `OutboundAIQualityDriftMinBaselineCalls`;
      - today's rate is at least `OutboundAIQualityDriftPoints` percentage points above the baseline, and at least twice the baseline.
    - **The mean evaluation score** alerts when it falls by at least `OutboundAIQualityDriftScoreDropTenths` tenths of a point, with at least `OutboundAIQualityDriftMinEvaluations` evaluations on each side.
    - **Each alert:**
      - fires once per integration, model, metric and UTC day;
      - is kept for `OutboundAIQualityAlertRetentionDays`;
      - sends `outbound.ai_quality.drift` to every app bound to the integration;
      - is listed by `GET /v1/outbound/integrations/{id}/ai-quality-alerts` and `gregale ai quality-alerts`.
  - **Visibility.**
    - `GET .../ai-usage` items gain the flag counts, good and bad ratings, evaluated calls, mean score and judge errors.
    - Request-log entries gain `quality_flags`, `feedback`, `feedback_label` and `evaluation_score`.
    - CLI: `gregale ai usage` shows the flag share and the mean score; `gregale ai policy` takes `--online-eval-percent`, `--online-eval-judge`, `--online-eval-model`, `--online-eval-rubric` and `--quality-alerts`.
- **Why:**
  - The gateway is the one place that sees every call, request and answer. These signals need no SDK and no code in the app, apart from feedback.
  - **The flags are structural.** They come from fields providers already return, so they cost no model call and they work on every call.
  - **Feedback keyed by a call ID** joins what users say to the exact request, model and cost. An app can do that with one header and one POST.
  - **Online evaluations reuse ADR-1431's judge and caps.** The customer chooses and pays for the judge model, the sample bounds that spend, and the captures it reads already exist under ADR-996's rules.
  - **Drift against the model's own recent past** catches silent provider changes. A fixed threshold would either miss them or fire on every naturally noisy model.
- **Consequences:**
  - **Request log ID:** the gateway now chooses each request-log row's ID. Its column default stays, for older writers.
  - **New columns:**
    - `outbound_ai_request_log`: `quality_flags`, `feedback`, `feedback_label`, `feedback_at`, `evaluation_state`, `evaluation_claimed_at` and `evaluation_score`;
    - `outbound_ai_usage_daily`: seven counters (one per flag, plus good and bad ratings) and three evaluation totals;
    - `outbound_integrations`: the online-evaluation settings and `ai_quality_alerts`.
  - **New tables:** `outbound_ai_online_evaluation_days` and `outbound_ai_quality_alerts`.
  - **New webhook event:** the outbox event check gains `outbound.ai_quality.drift`, added `NOT VALID` and validated online.
  - **Settlement** writes the flags in statements it already sends. A sampled call adds one statement to the settlement batch.
  - **Response guardrails** may change an answer, but flags describe the provider's answer, which is the one the model is responsible for.
  - **Signals are heuristics.** A `length` stop can be intended, and a judge can be wrong. The gateway reports rates and changes in rates; it never blocks on them.
- **Rejected alternatives:**
  - **Free-text feedback comments.** They would carry end-user data outside capture's opt-in and retention.
  - **Pairwise online evaluation against another model.** It doubles the spend per sample, and ADR-1431 already does it before a canary.
  - **Alerting on fixed thresholds.** Normal rates differ by model and app, from near zero to tens of percent.
  - **Detecting refusals from answer text.** Phrases like "I can't help" are language- and model-specific. Providers' own refusal fields are exact.
