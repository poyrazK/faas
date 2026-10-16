# ADR-997 · AI gateway phase 7: media metering, content guardrails and replay

- **Status:** accepted
- **Date:** 2026-10-13
- **Relates to:** ADR-996 (content capture, unknown usage), ADR-994 (price catalog), ADR-991 (hard caps, request log), ADR-990 (prices, routes), ADR-988 (AI gateway)
- **Decision:**
  - **Media metering.** Image generation, edits and variations, Imagen `predict`, audio transcription and translation, and speech are billable endpoints. They are reserved at admission and settled in the unit their price bills:
    - image tokens, or images;
    - audio seconds, or audio tokens;
    - characters.

    Price lists and the built-in catalog gain five optional media prices. Gemini image output tokens are now read by modality. A JSON response larger than the 1 MiB parse limit is now read for usage from its last 64 KiB, and its images are counted by marker.
  - **Content guardrails.** An AI integration may set a content policy. For each detector (e-mail address, payment card, IBAN, secret), outboundd leaves findings alone, redacts them before the request reaches the provider, or blocks the request. The policy also lists blocked terms. Captured content (ADR-996) is now always redacted for every detector.
  - **Replay and compare.** A captured call can be replayed on up to five targets: other integrations, other models, or both. apid records the replay; outboundd's worker sends the captured request to each target through the ordinary integration path. Each result records status, usage, cost, latency, the redacted response and its text.
- **Why:**
  - Image, transcription and speech calls settled as zero-token calls. An app could spend without limit under a token or cost cap, which ADR-991 promises is hard.
  - Gemini and gpt-image responses carry base64 images, so most are larger than the parse limit. Their usage was dropped even when the provider reported it.
  - Teams adopt gateways to stop personal data and keys from reaching model providers. ADR-996's capture also stored whatever the app sent, secrets included.
  - Choosing a cheaper model, or moving between providers, needs the same prompt run on both. Customers had to build an evaluation harness; the gateway already has the prompts, the credentials, the translators and the price lists.
- **Consequences:**
  - **Media metering.**
    - A model priced per image, per audio minute or per character reserves that unit. Any other model reserves tokens: 8,192 output image tokens per image, 50 audio tokens per estimated second, or the default output reserve for speech.
    - Upload durations are estimated at 16 kbit/s, so an upload of unknown length reserves 25 MiB of audio.
    - A call reporting no usage still settles at its reservation (ADR-996). For speech with a per-character price, the input characters are the usage.
    - A call is charged per image only when it reports no output image tokens, so a model priced both ways is not charged twice.
    - The catalog takes a per-image price only from LiteLLM's entry at the model's own name; size and quality variants are skipped, as before. gpt-image models report tokens and are priced per token. For a per-image model without such an entry, such as DALL-E, a price list sets the price.
    - Usage rollups count images, audio seconds and characters, and `gregale ai usage` shows them.
  - **Content guardrails.**
    - The policy reads the string values of JSON bodies; data URLs and base64 strings longer than 1 KiB are skipped. It also reads the text fields of multipart forms.
    - A form cannot be rewritten, so a finding to redact in a form blocks it.
    - With a policy on, a body the gateway cannot read is refused:
      - larger than the 4 MiB buffer, which includes large audio uploads (`413`);
      - unreadable JSON (`400 outbound_ai_content_unreadable`).
    - Responses are not checked.
    - Blocked content gets `400 outbound_ai_content_blocked`. The problem names the detector and never echoes the content.
    - Redactions are reported in `X-Gregale-AI-Content-Redactions: email=1,secret=2` and counted in `outbound_ai_content_findings_total`.
    - Captures are redacted after the cut plus 4 KiB more, so a secret split by the cut is still redacted. A redaction can make a capture shorter or mark it truncated.
  - **Replay.**
    - A replayed call counts against the target's budgets, rate limits and content policy like any call. It settles under the `replay` caller, so it shows separately in usage, and it is logged but never captured.
    - Replays resend the redacted capture, not the original request.
    - A replay needs a whole capture: a truncated request, or a capture made before this ADR without a path, cannot be replayed.
    - Calls are sent without streaming, so time to first byte is time to the whole response.
    - At most 100 replays per integration per UTC day; replays are kept 3 days.
- **Rejected alternatives:**
  - **Price images per size and quality from the request.** The catalog would need per-variant rows and the gateway would need each provider's size vocabulary. Token-priced image models already report exact usage, and a price list can set the exact per-image price of a per-image model.
  - **Read audio duration from the upload.** That means decoding every container format in the gateway. The bitrate estimate only sizes the reservation; settlement uses the duration the provider reports.
  - **Raise the JSON parse limit for media.** Several megabytes per call held in memory to read 200 bytes of usage. The tail read is bounded.
  - **Named-entity recognition, or a provider moderation call, in the request path.** Both add tens to hundreds of milliseconds and a dependency to every call. Their recall is not something we can promise either. Pattern detectors with checksums are fast and predictable; providers' own moderation stays available to apps.
  - **Check responses as they stream.** A finding in a stream that is already flowing cannot be withheld, so it could only be reported, not enforced.
  - **Optional capture redaction.** Captured content is stored unencrypted (ADR-996); keeping secrets there is never what a customer wants.
  - **Run replays in apid.** apid holds no provider credentials and must not call providers. Only outboundd opens them, so apid records intent and outboundd executes.
  - **Replay the unredacted original.** That would require keeping it, which capture redaction exists to avoid.

## Media metering

**Billable endpoints.**

