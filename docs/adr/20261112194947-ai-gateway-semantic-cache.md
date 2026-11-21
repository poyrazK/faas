# ADR 20261112194947 · AI gateway: semantic response cache

- **Status:** accepted
- **Date:** 2026-11-12
- **Relates to:** ADR-294 (response cache), ADR-990 (AI cache, prices), ADR-1340 (moderation check, in-process calls), ADR-1435 (response checks), ADR-1440 (streaming cache, cache savings), ADR-1410 (end users)
- **Decision:**
  - **Opt-in per integration.** An AI policy gains `semantic_cache`: `enabled`, `threshold_basis_points` (8500–9900, default 9500), `embedding_integration_id`, `embedding_model` and `share_across_users`. It needs the response cache (`response_cache_ttl_seconds` > 0) and a plan that includes it (Pro and Scale, `OutboundAISemanticCacheAllowed` in `pkg/api/limits.go`).
  - **Only the wording varies.** A call is a candidate when it is a buffered, non-batch generation call on chat completions, Responses, Anthropic messages or Gemini `generateContent`. Everything but the last user turn's text must match exactly. The partition key hashes:
    - the integration, the caller (app), the policy revision and the model;
    - the request with the last user turn's text removed: system prompt, earlier turns, tools, response format, sampling parameters and stream flag;
    - the end user (`X-Gregale-AI-User`) unless `share_across_users` is set.

    Calls with images, audio, files or tool results in the last turn, more than `OutboundAISemanticCacheMaxTurns` (3) user turns, more than `OutboundAISemanticCacheMaxInputBytes` (8 KiB) of embedded text, or `Cache-Control: no-cache` are not candidates.
  - **Order.** The exact-match cache is read first. On a miss the content policy and the moderation check run as before, since a semantically similar prompt was never checked. Only then is the semantic index searched; on a semantic miss the call goes to the provider.
  - **Embedding call.** The last user turn's text is embedded through the policy's embedding integration in process, as the moderation check is (`caller = semantic_cache`): admitted, capped and metered on that integration and billed to its key. Supported providers: OpenAI (`/v1/embeddings`) and Gemini (`batchEmbedContents`, through the embeddings translation of ADR-995). The call has `OutboundAISemanticCacheEmbedTimeoutSeconds` (1 s); any failure skips the semantic lookup and the call goes to the provider.
  - **Index.** Per outboundd process, bounded at `OutboundAISemanticCacheEntriesMax` (2,000) entries per integration with least-recently-used eviction. Each entry holds the partition key, the unit vector and the exact response-cache key of the answer it points at; no response is stored twice. Entries are added when a miss's `200` response is stored in the response cache, and leave the index when a lookup finds the answer gone. A hit needs cosine similarity ≥ the threshold and the response-cache entry still present; a missing entry is a miss.
  - **Hits.** A semantic hit is served and settled like an exact hit (ADR-1440): one cached request, no tokens, with the saving recorded. It carries `X-Gregale-AI-Cache: semantic` and `X-Gregale-AI-Similarity` (four decimal places), and the call's span records `gregale.ai.semantic_similarity`; like an exact hit, it writes no request log row. Response checks apply to hits as to misses.
  - **Metrics.** `outbound_ai_semantic_cache_total{integration_id, result}` with results `hit`, `miss`, `skipped` and `error`.
- **Why:**
  - The exact cache only helps when callers send byte-identical requests. Many apps ask the same question in different words (support bots, FAQ assistants, search), and pay for a full generation each time.
  - A provider-side vector store or Postgres vector extension would add a dependency to the control plane for a cache, which is not truth (ADR-294 keeps the cache per process for the same reason).
- **Consequences:**
  - **A wrong answer is possible.** Two prompts above the threshold can still need different answers. The conservative default threshold, the exact match on everything but the last turn and the per-user partition limit this; customers opt in per integration.
  - **Each miss costs one embedding call**, billed to the customer's embedding key. Usage shows it under the `semantic_cache` caller.
  - **Per process.** Each outboundd node builds its own index, like the response cache; a restart empties it.
  - **Memory.** 2,000 entries of up to 3,072 float32 dimensions is about 24 MiB per integration; the index holds at most `OutboundAISemanticCacheIntegrationsMax` (256) integrations and `OutboundAISemanticCacheIndexBytesMax` (64 MiB) per process, evicting the least recently used entry.
  - **Plan changes.** outboundd applies the settings only while the account's plan includes the semantic cache and the integration has a response cache; a downgrade turns it off without rewriting the stored settings.
  - **New column:** `outbound_integrations.ai_semantic_cache` (jsonb), and the usage caller CHECK accepts `semantic_cache`.
- **Rejected alternatives:**
  - **pgvector in the control-plane database.** Shared across nodes, but adds an extension, writes on every miss and vector data at rest for a cache.
  - **Embedding the whole conversation.** Longer inputs cost more and match less; exact-matching the earlier turns is cheaper and safer.
  - **Gregale-provided embeddings.** Needs pricing and billing decisions (Gregale-provided provider keys); the customer's own key works now.
