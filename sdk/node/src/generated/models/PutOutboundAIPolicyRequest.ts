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
   * Per-end-user UTC-day token limit for calls that send X-Gregale-AI-User (ADR-994). Requires ai_provider.
   */
  user_daily_token_limit?: number | null;
  /**
   * Per-end-user UTC-day cost limit for calls that send X-Gregale-AI-User (ADR-994). Requires ai_provider.
   */
  user_daily_cost_limit_millicents?: number | null;
};

