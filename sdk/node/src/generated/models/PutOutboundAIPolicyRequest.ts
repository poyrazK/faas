/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Replace an integration's AI gateway settings. An empty ai_provider turns AI handling off and requires a null daily_token_limit.
 */
export type PutOutboundAIPolicyRequest = {
  ai_provider: '' | 'openai' | 'anthropic' | 'gemini' | 'azure_openai' | 'bedrock' | 'vertex';
  daily_token_limit: number | null;
  daily_cost_limit_millicents?: number | null;
  monthly_cost_limit_millicents?: number | null;
  /**
   * Log per-call metadata (caller, route, model, status, tokens, cost, timing) for 7 days (ADR-991). Requires ai_provider.
   */
  request_log_enabled?: boolean;
  /**
   * Also keep the first 32 KiB of each logged call's request and response bodies, never headers, for 3 days and at most 1,000 calls per UTC day (ADR-996). Requires request_log_enabled.
   */
  request_log_content?: boolean;
  /**
   * Send each settled app call to the app's log drains as one JSON line of metadata, never prompts or completions, about 15 seconds after it settles (ADR-1340). Requires ai_provider.
   */
  log_export?: boolean;
  /**
   * Let through writes the gateway can neither meter nor knows to be free, such as realtime client secrets, vector stores, containers, fine-tuning jobs and context caches. They are otherwise refused with 403 outbound_ai_endpoint_unmetered; let through, they carry X-Gregale-AI-Unmetered and their spend is outside the caps (ADR-1432). Requires ai_provider.
   */
  allow_unmetered?: boolean;
  /**
   * Add prompt-cache breakpoints to Claude requests on the Anthropic API, Vertex AI and Amazon Bedrock that carry none: after a system prompt or tool list seen again within five minutes, and after the last message of a conversation that carries earlier turns or names X-Gregale-AI-Session. Prompts under about 1,024 tokens are not marked; the response names the added breakpoints in X-Gregale-AI-Prompt-Cache (ADR-1437). Requires ai_provider.
   */
  auto_prompt_cache?: boolean;
  /**
   * Per-end-user UTC-day token limit for calls that send X-Gregale-AI-User (ADR-994). Requires ai_provider.
   */
  user_daily_token_limit?: number | null;
  /**
   * Per-end-user UTC-day cost limit for calls that send X-Gregale-AI-User (ADR-994). Requires ai_provider.
   */
  user_daily_cost_limit_millicents?: number | null;
};

