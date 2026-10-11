/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Replace an integration's AI gateway settings. An empty ai_provider turns AI handling off and requires a null daily_token_limit.
 */
export type PutOutboundAIPolicyRequest = {
  ai_provider: '' | 'openai' | 'anthropic';
  daily_token_limit: number | null;
};

