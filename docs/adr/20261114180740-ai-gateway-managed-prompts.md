# ADR 20261114180740 · AI gateway: managed prompts

- **Status:** accepted
- **Date:** 2026-11-14
- **Relates to:** ADR-990 (fallback routes), ADR-991 (OpenAI-format translation), ADR-1340 (route canaries), ADR-1431 (evaluations, access tokens), ADR-1434 (route strategies), ADR 20261021171712 (quality signals, feedback)
- **Decision:**
  - **A prompt is a named, versioned call recipe on the account.** `outbound_ai_prompts` holds the name (a DNS label, unique per account), an optional description, the live version and the previous live version. `outbound_ai_prompt_versions` holds immutable versions numbered from 1. A version has:
    - `messages`: up to `OutboundAIPromptMessagesMax` (32) messages with role `system`, `developer`, `user` or `assistant` and text content. `{{name}}` (spaces inside the braces allowed) inserts a variable; names match `^[A-Za-z_][A-Za-z0-9_]{0,63}$`, at most `OutboundAIPromptVariablesMax` (50) per version. Anything else, including `{{` not followed by a name and `}}`, is literal text. Substitution is plain text, with no logic, so a value can never add messages or change the template.
    - `defaults`: values for variables a call may omit.
    - a target: an OpenAI-format route (ADR-991) by ID, or one of the account's enabled managed AI integrations that an OpenAI-format route accepts, with a model. A route target may also name a model, which the route's targets without a model of their own (OpenAI targets) use.
    - `params`: `temperature` (0–2), `top_p` (0–1), `max_tokens` (1–`OutboundAIMaxTokensPerResponse`), up to 4 `stop` strings and a `response_format` JSON object of at most 16 KiB.
    - an optional note.

    The messages, defaults and params together are at most `OutboundAIPromptTemplateMaxBytes` (64 KiB). A version is never edited; changing a prompt means pushing a new version.
  - **Pushing.** `POST /v1/outbound/ai-prompts/{name}/versions` creates the prompt on its first version, which becomes live. A later version is live only when the request says `"live": true`. A push identical to the latest version returns that version with `200` instead of storing a duplicate, so `gregale ai prompt push` can run on every deploy. Each prompt keeps `OutboundAIPromptVersionsMax` (200) versions: pushing one more deletes the oldest version that is neither live, previous live nor in a canary. Prompts per account follow `OutboundAIPromptsMax` by plan (Free 5, Hobby 25, Pro 100, Scale 500).
  - **Calling.** An app calls `POST $GREGALE_OUTBOUND_URL/p/{name}` (relay or access-token callers, as for routes) with `variables`, optional `messages` appended after the template's (any OpenAI chat message, so a chat app can send the conversation), optional `stream`, and optional `version` to pin one. The gateway:
    - picks the version: the pinned version; otherwise the canary version for the canary's share of calls (sticky per `X-Gregale-AI-Session` or `X-Gregale-AI-User`, as route canaries are); otherwise the live version;
    - renders the version into an OpenAI chat completions request, refusing a missing variable with `400 outbound_ai_prompt_variable_missing` that names it;
    - serves the rendered request through the ordinary route path: the version's route, or a one-target OpenAI-format route for an integration target. Translation, fallback, budgets, caps, guardrails, caches, tags, traces and accounting are unchanged.

    The response is OpenAI chat completions JSON or SSE, with `X-Gregale-AI-Prompt-Version` naming the version that served it. A prompt with no live version, or whose target is gone, answers `404 outbound_ai_prompt_not_found` or `409 outbound_ai_prompt_target_unavailable`.
  - **Recording.** Each settled attempt of a prompt call adds to its version's per-minute row in `outbound_ai_prompt_version_minutes` (calls, errors, time to first byte, cost; no foreign key, like route minutes). The request log gains `prompt_id` and `prompt_version`, so feedback and quality flags count per version. `GET /v1/outbound/ai-prompts/{name}/stats` returns each version's counts and good/bad feedback over `OutboundAIPromptStatsWindowMinutes` (24 h).
  - **Going live.**
    - `PUT .../live {"version": N}` makes a version live; the old live version becomes the previous one, and any canary ends.
    - `gregale ai prompt rollback` puts the previous live version back.
    - A canary (`PUT .../canary`) sends 1–50% of unpinned calls to a candidate version, with the same thresholds as route canaries: `max_error_rate_increase`, `max_latency_increase_percent`, `min_calls` and `window_minutes`. outboundd judges active prompt canaries on the route-canary interval with the same rule and rolls them back (`error_rate`, `latency`). `POST .../canary/promote` makes the candidate live and ends the canary.
  - **Evaluating.** `POST .../evaluations` runs a candidate version against the live version on up to `OutboundAIPromptEvaluationCasesMax` (50) variable sets the caller supplies. outboundd's evaluation worker renders both versions per case, calls each version's target in process under the `evaluation` caller (a route target uses its first target), and asks the judge model which answer is better with the ADR-1431 judge prompt. The pass rule is the same as ADR-1431's. A passing evaluation can start a canary it was created with.
  - **CLI and SDKs.**
    - The CLI has `gregale ai prompts list|get|push|diff|render|promote|rollback|canary|stats|evaluate|rm`. `push` reads a YAML or JSON file (`name`, `description`, `messages`, `target`, `params`, `defaults`, `note`); `render` shows what a version sends for given variables without calling a model.
    - The Node and Python SDKs get `prompt(name).run(variables, options)` helpers that call `/p/{name}`.
