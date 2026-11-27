/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Answer a call from the response cache when its last user turn means the same as a cached call's and everything else matches exactly. Pro and Scale plans; needs ai_provider and a response cache TTL. The embedding integration, one of the account's enabled OpenAI or Gemini integrations, embeds each candidate call on its own key, metered under the caller semantic_cache. A hit carries X-Gregale-AI-Cache semantic and X-Gregale-AI-Similarity.
 */
export type OutboundAISemanticCache = {
  enabled: boolean;
  /**
   * Cosine similarity a hit needs, in basis points. Absent or 0 means 9500.
   */
  threshold_basis_points?: number;
  embedding_integration_id: string;
  embedding_model: string;
  /**
   * Let calls of different end users (X-Gregale-AI-User) share answers. Off, each end user has their own.
   */
  share_across_users?: boolean;
};

