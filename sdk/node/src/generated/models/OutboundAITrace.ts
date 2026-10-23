/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One AI trace's totals (ADR 20261020202035). app_id and end_user are its first call's; repeated_tool_calls is its current streak of calls that requested the same tools with the same arguments.
 */
export type OutboundAITrace = {
  trace_id: string;
  app_id: string | null;
  end_user: string | null;
  first_at: string;
  last_at: string;
  calls: number;
  error_calls: number;
  usage_unknown_calls: number;
  input_tokens: number;
  output_tokens: number;
  cost_millicents: number;
  /**
   * Tool calls the trace's responses requested.
   */
  tool_calls: number;
  repeated_tool_calls: number;
  models: Array<string>;
};