- **Why:**
  - Prompts are the part of an AI app that changes most often, and today every change is a code change and a deploy. Product and support teams cannot tune wording without an engineer.
  - The gateway already has what versioned prompts need: route serving with translation and fallback, canaries with automatic rollback, an evaluation judge and per-call feedback. Prompts reuse them instead of adding a second pipeline.
  - Variables substituted as plain text keep a template's structure fixed, so end-user input reaches the model only where the author placed it.
- **Consequences:**
  - **Prompt text lives on Gregale.** Templates are configuration, readable with read scope like routes. The variables a call sends are request content and are captured only when the integration opted in to capture (ADR-996).
  - **Stats count attempts.** A prompt whose route falls back records the failed attempt as an error for that version. Both versions of a canary face the same providers, so the comparison stays fair.
  - **Evaluations use supplied cases, not captured traffic.** Captured requests hold the rendered text, not the variables, so they cannot be re-rendered with another version. Re-rendering captured traffic would need variables stored at rest; this is left for later.
  - **Targets can disappear.** A version stores its route or integration without a foreign key, so deleting a route or integration never fails because of a prompt. A call to a version whose target is gone answers `409`, and pushing a new version fixes it.
  - **New tables:**
    - `outbound_ai_prompts`;
    - `outbound_ai_prompt_versions`;
    - `outbound_ai_prompt_canaries`;
    - `outbound_ai_prompt_version_minutes`;
    - `outbound_ai_prompt_evaluations` and `outbound_ai_prompt_evaluation_items`.

    The request log gains two nullable columns. New metrics: `outbound_ai_prompt_calls_total{pick}` (`live`, `canary`, `pinned`) and `outbound_ai_prompt_canary_rollbacks_total{reason}`.
- **Rejected alternatives:**
  - **A template language with conditions and loops.** It gets more power from the author but turns every value into a possible injection into the template's logic, and it is hard to diff and evaluate. Callers that need branching can keep several prompts.
  - **Prompts inside routes.** A route chooses providers, while a prompt chooses wording. Keeping them apart lets many prompts share one route's fallback and strategy.
  - **Prompts addressed by OpenAI `model` names (`model: "prompt:support-reply"`).** This hides the version pin and the variables inside a field SDKs validate differently. A dedicated path keeps the contract explicit.
  - **Storing prompts in `gregale.yaml` only.** Rollout without a deploy is the point. `push` from a file in the repo covers teams that want prompts in git.
