/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Replace an app's AI guardrails on one integration. Null fields remove that guardrail.
 */
export type PutOutboundBindingAIGuardrailsRequest = {
  requests_per_minute?: number | null;
  tokens_per_minute?: number | null;
  allowed_models?: any[] | null;
  max_output_tokens?: number | null;
};

