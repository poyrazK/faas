# ADR-1432 · AI gateway phase 10: refuse paid calls the gateway cannot meter, and meter video generation

- **Status:** accepted
- **Date:** 2026-10-16
- **Relates to:** ADR-991 (hard caps), ADR-996 (unknown usage), ADR-997 (media metering), ADR-1431 (batches)
- **Decision:**
  - **Writes on an AI integration are metered, known free, or refused.** A `POST` to an AI integration goes through only when the gateway either meters its endpoint or knows it to be free of charge.
    - **Metered:** generation, embeddings, images, audio and batches (ADR-997, ADR-1431), and now video generation.
    - **Known free:** an explicit list per provider (see below): file uploads, token counting, moderation, cancellations, conversation and metadata updates.
    - **Refused:** every other `POST`, and every `PUT` or `PATCH`, with `403 outbound_ai_endpoint_unmetered`. This covers endpoints that are paid but cannot be capped, such as realtime client secrets, vector stores, containers, fine-tuning, context caches, Gemini batch mode and Vertex batch and tuning jobs, and also any endpoint a provider adds later.
    - Reads (`GET`, `HEAD`) and `DELETE` are unchanged.
  - **Opt-in.** An integration's AI policy can set `allow_unmetered`. Refused calls then go through as before, unmetered, with `X-Gregale-AI-Unmetered: allowed`. Their spend is outside the integration's caps.
  - **Video generation is metered by seconds.** Veo `predictLongRunning` on Gemini and Vertex reserves and settles its duration × video count × the price per second for its resolution.
    - The duration is `durationSeconds` (default 8), the count is `numberOfVideos` or `sampleCount` (default 1), and the resolution is `resolution` (default `720p`).
    - A price is looked up for `<model>@<resolution>` first, then `<model>`, so price lists and the catalog can price resolutions apart.
    - A successful creation settles at that amount as known usage. The provider bills the generated seconds, and the operation's later result carries no usage.
    - A video call the gateway cannot size (body unreadable or larger than `OutboundAIVideoBodyMaxBytes`) or price (no price per second) is unmetered: refused unless `allow_unmetered`.
- **Why:**
  - Hard caps are the gateway's main promise (ADR-991). Before this ADR, a `POST` the gateway did not recognize passed through unmetered. Batches were one such hole (ADR-1431). Others remain: a realtime client secret lets a browser spend with the provider directly; vector stores, containers and context caches bill storage by the hour; video generation costs up to several euros a call.
  - Listing paid endpoints one by one loses to providers adding new ones. Listing what is metered or free, and refusing the rest, keeps a cap honest by default.
  - Veo is the only widely available video API the gateway serves. OpenAI shut down Sora and its Videos API on 2026-09-24, so OpenAI video needs no metering.
- **Consequences:**
  - **Behaviour change.** Integrations that called endpoints outside the lists now get `403 outbound_ai_endpoint_unmetered` until they set `allow_unmetered`. The AI gateway is in preview and production has no AI integrations, so no live caller breaks.
  - **One switch, not per endpoint.** `allow_unmetered` lets every unmetered endpoint through; a finer list can follow if customers need it.
  - **Fine-tuning is refused, not metered.** OpenAI is winding down its fine-tuning platform (no new users), so the gateway does not size training jobs. They need `allow_unmetered`.
  - **Video settles at creation.** A video whose operation later fails is still counted. The gateway does not poll operations.
  - **New columns:**
    - `outbound_integrations.ai_allow_unmetered`;
    - `outbound_ai_price_entries.millicents_per_video_second`;
    - `outbound_ai_usage_daily.video_seconds`.
  - **Catalog.** `catalog.json` gains a tenth field, the price per second of video, and `<model>@<resolution>` rows from LiteLLM's resolution prices for Veo on Gemini and Vertex.
- **Rejected alternatives:**
  - **Meter each paid endpoint as it is found.** Realtime client secrets and storage cannot be metered at all, and a list of paid endpoints is always behind the providers.
  - **Refuse only a list of known paid endpoints.** Same weakness: a new endpoint would pass unmetered until someone added it.
  - **Charge video at the dearest resolution.** It keeps caps safe but overstates every report; the request names its resolution, so it is priced exactly.
  - **Poll video operations and settle on completion.** The provider bills the requested seconds; polling would add a worker for no better number.

## Known free writes

Paths are matched after the API root (`/v1/`, `/v1beta/`, `/openai/` and similar). `*` is one path segment.

| Provider | Free writes |
| --- | --- |
| OpenAI, Azure OpenAI | `files`, `uploads`, `uploads/*/parts`, `uploads/*/complete`, `uploads/*/cancel`, `moderations`, `batches/*/cancel`, `fine_tuning/jobs/*/cancel`, `responses/*/cancel`, `responses/input_tokens`, `chat/completions/*`, `conversations`, `conversations/*`, `conversations/*/items` |
| Anthropic | `messages/count_tokens`, `messages/batches/*/cancel`, `files`, `skills`, `skills/*/versions` |
| Gemini | `files` (including uploads), `models/*:countTokens`, `batches/*:cancel`, `operations/*:cancel` |
| Vertex | Google models' `:countTokens`, `:computeTokens` and `:fetchPredictOperation` |
| Bedrock | `model/*/count-tokens` |
