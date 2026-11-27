/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A model-based check of the request's user, tool and input text before the provider call. It runs through another of the account's AI integrations and is billed by that integration's provider: openai_moderation calls OpenAI's /v1/moderations on an OpenAI integration; classifier asks a chat model which categories apply. flag lets the call through and names the categories in X-Gregale-AI-Moderation; block refuses it with 400 outbound_ai_content_flagged. on_error decides what a failed check does; block answers 503 outbound_ai_moderation_unavailable.
 */
export type OutboundAIModeration = {
  action?: '' | 'off' | 'flag' | 'block';
  kind?: 'openai_moderation' | 'classifier';
  integration_id?: string;
  /**
   * Required for classifier.
   */
  model?: string;
  categories?: Array<'prompt_injection' | 'harassment' | 'hate' | 'self_harm' | 'sexual' | 'violence' | 'illicit'>;
  on_error?: '' | 'allow' | 'block';
};

