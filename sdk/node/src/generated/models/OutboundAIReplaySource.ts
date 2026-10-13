/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The captured call a replay resends.
 */
export type OutboundAIReplaySource = {
  model: string;
  status: number;
  input_tokens: number;
  output_tokens: number;
  cost_millicents: number;
  duration_ms: number;
  ttfb_ms: number | null;
  /**
   * Text read from the captured response.
   */
  output_text: string | null;
};

