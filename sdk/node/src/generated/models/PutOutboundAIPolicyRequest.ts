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
   * Log per-call metadata (caller, route, model, status, tokens, cost, timing) for 7 days (ADR-984). Requires ai_provider.
   */
  request_log_enabled?: boolean;
};

