/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIContentBody } from './OutboundAIContentBody.js';
/**
 * One target's replayed call. status is null without a response and outcome null when the call was refused before it reached the provider.
 */
export type OutboundAIReplayResult = {
  integration_id: string;
  model: string | null;
  status: number | null;
  /**
   * known, unknown or error; null when the call did not settle.
   */
  outcome: string | null;
  /**
   * Problem code of a refused or failed call.
   */
  error_code: string | null;
  input_tokens: number;
  output_tokens: number;
  cost_millicents: number;
  duration_ms: number;
  ttfb_ms: number | null;
  /**
   * Up to 4 KiB of the response text, redacted.
   */
  output_text: string;
  response: OutboundAIContentBody;
};

