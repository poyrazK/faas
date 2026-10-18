# ADR-1431 · AI gateway phase 9: evaluations before a canary, access tokens for callers outside Gregale, and batch APIs

- **Status:** accepted
- **Date:** 2026-10-16
- **Relates to:** ADR-988 (deferred public endpoint), ADR-991 (hard caps), ADR-997 (replay), ADR-1120 (admission round trips), ADR-1340 (model canaries, moderation)
- **Decision:**
  - **Evaluations.** A customer can evaluate a route target against the route's first target before a canary.
    - outboundd takes the latest captured calls of the first target's integration (at most `OutboundAIEvaluationSampleMax`) and replays each on the candidate target.
    - A judge model of the customer's choice compares the captured answer and the candidate's answer. The two answers are shown in a random order, so the judge sees no position.
    - The evaluation reports wins, ties, losses and candidate errors, with both targets' cost and time to first byte.
    - It passes when losses plus errors are at most `max_loss_rate` percent of the sample.
    - A passing evaluation can start the canary it was created with. The customer asked apid for that canary when creating the evaluation; outboundd starts it later on their behalf.
    - Candidate and judge calls run in process through the customer's integrations, metered and capped under the caller `evaluation`.
  - **Access tokens.** apid issues expiring tokens for one app (`ggai_` + 32 random bytes; only a SHA-256 hash is stored).
    - outboundd accepts them on an optional public listener, behind the control plane's Caddy, for the app's AI integrations and the account's routes only.
    - A caller sets the base URL and puts the token where the SDK puts its API key: `Authorization: Bearer`, `x-api-key`, `x-goog-api-key` or `api-key`. outboundd removes every such header before it adds the provider credential.
    - A token call is the app's call: the app's bindings, guardrails, caps and content policy apply, and usage is the app's.
    - A token can be narrowed to listed integrations. It expires after at most `OutboundAIAccessTokenTTLMaxHours` and can be revoked at any time.
  - **Batch APIs.** OpenAI and Azure OpenAI `/batches` and Anthropic `/messages/batches` become AI endpoints.
    - **Creating a batch** reserves its estimated tokens and cost, priced at `OutboundAIBatchPricePercent`. Anthropic's inline requests are read from the body; for OpenAI, outboundd reads the input file from the provider.
    - **Before the provider call**, the reservation is admitted against the integration's caps and kept in `outbound_ai_batches`. Admission counts open batches with live leases, in the statement that already totals leases.
    - **Settlement:** outboundd polls open batches. When one ends (completed, failed, expired or cancelled), it reads the results, settles the actual usage per model, and releases the reservation. Each settlement is keyed by the batch and the model, so a retry counts it once.
    - **A batch whose input file outboundd cannot read is refused**, because its spend could not be capped.
- **Why:**
  - **Evaluations.** A canary (ADR-1340) protects production traffic but still sends real users to the new model. Replaying recent real calls first catches a clearly worse model before any user sees it. ADR-997 already keeps the calls and the replay machinery; a judge is the missing piece. No peer runs an offline comparison before an online one.
  - **Access tokens.** Only apps running on Gregale can call the gateway today: through the relay (ADR-989) or a vmmd-signed workload identity. A developer running an app on a laptop, a CI job or a notebook needed their own provider key, outside the caps. ADR-988 deferred a public endpoint until it could be scoped; tokens bound to one app's caps are that scope.
  - **Batches.** The batch endpoints were not classified as AI calls, so batch spend bypassed every cap and report. Batches also cost half the list price, so customers have a reason to use them.