| Endpoint | Media | Usage read |
| --- | --- | --- |
| OpenAI and Azure `…/images/generations`, `…/images/edits`, `…/images/variations` | image | `data` count, or `image_generation.completed` / `image_edit.completed` events; `usage` input and output tokens, with `input_tokens_details.image_tokens` and output tokens as image tokens unless split |
| Gemini and Vertex `models/imagen-…:predict` | image | predictions with image bytes |
| OpenAI and Azure `…/audio/transcriptions`, `…/audio/translations` | transcription | `usage` tokens, `usage.seconds` (type `duration`), or verbose `duration` |
| OpenAI and Azure `…/audio/speech` | speech | the request's `input` characters |
| Gemini and Vertex `generateContent` (all models) | — | `candidatesTokensDetails` and `promptTokensDetails` `IMAGE` counts as image tokens |

A multipart upload's `model` and `n` are read from its form, including the first 64 KiB of an upload too large to buffer.

**Prices.** Price-list entries and catalog rows gain:
- `input_image_millicents_per_mtok` and `output_image_millicents_per_mtok`, which fall back to the input and output prices;
- `millicents_per_image`, up to EUR 100;
- `millicents_per_audio_minute`, up to EUR 100;
- `millicents_per_million_characters`.

The per-image, per-minute and per-character prices default to zero. A media-only entry may leave the token prices at zero. CLI: `gregale ai prices set <id> --price model=dall-e-3,image=0.04`.

Cost is uncached text input, cached input, cache writes and input image tokens at their prices, plus text output and output image tokens at theirs, plus characters. It also adds audio seconds × the per-minute price ÷ 60 and, when there are no output image tokens, images × the per-image price. The total is rounded up once.

The catalog generator now keeps LiteLLM's `image_generation`, `audio_transcription` and `audio_speech` modes. It maps `input_cost_per_image_token`, `output_cost_per_image_token`, `output_cost_per_image`, `input_cost_per_second` (× 60) and `input_cost_per_character`. Audio models take the dearer of the text and audio input prices. No existing token price changed; 106 models were added.

**Reservation.**

| Call | Priced in its unit | Otherwise |
| --- | --- | --- |
| image (`n`, default 1; Imagen `sampleCount`, default 4; at most 10) | images | 8,192 output image tokens per image |
| transcription | estimated seconds (body bytes ÷ 2,000) | 50 audio tokens per estimated second |
| speech | characters | the default output reserve |

An image call's input estimate is capped at two images' worth of tokens, so uploaded images do not reserve their byte size.

## Content guardrails

`PUT /v1/outbound/integrations/{id}/ai-content-policy` replaces the policy:

```json
{"email": "redact", "payment_card": "block", "iban": "off", "secret": "block", "blocked_terms": ["Project Falcon"]}
```

Each action is `off`, `redact` or `block`. Blocked terms number at most 32, each 1 to 128 bytes, unique without case, and match without case. An active policy needs an AI provider, and turning AI handling off clears it. Offers report `ai_content_policy`. CLI: `gregale ai content-policy <id> --email redact --secrets block --block-term TERM`.

**Detectors:**
- **email:** an address with a dotted domain.
- **payment_card:** 13 to 19 digits, optionally in groups split by single spaces or hyphens, standing alone and passing the Luhn check.
- **iban:** two letters, two check digits and 11 to 30 more characters, passing ISO 7064 mod 97.
- **secret:**
  - OpenAI and Anthropic `sk-` keys;
  - AWS access key IDs;
  - GitHub, Slack, Google and Stripe live keys;
  - JWTs and bearer tokens of at least 20 characters;
  - PEM private keys.

Where findings overlap, a secret wins.

**Enforcement** runs before admission, so a blocked call costs nothing. Redaction replaces a finding with `[REDACTED:kind]` and re-encodes the JSON body; the rest of the body is unchanged.

## Replay

`POST /v1/outbound/integrations/{id}/ai-requests/{request_id}/replays` with `{"targets": [{"integration_id": "…", "model": "…"}]}` returns `202` and the pending replay. `GET /v1/outbound/ai-replays/{replay_id}` reads it. CLI: `gregale ai replay <id> <request-id> --target <id>[:<model>]` waits and prints a table comparing the original call with each target.

**Rules:**
- Targets are the account's customer AI integrations.
- A target on a provider other than the source's needs a model; without one, the captured model is kept.
- A captured OpenAI or Azure chat completions, Responses or embeddings call replays on any provider through the route translators (ADR-991, ADR-995, ADR-996). Results come back in the OpenAI format.
- Other calls replay on integrations of the same provider, with the model replaced in the body or the path.
- Streaming is turned off: `stream` and `stream_options` are removed, and streaming verbs become their plain forms.

**Execution.** apid inserts `outbound_ai_replays`. outboundd's worker:
- claims the oldest pending replay with `FOR UPDATE SKIP LOCKED`;
- calls its own handler in process with a replay principal, which only it can set. The principal reaches only the account's customer AI integrations, settles under `replay`, and skips capture.
- writes `outbound_ai_replay_results`.

Each result keeps:
- the status;
- the outcome, or the problem code of a refused call;
- tokens, cost, duration and time to first byte;
- the first 32 KiB of the response, redacted;
- up to 4 KiB of its text.

The text is read from OpenAI chat or Responses, Anthropic, Gemini, Bedrock Converse or transcription JSON, or from their event streams for the original. Tool calls appear as `[name(arguments)]`.

A replay running longer than 15 minutes fails as `outbound_replay_interrupted`. A replay whose capture is gone fails as `outbound_replay_capture_gone`.
