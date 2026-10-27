/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One logged call of an AI trace.
 */
export type OutboundAITraceCall = {
  id: string;
  created_at: string;
  caller: string;
  model: string;
  status: number;
  outcome: 'known' | 'unknown' | 'error';
  input_tokens: number;
  output_tokens: number;
  cost_millicents: number;
  duration_ms: number;
  ttfb_ms: number | null;
  /**
   * The names of the tools the call's response requested.
   */
  tool_calls: Array<string>;
};