- **Consequences:**
  - **Admission:** the lease-totals statement also sums open batches' reservations for the integration and app. Admission keeps its round trips: `TestAIGatewayRoundTripBudget` still holds at 11 and 12.
  - **Batch reservations last until the batch ends**, up to the provider's completion window (24 hours). A large batch can therefore hold a day's cap until it settles, and its usage counts toward the day it settles in, like other calls.
  - **Batch caps:** batches are reserved against the integration's and the app's daily token caps and the integration's cost caps. Per-end-user limits are not reserved for batches: a batch names no end user.
  - **Abandoned batches:** a batch that has not ended after `OutboundAIBatchAbandonHours` settles at its reservation as unknown usage, like ADR-996's unreadable calls, and is no longer polled. A pending row that never received a provider batch ID is deleted after an hour, which releases it.
  - **OpenAI input files:** creating an OpenAI batch reads its input file (at most `OutboundAIBatchInputMaxBytes`) once from the provider before admission, adding that download's latency to batch creation.
  - **A public listener.** outboundd gains an optional listener that serves only token callers, on `/i/` and `/r/` paths. Caddy routes a configured host to it when `faas_ai_gateway_public_enabled` is set, and DNS for that host is an operator step. With the flag off nothing changes.
  - **Token checks** are cached for `OutboundAIAccessTokenCacheSeconds`, so a revoked token can work for that long. `last_used_at` is written at most once a minute per token.
  - **Evaluations need captures.** An integration without request-log content capture (ADR-996) has nothing to replay, and its evaluation fails with `outbound_ai_evaluation_no_captures`. Captured requests are already redacted, so the candidate and the judge see redacted text.
  - **outboundd starts an evaluated canary.** A passing evaluation inserts the canary row that apid would, holding the route's row lock as apid does. It is the canary the customer asked apid for when creating the evaluation, as ADR-1340's rollback is a write the customer asked for when starting the canary. apid still owns the route and its targets.
  - **Batches are checked less than calls.** The content policy checks an Anthropic batch's inline requests like any body, but an OpenAI batch's input file is only sized, never scanned. Moderation does not run on batches: one check per request would cost as much as the batch. Batch results are not captured or exported.
  - **A batch's creation call counts twice, briefly.** Its lease and its batch row both hold the estimate until the creation call returns. Admission overcounts for that moment and never undercounts.
  - **New tables:**
    - `outbound_ai_evaluations` and `outbound_ai_evaluation_items` are operational;
    - `outbound_ai_access_tokens` is account identity (only hashes);
    - `outbound_ai_batches` is operational.
    None is cloned.
- **Rejected alternatives:**
  - **Evaluations.**
    - **Score with exact match or embeddings.** Generated text rarely matches exactly, and embedding similarity does not say which answer is better. A pairwise judge does.
    - **Judge each answer alone on a scale.** Absolute scores drift between calls; a side-by-side comparison of the same prompt is steadier.
  - **Access tokens.**
    - **Proxy through apid.** apid would carry every streamed generation and become an AI proxy. outboundd already owns this path; a second listener keeps one owner.
    - **Reuse workload identity tokens.** They are vmmd-signed, five-minute assertions for running instances; a developer has no instance.
    - **Tokens for any managed integration.** AI integrations have caps that bound a leaked token's damage; arbitrary integrations do not.
  - **Batches.**
    - **Intercept the file upload instead of reading the file back.** The upload is a multipart stream, and the batch can name a file uploaded any other way. Reading the named file once at batch creation covers both.
    - **Settle when the customer downloads the results.** A customer may never download them, and their spend would never settle.
    - **Count batches as leases.** A lease holds an in-flight slot; a day-long batch would block the integration's concurrency.

## Evaluation rules

- A sample is a captured call of the first target's integration:
  - its request log status is 2xx;
  - neither body was truncated;
  - its path is an OpenAI chat completions or Responses call, which replays on any provider through the translators.
- The judge receives the redacted user text (as ADR-1340's moderation reads it) and the two answers' text. It replies with a JSON object `{"winner": "A"|"B"|"tie"}`. A reply without one counts as a tie and is reported as `judge_error`.
- A candidate call that fails counts as a loss and is reported as `candidate_error`.
- Samples sent by the gateway itself (replays, moderation checks, evaluations) are skipped.
- The first target must be an OpenAI or Azure OpenAI integration, since its captures are replayed through the translators.
- `OutboundAIEvaluationParallelCalls` samples run at a time, each call within `OutboundAIEvaluationCallTimeoutSeconds`.
- An evaluation with more than `OutboundAIEvaluationJudgeErrorMaxPercent` percent judge errors fails with `outbound_ai_evaluation_judge_failed` instead of passing on ties.
- A passing evaluation starts its canary only when the route has no active canary and the target at its position still has the integration and model it was evaluated with.
- An evaluation runs once; `OutboundAIEvaluationsPerDay` bounds evaluations per route per UTC day.

## Batch estimate

A batch's first status check waits one `OutboundAIBatchPollIntervalSeconds`. Each request's input is its body bytes divided by `OutboundAIReserveBytesPerToken`. Its output is its own output ceiling, or `OutboundAIDefaultOutputReserveTokens`. Cost is priced per request model at `OutboundAIBatchPricePercent` of the integration's price.
