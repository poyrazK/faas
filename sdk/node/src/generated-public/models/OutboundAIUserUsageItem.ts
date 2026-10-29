/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One end user's UTC-day AI usage on an integration; tokens is input plus output, the amount the per-user token limit counts.
 */
export type OutboundAIUserUsageItem = {
  /**
   * X-Gregale-AI-User value; "~other" collects end users beyond the daily cap.
   */
  end_user: string;
  requests: number;
  error_requests: number;
  input_tokens: number;
  output_tokens: number;
  tokens: number;
  cost_millicents: number;
};

